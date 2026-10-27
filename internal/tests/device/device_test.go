package device

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/response"
)

const devices = "/api/v1/outlets/o1/devices"

// activate activates a device of o1 as its Manager, in a request that
// carries current as its device cookie, and returns the device with its
// token, from the cookie it set or the body.
func (h *harness) activate(name, platform, current string) (models.DeviceResponse, string) {
	h.t.Helper()
	var resp models.ActivateDeviceResponse
	r := h.do(http.MethodPost, devices, "manager", current, map[string]any{"name": name, "platform": platform}).
		status(http.StatusCreated, "")
	r.decode(&resp)
	if c := r.cookie(); c != nil {
		return resp.Device, c.Value
	}
	if resp.Token == nil {
		h.t.Fatalf("no token in %s", r.Body)
	}
	return resp.Device, *resp.Token
}

// tokenHash is what the database should keep of a device token: its
// SHA-256, never the token.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// waitForLock waits until n sessions wait for the lock the query counts.
func (h *harness) waitForLock(n int, query string) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for waiting := 0; waiting < n; {
		if time.Now().After(deadline) {
			h.t.Fatalf("%d of %d requests waiting after 10s", waiting, n)
		}
		time.Sleep(10 * time.Millisecond)
		h.scalar(&waiting, query)
	}
}

func TestActivateInABrowser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := map[string]any{"name": " Kasir depan ", "platform": "web"}

	var resp models.ActivateDeviceResponse
	r := h.do(http.MethodPost, devices, "manager", "", body).status(http.StatusCreated, "")
	r.decode(&resp)
	d := resp.Device
	if d.Code != "K01" || d.Name != "Kasir depan" || d.Platform != "web" || d.Status != "active" ||
		d.ActivatedBy.Name != "Rina" || d.LastSeenAt == nil || d.RevokedAt != nil || d.RevokedBy != nil {
		t.Errorf("device = %+v", d)
	}
	if resp.Token != nil {
		t.Error("the token is in the body too: a browser gets it only as a cookie")
	}
	c := r.cookie()
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/api/v1" ||
		c.MaxAge != 400*24*60*60 || len(c.Value) != 43 {
		t.Fatalf("cookie = %+v", c)
	}
	// The database keeps only the token's hash.
	var stored string
	h.scalar(&stored, `SELECT token_hash FROM devices WHERE device_id = $1`, d.DeviceID)
	if stored != tokenHash(c.Value) || strings.Contains(stored, c.Value) {
		t.Errorf("token_hash = %q, want the token's SHA-256", stored)
	}
	current, err := h.svc.Current(t.Context(), "o1", c.Value)
	if err != nil || current.DeviceID != d.DeviceID || current.Code != "K01" {
		t.Errorf("Current = %+v, %v", current, err)
	}

	// A browser active in the outlet already isn't activated twice.
	h.do(http.MethodPost, devices, "manager", c.Value, body).status(http.StatusConflict, "device_already_active")
	if got := h.actions(d.DeviceID); !slices.Equal(got, []string{"device.activated"}) {
		t.Errorf("Log Action = %v", got)
	}
	var after string
	h.scalar(&after, `SELECT after_json::text FROM action_logs WHERE target_id = $1`, d.DeviceID)
	if after != `{"code": "K01", "name": "Kasir depan", "platform": "web"}` {
		t.Errorf("device.activated after = %s", after)
	}
}

func TestActivateTheApp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var resp models.ActivateDeviceResponse
	r := h.do(http.MethodPost, devices, "manager", "", map[string]any{"name": "Tablet", "platform": "android"}).
		status(http.StatusCreated, "")
	r.decode(&resp)
	if r.cookie() != nil {
		t.Error("a cookie for the app, which keeps its token itself")
	}
	if resp.Token == nil || len(*resp.Token) != 43 {
		t.Fatalf("token = %v, want it in the body", resp.Token)
	}
	if d, err := h.svc.Current(t.Context(), "o1", *resp.Token); err != nil || d.Platform != "android" {
		t.Errorf("Current = %+v, %v", d, err)
	}
}

func TestActivateRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for name, tt := range map[string]struct {
		body        map[string]any
		field, rule string
	}{
		"a blank name":        {map[string]any{"name": "   ", "platform": "web"}, "name", "required"},
		"a long name":         {map[string]any{"name": strings.Repeat("n", 51), "platform": "web"}, "name", "max"},
		"an unknown platform": {map[string]any{"name": "Kasir", "platform": "ios"}, "platform", "oneof"},
		"no platform":         {map[string]any{"name": "Kasir"}, "platform", "required"},
	} {
		t.Run(name, func(t *testing.T) {
			r := h.do(http.MethodPost, devices, "manager", "", tt.body).status(http.StatusUnprocessableEntity, "validation_failed")
			if r.fields()[tt.field] != tt.rule {
				t.Errorf("fields = %v, want %s: %s", r.fields(), tt.field, tt.rule)
			}
		})
	}

	// A revoked device may be activated anew, and so may another outlet's:
	// it is a new device of this outlet.
	revoked, token := h.activate("Kasir", "web", "")
	h.do(http.MethodDelete, devices+"/"+revoked.DeviceID, "manager", "", nil).status(http.StatusNoContent, "")
	if again, _ := h.activate("Kasir", "web", token); again.Code != "K02" {
		t.Errorf("the revoked device again = %+v, want K02", again)
	}
	var other models.ActivateDeviceResponse
	h.do(http.MethodPost, "/api/v1/outlets/o2/devices", "manager2", "", map[string]any{"name": "Kasir", "platform": "android"}).
		status(http.StatusCreated, "").decode(&other)
	if d, _ := h.activate("Kasir", "web", *other.Token); d.Code != "K03" {
		t.Errorf("another outlet's device here = %+v, want K03", d)
	}
}

// Activations wait for each other to take their codes. The test holds the
// outlet's lock until all five are waiting for it, so that without the
// lock they would read the same highest code; and a code is never used
// again.
func TestDeviceCodes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.WithoutCancel(t.Context())
	hold, err := h.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Back to the pool however the test ends, or the pool can't close; its
	// session's lock goes when the pool closes it.
	defer hold.Release()
	const key = `hashtextextended('devices:o1', 0)`
	if _, err := hold.Exec(ctx, `SELECT pg_advisory_lock(`+key+`)`); err != nil {
		t.Fatal(err)
	}

	codes := make([]string, 5)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Go(func() {
			r := h.do(http.MethodPost, devices, "manager", "", map[string]any{"name": fmt.Sprintf("Kasir %d", i), "platform": "android"})
			var resp models.ActivateDeviceResponse
			if r.Code != http.StatusCreated || json.Unmarshal(r.Body.Bytes(), &resp) != nil {
				codes[i] = fmt.Sprintf("status %d", r.Code)
				return
			}
			codes[i] = resp.Device.Code
		})
	}
	// Other tests' outlets are o1 too, so their activations may queue here
	// as well: at least five.
	h.waitForLock(5, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted
		AND ((classid::bigint << 32) | objid::bigint) = `+key)
	if _, err := hold.Exec(ctx, `SELECT pg_advisory_unlock(`+key+`)`); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	slices.Sort(codes)
	if want := []string{"K01", "K02", "K03", "K04", "K05"}; !slices.Equal(codes, want) {
		t.Fatalf("codes = %v, want %v", codes, want)
	}
	// The highest code, revoked, still counts.
	var k05 string
	h.scalar(&k05, `SELECT device_id FROM devices WHERE outlet_id = 'o1' AND code = 'K05'`)
	h.do(http.MethodDelete, devices+"/"+k05, "manager", "", nil).status(http.StatusNoContent, "")
	if d, _ := h.activate("Kasir baru", "web", ""); d.Code != "K06" {
		t.Errorf("after K05 is revoked the next is %s, want K06: codes are in old receipt numbers", d.Code)
	}
	// Each outlet counts its own.
	var other models.ActivateDeviceResponse
	h.do(http.MethodPost, "/api/v1/outlets/o2/devices", "manager2", "", map[string]any{"name": "Kasir", "platform": "web"}).
		status(http.StatusCreated, "").decode(&other)
	if other.Device.Code != "K01" {
		t.Errorf("another outlet's first device = %s, want K01", other.Device.Code)
	}
}

func TestListAndRevoke(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	front, frontToken := h.activate("Kasir depan", "web", "")
	tablet, _ := h.activate("Tablet", "android", "")

	h.do(http.MethodDelete, devices+"/"+front.DeviceID, "owner", "", nil).status(http.StatusNoContent, "")
	// Revoking it again changes nothing.
	h.do(http.MethodDelete, devices+"/"+front.DeviceID, "manager", "", nil).status(http.StatusNoContent, "")
	if got := h.actions(front.DeviceID); !slices.Equal(got, []string{"device.activated", "device.revoked"}) {
		t.Errorf("Log Action = %v", got)
	}
	if _, err := h.svc.Current(t.Context(), "o1", frontToken); !errors.Is(err, apperror.ErrDeviceRevoked) {
		t.Errorf("a revoked device's token: %v, want ErrDeviceRevoked", err)
	}

	var list response.List[models.DeviceResponse]
	h.do(http.MethodGet, devices, "owner", "", nil).status(http.StatusOK, "").decode(&list)
	var got []string
	for _, d := range list.Data {
		by := ""
		if d.RevokedBy != nil {
			by = " by " + d.RevokedBy.Name
		}
		got = append(got, d.Code+" "+d.Status+by)
	}
	// Those in use first.
	if want := []string{"K02 active", "K01 revoked by Faiz"}; !slices.Equal(got, want) || list.Meta.Total != 2 {
		t.Errorf("devices = %v (%d), want %v", got, list.Meta.Total, want)
	}
	if list.Data[0].DeviceID != tablet.DeviceID || list.Data[1].RevokedAt == nil {
		t.Errorf("list = %+v", list.Data)
	}

	h.do(http.MethodDelete, devices+"/nope", "manager", "", nil).status(http.StatusNotFound, "device_not_found")
	var theirs models.ActivateDeviceResponse
	h.do(http.MethodPost, "/api/v1/outlets/o2/devices", "manager2", "", map[string]any{"name": "Kasir", "platform": "web"}).
		status(http.StatusCreated, "").decode(&theirs)
	h.do(http.MethodDelete, devices+"/"+theirs.Device.DeviceID, "owner", "", nil).status(http.StatusNotFound, "device_not_found")
}

func TestCurrentDevice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := t.Context()
	d, token := h.activate("Kasir", "web", "")
	var theirs models.ActivateDeviceResponse
	h.do(http.MethodPost, "/api/v1/outlets/o2/devices", "manager2", "", map[string]any{"name": "Kasir", "platform": "android"}).
		status(http.StatusCreated, "").decode(&theirs)

	for name, bad := range map[string]string{"none": "", "an unknown token": "made-up", "another outlet's": *theirs.Token} {
		if _, err := h.svc.Current(ctx, "o1", bad); !errors.Is(err, apperror.ErrDeviceNotRegistered) {
			t.Errorf("%s: %v, want ErrDeviceNotRegistered", name, err)
		}
	}

	// A sale marks the device seen.
	h.exec(`UPDATE devices SET last_seen_at = NULL WHERE device_id = $1`, d.DeviceID)
	if err := h.db.InTx(ctx, func(ctx context.Context) error {
		held, err := h.svc.Hold(ctx, "o1", token)
		if err == nil && held.DeviceID != d.DeviceID {
			return fmt.Errorf("held %s, want %s", held.DeviceID, d.DeviceID)
		}
		return err
	}); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	var seen bool
	h.scalar(&seen, `SELECT last_seen_at IS NOT NULL FROM devices WHERE device_id = $1`, d.DeviceID)
	if !seen {
		t.Error("last_seen_at not set by a sale")
	}
}

// A revocation that commits while a sale waits for the device stops the
// sale: the sale holds the device before it checks it.
func TestRevocationStopsTheSaleWaitingForIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.WithoutCancel(t.Context())
	d, token := h.activate("Kasir", "web", "")

	revoke, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = revoke.Rollback(ctx) }()
	if _, err := revoke.Exec(ctx, `UPDATE devices SET revoked_at = now(), revoked_by = 'm1' WHERE device_id = $1`, d.DeviceID); err != nil {
		t.Fatal(err)
	}
	held := make(chan error, 1)
	go func() {
		held <- h.db.InTx(ctx, func(ctx context.Context) error {
			_, err := h.svc.Hold(ctx, "o1", token)
			return err
		})
	}()
	h.waitForLock(1, `SELECT count(DISTINCT pid) FROM pg_locks WHERE locktype = 'tuple' AND relation = 'devices'::regclass`)
	if err := revoke.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-held; !errors.Is(err, apperror.ErrDeviceRevoked) {
		t.Errorf("the waiting sale: %v, want ErrDeviceRevoked", err)
	}
}

func TestDeviceAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d, _ := h.activate("Kasir", "web", "")
	body := map[string]any{"name": "Kasir", "platform": "web"}

	// The Owner sees and revokes devices, but doesn't run an outlet's till.
	h.do(http.MethodPost, devices, "owner", "", body).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodGet, devices, "owner", "", nil).status(http.StatusOK, "")
	// A cashier does none of it.
	h.do(http.MethodPost, devices, "cashier", "", body).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodGet, devices, "cashier", "", nil).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodDelete, devices+"/"+d.DeviceID, "cashier", "", nil).status(http.StatusForbidden, "forbidden")
	// Another outlet's Manager can't reach it, and nobody signed in is turned away.
	h.do(http.MethodPost, devices, "manager2", "", body).status(http.StatusNotFound, "outlet_not_found")
	h.do(http.MethodGet, devices, "", "", nil).status(http.StatusUnauthorized, "unauthorized")

	var status string
	h.scalar(&status, `SELECT CASE WHEN revoked_at IS NULL THEN 'active' ELSE 'revoked' END FROM devices WHERE device_id = $1`, d.DeviceID)
	if status != "active" {
		t.Errorf("device = %s after the refused revoke", status)
	}
}
