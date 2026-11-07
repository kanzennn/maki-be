// Package media tests photos from the upload to the stored object: the API
// and the jobs against a database, with in-memory buckets and a stand-in for
// libvips, whose real conversion internal/utils/imaging tests.
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/jobs"
	"github.com/kervzent/maki-be/internal/middlewares"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/tests/dbtest"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/i18n"
	"github.com/kervzent/maki-be/internal/utils/imaging"
	"github.com/kervzent/maki-be/internal/utils/outbox"
	"github.com/kervzent/maki-be/internal/utils/storage"
)

const (
	userID   = "u1"
	outletID = "o1"
	menuID   = "m1"
)

// fakeConverter stands in for libvips: one small object per size, or a
// failure the test chose.
type fakeConverter struct {
	err   error
	calls int
}

func (f *fakeConverter) Convert(_ []byte, sizes []imaging.Size) ([][]byte, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]byte, len(sizes))
	for i := range sizes {
		out[i] = []byte("webp")
	}
	return out, nil
}

type harness struct {
	t               *testing.T
	pool            *pgxpool.Pool
	public, private *storage.Memory
	images          *fakeConverter
	media           *services.MediaService
	jobs            *jobs.MediaJobs
	router          *gin.Engine
	logs            *bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := dbtest.New(t)
	ctx := t.Context()
	for _, stmt := range []string{
		`INSERT INTO outlets (outlet_id, code, name, timezone, locale) VALUES ('o1', 'BDG01', 'Bandung', 'Asia/Jakarta', 'id')`,
		`INSERT INTO users (user_id, outlet_id, name, username, password_hash) VALUES ('u1', 'o1', 'Rina', 'rina', 'x')`,
		`INSERT INTO menu_categories (category_id, outlet_id, name) VALUES ('c1', 'o1', 'Minuman')`,
		`INSERT INTO menus (menu_id, outlet_id, category_id, name, price) VALUES ('m1', 'o1', 'c1', 'Kopi', 18000)`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	db := config.NewDatabase(pool)
	public, private := storage.NewMemory("public"), storage.NewMemory("private")
	audit := services.NewAuditService(repositories.NewAuditRepository(db))
	media := services.NewMediaService(db, repositories.NewMenuRepository(db), repositories.NewUserRepository(db), audit, private)
	images := &fakeConverter{}
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middlewares.RequestID(), middlewares.Recovery(logger), middlewares.ErrorHandler(logger))
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(models.WithPrincipal(c.Request.Context(), models.Principal{UserID: userID, OutletID: outletID, Locale: i18n.Default}))
		c.Next()
	})
	ctl := controllers.NewUploadController(media)
	router.POST("/uploads/presign", ctl.Presign)
	router.PUT("/me/photo", ctl.SetMyPhoto)
	router.DELETE("/me/photo", ctl.RemoveMyPhoto)

	return &harness{
		t: t, pool: pool, public: public, private: private, images: images, media: media, logs: logs, router: router,
		jobs: jobs.NewMediaJobs(media, public, private, images, logger),
	}
}

func (h *harness) do(method, path, body string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequestWithContext(h.t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

// upload puts a file where a client's upload lands.
func (h *harness) upload(name string, data []byte) string {
	h.t.Helper()
	key := models.UploadPrefix + name
	if err := h.private.Put(h.t.Context(), key, "image/png", "", data); err != nil {
		h.t.Fatal(err)
	}
	return key
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// drain runs every queued job the worker would, to the end.
func (h *harness) drain() {
	h.t.Helper()
	w := outbox.NewWorker(h.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), outbox.Options{PollInterval: 5 * time.Millisecond, MaxAttempts: 1})
	h.jobs.Register(w)
	ctx, cancel := context.WithCancel(h.t.Context())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var open int
		err := h.pool.QueryRow(h.t.Context(), `SELECT count(*) FROM outbox_jobs WHERE status IN ('pending', 'running')`).Scan(&open)
		if err != nil {
			h.t.Fatal(err)
		}
		if open == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

// queue asks for a menu photo as the menu service does.
func (h *harness) queueMenu(uploadKey string) {
	h.t.Helper()
	db := config.NewDatabase(h.pool)
	err := db.InTx(h.t.Context(), func(ctx context.Context) error {
		return h.media.QueueMenuImage(ctx, outletID, menuID, uploadKey)
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) imageKey() string {
	h.t.Helper()
	key, err := h.media.MenuImageKey(h.t.Context(), outletID, menuID)
	if err != nil {
		h.t.Fatal(err)
	}
	if key == nil {
		return ""
	}
	return *key
}

func (h *harness) photoKey() string {
	h.t.Helper()
	key, err := h.media.UserPhotoKey(h.t.Context(), userID)
	if err != nil {
		h.t.Fatal(err)
	}
	if key == nil {
		return ""
	}
	return *key
}

func TestPresign(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodPost, "/uploads/presign", `{"purpose":"menu_image","content_type":"image/webp","size":1024}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var ticket models.UploadTicketResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ticket); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ticket.UploadKey, models.UploadPrefix) || !strings.Contains(ticket.UploadURL, ticket.UploadKey) {
		t.Errorf("ticket = %+v", ticket)
	}
	if until := time.Until(ticket.ExpiresAt); until < 4*time.Minute || until > 5*time.Minute {
		t.Errorf("expires in %v", until)
	}
	for name, body := range map[string]string{
		"an svg":     `{"purpose":"menu_image","content_type":"image/svg+xml","size":1024}`,
		"over 2 MB":  `{"purpose":"menu_image","content_type":"image/png","size":2097153}`,
		"nothing":    `{"purpose":"menu_image","content_type":"image/png","size":0}`,
		"who knows":  `{"purpose":"backup","content_type":"image/png","size":10}`,
		"no purpose": `{"content_type":"image/png","size":10}`,
	} {
		if rec := h.do(http.MethodPost, "/uploads/presign", body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func TestWithoutStorage(t *testing.T) {
	var none *services.MediaService
	if _, err := none.Presign(t.Context(), "image/png", 1); !errors.Is(err, apperror.ErrStorageUnavailable) {
		t.Errorf("presign: %v", err)
	}
	if err := none.CheckUpload(t.Context(), "uploads/abc"); !errors.Is(err, apperror.ErrStorageUnavailable) {
		t.Errorf("claim: %v", err)
	}
}

func TestClaimChecksTheKey(t *testing.T) {
	h := newHarness(t)
	for _, key := range []string{"outlets/o1/menus/m1/a.webp", "uploads/../users/u1/a.webp", "uploads/", "abc"} {
		// Refused for its shape, before the storage is asked.
		if err := h.media.CheckUpload(t.Context(), key); err == nil || errors.Is(err, apperror.ErrUploadNotFound) {
			t.Errorf("%q: %v", key, err)
		}
	}
	if err := h.media.CheckUpload(t.Context(), "uploads/neverfinished"); !errors.Is(err, apperror.ErrUploadNotFound) {
		t.Errorf("unfinished: %v", err)
	}
	if err := h.media.CheckUpload(t.Context(), h.upload("finished", pngOf(t, 1, 1))); err != nil {
		t.Errorf("finished: %v", err)
	}
}

func TestMenuImageFromUploadToObject(t *testing.T) {
	h := newHarness(t)
	key := h.upload("first", pngOf(t, 10, 10))
	h.queueMenu(key)
	h.drain()

	want := "outlets/o1/menus/m1/first.webp"
	if got := h.imageKey(); got != want {
		t.Fatalf("image key = %q", got)
	}
	if got := h.public.Keys(); !slices.Equal(got, []string{"outlets/o1/menus/m1/first.webp", "outlets/o1/menus/m1/first_thumb.webp"}) {
		t.Errorf("public = %v", got)
	}
	if got := h.private.Keys(); len(got) != 0 {
		t.Errorf("the upload should be gone: %v", got)
	}
	if got := h.public.CacheControl(want); got != "public, max-age=31536000, immutable" {
		t.Errorf("cache control = %q", got)
	}

	// A new photo replaces the old one, and its old objects go.
	h.queueMenu(h.upload("second", pngOf(t, 10, 10)))
	h.drain()
	if got := h.imageKey(); got != "outlets/o1/menus/m1/second.webp" {
		t.Fatalf("image key = %q", got)
	}
	if got := h.public.Keys(); !slices.Equal(got, []string{"outlets/o1/menus/m1/second.webp", "outlets/o1/menus/m1/second_thumb.webp"}) {
		t.Errorf("public = %v", got)
	}
}

func TestRawUploadNeverReachesThePublicBucket(t *testing.T) {
	h := newHarness(t)
	data := pngOf(t, 10, 10)
	h.queueMenu(h.upload("raw", data))
	h.drain()
	for _, key := range h.public.Keys() {
		got, err := h.public.Get(t.Context(), key, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(got, data) {
			t.Errorf("%s is the raw upload", key)
		}
	}
}

func TestRejectedFilesAreDroppedNotRetried(t *testing.T) {
	big := make([]byte, models.MaxUploadBytes+1)
	for name, data := range map[string][]byte{
		"not an image": []byte("<script>alert(1)</script>"),
		"too many px":  pngOf(t, models.MaxImageSide+1, 1),
		"too many mb":  big,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.queueMenu(h.upload("bad", data))
			h.drain()
			if got := h.imageKey(); got != "" {
				t.Errorf("image key = %q", got)
			}
			if h.images.calls != 0 {
				t.Error("the file should be refused before it is opened")
			}
			if got := h.private.Keys(); len(got) != 0 {
				t.Errorf("the upload should be gone: %v", got)
			}
			if !strings.Contains(h.logs.String(), "upload rejected") {
				t.Errorf("not logged: %s", h.logs)
			}
			var failed int
			if err := h.pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_jobs WHERE status <> 'done'`).Scan(&failed); err != nil {
				t.Fatal(err)
			}
			if failed != 0 {
				t.Errorf("%d jobs would run again", failed)
			}
		})
	}
}

func TestConverterRejectionKeepsTheOldPhoto(t *testing.T) {
	h := newHarness(t)
	h.queueMenu(h.upload("good", pngOf(t, 10, 10)))
	h.drain()
	h.images.err = imaging.ErrRejected
	h.queueMenu(h.upload("broken", pngOf(t, 10, 10)))
	h.drain()
	if got := h.imageKey(); got != "outlets/o1/menus/m1/good.webp" {
		t.Errorf("image key = %q", got)
	}
	if got := h.private.Keys(); len(got) != 0 {
		t.Errorf("the rejected upload should be gone: %v", got)
	}
}

func TestStorageTroubleIsRetried(t *testing.T) {
	h := newHarness(t)
	h.images.err = errors.New("out of memory")
	key := h.upload("later", pngOf(t, 10, 10))
	h.queueMenu(key)
	h.drain()
	if got := h.private.Keys(); !slices.Equal(got, []string{key}) {
		t.Errorf("the upload should wait for another attempt: %v", got)
	}
}

func TestSecondAttemptAfterTheRowWasSet(t *testing.T) {
	h := newHarness(t)
	// As if a first attempt was cut off right after its transaction: the row
	// is set, and its clean-up is queued apart.
	key := "uploads/again"
	err := h.media.AttachMenuImage(t.Context(), outletID, menuID, models.MenuImageKey(outletID, menuID, key), key)
	if err != nil {
		t.Fatal(err)
	}
	h.queueMenu(key)
	h.drain()
	if h.images.calls != 0 {
		t.Error("the job should see it is done")
	}
}

func TestMenuGoneBeforeTheJobRuns(t *testing.T) {
	h := newHarness(t)
	if _, err := h.pool.Exec(t.Context(), `UPDATE menus SET deleted_at = now() WHERE menu_id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	h.queueMenu(h.upload("late", pngOf(t, 10, 10)))
	h.drain()
	if got := h.public.Keys(); len(got) != 0 {
		t.Errorf("public = %v", got)
	}
	if got := h.private.Keys(); len(got) != 0 {
		t.Errorf("private = %v", got)
	}
}

func TestRemoveMenuImage(t *testing.T) {
	h := newHarness(t)
	h.queueMenu(h.upload("gone", pngOf(t, 10, 10)))
	h.drain()
	db := config.NewDatabase(h.pool)
	err := db.InTx(t.Context(), func(ctx context.Context) error {
		removed, err := h.media.RemoveMenuImage(ctx, outletID, menuID)
		if !removed {
			t.Error("the menu had a photo")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	h.drain()
	if got := h.imageKey(); got != "" {
		t.Errorf("image key = %q", got)
	}
	if got := h.public.Keys(); len(got) != 0 {
		t.Errorf("public = %v", got)
	}
}

func TestOwnPhoto(t *testing.T) {
	h := newHarness(t)
	if rec := h.do(http.MethodPut, "/me/photo", `{"upload_key":"uploads/neverfinished"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unfinished: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do(http.MethodPut, "/me/photo", `{"upload_key":"users/u1/a.webp"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("not an upload: %d %s", rec.Code, rec.Body)
	}
	key := h.upload("face", pngOf(t, 10, 10))
	if rec := h.do(http.MethodPut, "/me/photo", `{"upload_key":"`+key+`"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	h.drain()
	want := "users/u1/face.webp"
	if got := h.photoKey(); got != want {
		t.Fatalf("photo key = %q", got)
	}
	// A face stays in the private bucket.
	if got := h.private.Keys(); !slices.Equal(got, []string{want}) {
		t.Errorf("private = %v", got)
	}
	if got := h.public.Keys(); len(got) != 0 {
		t.Errorf("public = %v", got)
	}

	if rec := h.do(http.MethodDelete, "/me/photo", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	h.drain()
	if got := h.photoKey(); got != "" {
		t.Errorf("photo key = %q", got)
	}
	if got := h.private.Keys(); len(got) != 0 {
		t.Errorf("private = %v", got)
	}
	if rec := h.do(http.MethodDelete, "/me/photo", ""); rec.Code != http.StatusNoContent {
		t.Errorf("again: %d", rec.Code)
	}

	var actions []string
	rows, err := h.pool.Query(t.Context(), `SELECT action FROM action_logs ORDER BY occurred_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, a)
	}
	if !slices.Equal(actions, []string{"user.photo_changed", "user.photo_removed"}) {
		t.Errorf("logged %v", actions)
	}
}

func TestSweepTakesOnlyOldUploads(t *testing.T) {
	h := newHarness(t)
	fresh, stale := h.upload("fresh", []byte("x")), h.upload("stale", []byte("x"))
	h.private.Age(stale, jobs.UploadMaxAge+time.Minute)
	photo := "users/u1/old.webp"
	if err := h.private.Put(t.Context(), photo, "image/webp", "", []byte("x")); err != nil {
		t.Fatal(err)
	}
	h.private.Age(photo, 100*jobs.UploadMaxAge)

	n, err := h.jobs.Sweep(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("swept %d", n)
	}
	if got := h.private.Keys(); !slices.Equal(got, []string{fresh, photo}) {
		t.Errorf("left %v", got)
	}
}

func (h *harness) principal() models.Principal {
	return models.Principal{UserID: userID, OutletID: outletID, IsManager: true, Locale: i18n.Default}
}

func TestMenuThroughItsService(t *testing.T) {
	h := newHarness(t)
	db := config.NewDatabase(h.pool)
	repo := repositories.NewMenuRepository(db).WithImageURLs(h.public.URL)
	audit := services.NewAuditService(repositories.NewAuditRepository(db))
	menus := services.NewMenuService(db, repo, repositories.NewCategoryRepository(db), repositories.NewStockRepository(db), audit, h.media)
	input := func(key *string, remove bool) models.MenuInput {
		price := int64(18000)
		return models.MenuInput{Name: "Es Jeruk", CategoryID: "c1", Price: &price, UploadKey: key, RemoveImage: remove}
	}
	key := h.upload("created", pngOf(t, 10, 10))
	m, err := menus.CreateMenu(t.Context(), h.principal(), "", outletID, input(&key, false))
	if err != nil {
		t.Fatal(err)
	}
	if m.ImageURL != nil {
		t.Error("the photo shows only once the worker is done")
	}
	h.drain()
	m, err = menus.Menu(t.Context(), outletID, m.MenuID)
	if err != nil {
		t.Fatal(err)
	}
	base := "https://public.test/outlets/o1/menus/" + m.MenuID + "/created"
	if m.ImageURL == nil || *m.ImageURL != base+".webp" || m.ImageThumbURL == nil || *m.ImageThumbURL != base+"_thumb.webp" {
		t.Errorf("urls: %v %v", m.ImageURL, m.ImageThumbURL)
	}
	if got := models.NewCatalogMenuResponse(m); got.ImageThumbURL == nil {
		t.Error("the catalog should send the small photo")
	}

	// A save that changes nothing but the photo still counts.
	key = h.upload("replaced", pngOf(t, 10, 10))
	if _, err := menus.UpdateMenu(t.Context(), h.principal(), "", outletID, m.MenuID, input(&key, false)); err != nil {
		t.Fatal(err)
	}
	h.drain()
	m, err = menus.Menu(t.Context(), outletID, m.MenuID)
	if err != nil {
		t.Fatal(err)
	}
	if m.ImageURL == nil || !strings.HasSuffix(*m.ImageURL, "/replaced.webp") {
		t.Errorf("after replacing: %v", m.ImageURL)
	}
	if got := len(h.public.Keys()); got != 2 {
		t.Errorf("the old objects should go: %v", h.public.Keys())
	}

	// A save that doesn't mention it keeps it.
	if _, err := menus.UpdateMenu(t.Context(), h.principal(), "", outletID, m.MenuID, input(nil, false)); err != nil {
		t.Fatal(err)
	}
	if m, _ = menus.Menu(t.Context(), outletID, m.MenuID); m.ImageURL == nil {
		t.Error("the photo should stay")
	}

	if _, err := menus.UpdateMenu(t.Context(), h.principal(), "", outletID, m.MenuID, input(nil, true)); err != nil {
		t.Fatal(err)
	}
	h.drain()
	if m, _ = menus.Menu(t.Context(), outletID, m.MenuID); m.ImageURL != nil {
		t.Errorf("after removing: %v", m.ImageURL)
	}
	if got := h.public.Keys(); len(got) != 0 {
		t.Errorf("public = %v", got)
	}

	var images []string
	rows, err := h.pool.Query(t.Context(), `SELECT after_json->>'image' FROM action_logs WHERE action LIKE 'menu.%' ORDER BY occurred_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var v *string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v != nil {
			images = append(images, *v)
		}
	}
	if !slices.Equal(images, []string{"new", "new", "removed"}) {
		t.Errorf("logged %v", images)
	}
}

func TestStaffPhotoThroughItsService(t *testing.T) {
	h := newHarness(t)
	db := config.NewDatabase(h.pool)
	if _, err := h.pool.Exec(t.Context(), `
		INSERT INTO users (user_id, outlet_id, name, username, password_hash, is_owner, is_manager, email) VALUES
			('owner', NULL, 'Faiz', 'owner', 'x', true, false, 'owner@maki.test'),
			('m2', 'o1', 'Dewi', 'dewi', 'x', false, true, NULL)`); err != nil {
		t.Fatal(err)
	}
	repo := repositories.NewUserRepository(db).WithPhotoURLs(func(ctx context.Context, key string) (string, error) {
		return h.private.PresignGet(ctx, key, time.Minute)
	})
	audit := services.NewAuditService(repositories.NewAuditRepository(db))
	staff := services.NewStaffService(db, repo, repositories.NewRoleRepository(db), nil, audit, repositories.NewSessionRepository(db), h.media)
	owner := models.Principal{UserID: "owner", IsOwner: true, Locale: i18n.Default}

	key := h.upload("dewi", pngOf(t, 10, 10))
	in := models.StaffInput{Name: "Dewi", IsManager: true, UploadKey: &key}
	if _, err := staff.UpdateStaff(t.Context(), owner, "", outletID, "m2", in); err != nil {
		t.Fatal(err)
	}
	h.drain()
	s, err := staff.Staff(t.Context(), outletID, "m2")
	if err != nil {
		t.Fatal(err)
	}
	if s.PhotoURL == nil || !strings.Contains(*s.PhotoURL, "users/m2/dewi.webp") || !strings.Contains(*s.PhotoURL, "signed=get") {
		t.Fatalf("photo url: %v", s.PhotoURL)
	}
	if !slices.Contains(h.private.Keys(), "users/m2/dewi.webp") {
		t.Errorf("private = %v", h.private.Keys())
	}

	in = models.StaffInput{Name: "Dewi", IsManager: true, RemovePhoto: true}
	if _, err := staff.UpdateStaff(t.Context(), owner, "", outletID, "m2", in); err != nil {
		t.Fatal(err)
	}
	h.drain()
	if s, _ = staff.Staff(t.Context(), outletID, "m2"); s.PhotoURL != nil {
		t.Errorf("after removing: %v", s.PhotoURL)
	}
}
