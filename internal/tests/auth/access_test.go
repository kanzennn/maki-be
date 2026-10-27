package auth

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
)

func (h *harness) me(access string) models.MeResponse {
	h.t.Helper()
	return h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).status(http.StatusOK, "").me()
}

func TestPermissionsFollowTheRole(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.exec(`INSERT INTO roles (role_id, outlet_id, name) VALUES ('r1', 'o1', 'Kasir')`)
	h.exec(`INSERT INTO role_permissions (role_id, permission_code) VALUES ('r1', 'checkout.create'), ('r1', 'menu.view')`)
	staff := h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	h.exec(`INSERT INTO user_roles (user_id, role_id, outlet_id) VALUES ($1, 'r1', 'o1')`, staff)
	access, _ := h.login("kasir1", "kopi susu 123", "mobile")

	me := h.me(access)
	if !slices.Equal(me.Permissions, []string{"checkout.create", "menu.view"}) {
		t.Errorf("permissions = %v, want the role's", me.Permissions)
	}
	want := models.OutletSummary{OutletID: "o1", Code: "O1", Name: "Outlet", Timezone: "Asia/Jakarta", Locale: "id"}
	if me.Outlet == nil || *me.Outlet != want {
		t.Errorf("outlet = %+v, want %+v", me.Outlet, want)
	}

	// A change to the role applies from the next request, on the same
	// access token. A code the catalog doesn't know gives nothing.
	h.exec(`DELETE FROM role_permissions WHERE role_id = 'r1' AND permission_code = 'checkout.create'`)
	h.exec(`INSERT INTO role_permissions (role_id, permission_code) VALUES ('r1', 'log.view'), ('r1', 'report.export')`)
	if got := h.me(access).Permissions; !slices.Equal(got, []string{"log.view", "menu.view"}) {
		t.Errorf("after the change: %v, want [log.view menu.view]", got)
	}

	h.exec(`UPDATE roles SET deleted_at = now() WHERE role_id = 'r1'`)
	if got := h.me(access).Permissions; got == nil || len(got) != 0 {
		t.Errorf("with the role deleted: %#v, want []", got)
	}
}

func TestManagerHoldsEveryPermissionOfTheirOutlet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "manajer", password: "kopi susu 123", manager: true})
	access, _ := h.login("manajer", "kopi susu 123", "web")

	want := []string{
		"checkout.create", "dashboard.view", "log.view", "menu.manage", "menu.view", "order.void", "payment.manage",
		"role.manage", "role.view", "stock.adjust", "table.manage", "table.view", "user.manage", "user.view",
	}
	if got := h.me(access).Permissions; !slices.Equal(got, want) {
		t.Errorf("permissions = %v, want every one", got)
	}
}

func TestDeactivatingAnOutletEndsItsUsersAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	h.createUser(testUser{username: "owner", password: "kopi susu 123", owner: true, email: "owner@maki.test"})
	access, cookie := h.login("kasir1", "kopi susu 123", "web")
	ownerAccess, _ := h.login("owner", "kopi susu 123", "web")
	signIn := func(pw string) reply {
		return h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: map[string]string{
			"username": "kasir1", "password": pw, "client": "web",
		}})
	}

	h.exec(`UPDATE outlets SET status = 'inactive' WHERE outlet_id = 'o1'`)

	// At once, not when the access token expires.
	h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).status(http.StatusUnauthorized, "unauthorized")
	refresh(h, cookie, "").status(http.StatusUnauthorized, "unauthorized")
	var reason string
	h.scalar(&reason, `SELECT after_json->>'reason' FROM action_logs WHERE action = 'auth.session_revoked'`)
	if reason != "outlet_inactive" {
		t.Errorf("session_revoked reason = %q, want outlet_inactive", reason)
	}

	// Only someone with the password learns why.
	signIn("salah sekali").status(http.StatusUnauthorized, "invalid_credentials")
	signIn("kopi susu 123").status(http.StatusForbidden, "outlet_inactive")

	// The Owner belongs to no outlet and carries on.
	h.me(ownerAccess)

	h.exec(`UPDATE outlets SET status = 'active' WHERE outlet_id = 'o1'`)
	signIn("kopi susu 123").status(http.StatusOK, "")

	// A deleted outlet is closed for good.
	h.exec(`UPDATE outlets SET status = 'inactive', deleted_at = now() WHERE outlet_id = 'o1'`)
	signIn("kopi susu 123").status(http.StatusForbidden, "outlet_inactive")
}

func TestLanguageFollowsTheOutlet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.exec(`INSERT INTO outlets (outlet_id, code, name, timezone, locale)
		VALUES ('o2', 'O2', 'Outlet EN', 'Asia/Makassar', 'en')`)
	h.createUser(testUser{
		username: "kasir2", password: "kopi susu 123", outletID: "o2", email: "kasir2@maki.test", verified: true,
	})
	access, _ := h.login("kasir2", "kopi susu 123", "mobile")

	me := h.me(access)
	if me.Locale != nil || me.Language != "en" || me.Outlet == nil || me.Outlet.Timezone != "Asia/Makassar" {
		t.Errorf("me = %+v, want no choice of their own, so the outlet's en and its time zone", me)
	}

	// Emails follow the same rule.
	h.do(request{method: http.MethodPut, path: "/api/v1/me/password", bearer: access, body: map[string]string{
		"current_password": "kopi susu 123", "new_password": "es jeruk 456",
	}}).status(http.StatusNoContent, "")
	mails := h.runJobs()
	if len(mails) != 1 || mails[0].Subject != "Your Maki password was changed" {
		t.Errorf("mails = %+v, want one notice in English", mails)
	}

	// Their own choice wins.
	r := h.do(request{method: http.MethodPatch, path: "/api/v1/me", bearer: access, body: map[string]any{"locale": "id"}})
	if got := r.status(http.StatusOK, "").me().Language; got != "id" {
		t.Errorf("language = %q after choosing id, want id", got)
	}
}

// The staff service ends every session of a user whose account is
// deactivated, deleted, promoted or demoted, or whose password someone else
// reset.
func TestEndingEverySessionOfAUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	userID := h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	web, cookie := h.login("kasir1", "kopi susu 123", "web")
	mobile, token := h.login("kasir1", "kopi susu 123", "mobile")
	h.createUser(testUser{username: "kasir2", password: "kopi susu 123"})
	other, _ := h.login("kasir2", "kopi susu 123", "mobile")

	if err := h.db.InTx(t.Context(), func(ctx context.Context) error {
		return repositories.NewSessionRepository(h.db).RevokeUserSessions(ctx, userID, "")
	}); err != nil {
		t.Fatal(err)
	}

	for _, access := range []string{web, mobile} {
		h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).status(http.StatusUnauthorized, "unauthorized")
	}
	refresh(h, cookie, "").status(http.StatusUnauthorized, "unauthorized")
	refresh(h, "", token).status(http.StatusUnauthorized, "unauthorized")
	// Nobody else's.
	h.me(other)
}
