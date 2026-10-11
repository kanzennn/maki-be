package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/order"
	"github.com/kervzent/maki-be/internal/outlet"
	"github.com/kervzent/maki-be/internal/payment"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db/dbtest"
	"github.com/kervzent/maki-be/internal/platform/id"
	"github.com/kervzent/maki-be/internal/platform/mail"
	"github.com/kervzent/maki-be/internal/platform/middleware"
	"github.com/kervzent/maki-be/internal/platform/outbox"
	"github.com/kervzent/maki-be/internal/platform/password"
	"github.com/kervzent/maki-be/internal/user"
)

// These tests drive the module the way clients do: HTTP requests through the
// real middleware and handlers into a PostgreSQL schema of their own, with
// the emailed jobs run by a real outbox worker into a fake mailer.

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

const webURL = "https://admin.maki.test"

// Cheap argon2id parameters keep the tests fast.
var testParams = password.Params{Memory: 1024, Time: 1, Threads: 1}

type harness struct {
	t      *testing.T
	pool   *pgxpool.Pool
	svc    *Service
	router *gin.Engine
	mailer *fakeMailer
	logger *slog.Logger
	hasher *password.Hasher
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := dbtest.New(t)
	logger := slog.New(slog.NewTextHandler(t.Output(), &slog.HandlerOptions{Level: slog.LevelWarn}))
	tokens, err := authn.NewTokens([]byte("0123456789abcdef0123456789abcdef"), AccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	hasher := password.NewHasher(testParams, 4)
	users := user.NewService(pool, hasher, audit.Recorder{}, Sessions{})
	svc := NewService(pool, users, outlet.NewService(pool, users, payment.NewService(pool), order.Records{}, audit.Recorder{}), tokens, hasher, audit.Recorder{}, logger)

	// The same groups as cmd/api's router.
	r := gin.New()
	r.Use(middleware.RequestID(), middleware.Recovery(logger))
	public := r.Group("/api/v1")
	authed := public.Group("", middleware.Authenticate(tokens, svc, logger))
	protected := authed.Group("", middleware.RequirePasswordChanged())
	NewHandler(svc, tokens, logger, true).Routes(public, authed, protected)

	h := &harness{t: t, pool: pool, svc: svc, router: r, mailer: &fakeMailer{}, logger: logger, hasher: hasher}
	// Everyone but the Owner belongs to an outlet; testUser puts them in o1.
	h.exec(`INSERT INTO outlets (outlet_id, code, name, timezone, locale)
		VALUES ('o1', 'O1', 'Outlet', 'Asia/Jakarta', 'id')`)
	return h
}

// testUser is a row to insert.
type testUser struct {
	username, password, name string
	email                    string // empty: none
	verified                 bool
	owner, manager           bool
	outletID                 string // defaults to "o1" for everyone but the Owner
	mustChange, inactive     bool
	hash                     string // overrides the hash of password
}

func (h *harness) createUser(u testUser) string {
	h.t.Helper()
	if u.hash == "" {
		var err error
		if u.hash, err = h.hasher.Hash(h.t.Context(), u.password); err != nil {
			h.t.Fatal(err)
		}
	}
	var outlet, email, verifiedAt any
	if !u.owner {
		outlet = "o1"
		if u.outletID != "" {
			outlet = u.outletID
		}
	}
	if u.email != "" {
		email = u.email
		if u.verified {
			verifiedAt = time.Now()
		}
	}
	if u.name == "" {
		u.name = "Test " + u.username
	}
	status := "active"
	if u.inactive {
		status = "inactive"
	}
	userID := id.New()
	h.exec(`INSERT INTO users (user_id, outlet_id, name, username, email, email_verified_at, password_hash,
			must_change_password, is_owner, is_manager, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		userID, outlet, u.name, u.username, email, verifiedAt, u.hash, u.mustChange, u.owner, u.manager, status)
	return userID
}

func (h *harness) exec(sql string, args ...any) {
	h.t.Helper()
	if _, err := h.pool.Exec(context.WithoutCancel(h.t.Context()), sql, args...); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
}

func (h *harness) scalar(dst any, sql string, args ...any) {
	h.t.Helper()
	if err := h.pool.QueryRow(h.t.Context(), sql, args...).Scan(dst); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
}

// actions returns the Log Action's actions, oldest first.
func (h *harness) actions() []string {
	h.t.Helper()
	rows, err := h.pool.Query(h.t.Context(), `SELECT action FROM action_logs ORDER BY created_at, action_log_id`)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

type request struct {
	method, path string
	body         any
	bearer       string
	cookie       string // the refresh cookie's value
	ip           string
	lang         string
}

type response struct {
	*httptest.ResponseRecorder
	t *testing.T
}

func (h *harness) do(r request) response {
	h.t.Helper()
	var body *bytes.Reader
	switch b := r.body.(type) {
	case nil:
		body = bytes.NewReader(nil)
	case string:
		body = bytes.NewReader([]byte(b))
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			h.t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	req := httptest.NewRequestWithContext(h.t.Context(), r.method, r.path, body)
	req.Header.Set("Content-Type", "application/json")
	if r.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+r.bearer)
	}
	if r.cookie != "" {
		// A cookie the client sends: attributes only mean something on
		// one a server sets.
		req.AddCookie(&http.Cookie{Name: refreshCookie, Value: r.cookie}) //nolint:gosec // see above
	}
	if r.ip != "" {
		req.RemoteAddr = r.ip + ":4000"
	}
	if r.lang != "" {
		req.Header.Set("Accept-Language", r.lang)
	}
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return response{w, h.t}
}

// status asserts the response's status and, for an error, its code.
func (r response) status(want int, code string) response {
	r.t.Helper()
	if r.Code != want {
		r.t.Fatalf("status = %d, want %d: %s", r.Code, want, r.Body)
	}
	if code != "" {
		if got := r.errorCode(); got != code {
			r.t.Fatalf("error code = %q, want %q: %s", got, code, r.Body)
		}
	}
	return r
}

func (r response) errorCode() string {
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &env)
	return env.Error.Code
}

func (r response) errorDetails() map[string]any {
	var env struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &env)
	return env.Error.Details
}

func (r response) session() sessionResponse {
	r.t.Helper()
	var s sessionResponse
	if err := json.Unmarshal(r.Body.Bytes(), &s); err != nil {
		r.t.Fatalf("not a session: %v: %s", err, r.Body)
	}
	return s
}

func (r response) me() meResponse {
	r.t.Helper()
	var m meResponse
	if err := json.Unmarshal(r.Body.Bytes(), &m); err != nil {
		r.t.Fatalf("not a user: %v: %s", err, r.Body)
	}
	return m
}

// refreshCookie returns the maki_refresh cookie the response set, if any.
func (r response) refreshCookie() *http.Cookie {
	for _, c := range r.Result().Cookies() {
		if c.Name == refreshCookie {
			return c
		}
	}
	return nil
}

// login signs in and returns the session, with the refresh token from the
// cookie (web) or the body (mobile).
func (h *harness) login(username, pw, client string) (access, refresh string) {
	h.t.Helper()
	r := h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: map[string]string{
		"username": username, "password": pw, "client": client,
	}}).status(http.StatusOK, "")
	s := r.session()
	if client == "web" {
		return s.AccessToken, r.refreshCookie().Value
	}
	return s.AccessToken, s.RefreshToken
}

// runJobs runs the outbox until no job is waiting, and returns the emails
// sent. A job left failing — or retrying — fails the test with its error.
func (h *harness) runJobs() []mail.Message {
	h.t.Helper()
	w := outbox.NewWorker(h.pool, h.logger, outbox.Options{PollInterval: 5 * time.Millisecond, MaxAttempts: 1})
	users := user.NewService(h.pool, h.hasher, audit.Recorder{}, Sessions{})
	NewJobs(h.pool, users, outlet.NewService(h.pool, users, payment.NewService(h.pool), order.Records{}, audit.Recorder{}), h.mailer, webURL).Register(w)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		var open int
		h.scalar(&open, `SELECT count(*) FROM outbox_jobs WHERE status IN ('pending', 'running')`)
		if open == 0 {
			break
		}
		if time.Now().After(deadline) {
			h.t.Fatal("jobs still waiting after 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
	var failed string
	h.scalar(&failed, `SELECT COALESCE(string_agg(kind || ': ' || COALESCE(last_error, ''), '; '), '') FROM outbox_jobs WHERE status = 'failed'`)
	if failed != "" {
		h.t.Fatalf("jobs failed: %s", failed)
	}
	return h.mailer.take()
}

type fakeMailer struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (m *fakeMailer) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *fakeMailer) take() []mail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.sent
	m.sent = nil
	return out
}

var linkToken = regexp.MustCompile(regexp.QuoteMeta(webURL) + `/([a-z-]+)#token=([A-Za-z0-9_-]+)`)

// tokenIn returns the token of the link to path in msg, failing if there is
// none.
func tokenIn(t *testing.T, msg mail.Message, path string) string {
	t.Helper()
	m := linkToken.FindStringSubmatch(msg.Text)
	if m == nil || m[1] != strings.TrimPrefix(path, "/") {
		t.Fatalf("no %s link in the email:\n%s", path, msg.Text)
	}
	if !strings.Contains(msg.HTML, m[0]) {
		t.Errorf("the HTML part lacks the link %s", m[0])
	}
	return m[2]
}
