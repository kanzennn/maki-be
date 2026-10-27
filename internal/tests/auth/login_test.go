package auth

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/utils/password"
)

func loginBody(username, pw, client string) map[string]string {
	return map[string]string{"username": username, "password": pw, "client": client}
}

func TestLoginWebSetsAnHttpOnlyCookie(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	userID := h.createUser(testUser{username: "kasir1", password: "kopi susu 123", name: "Budi"})

	r := h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("kasir1", "kopi susu 123", "web")}).
		status(http.StatusOK, "")

	s := r.session()
	if s.AccessToken == "" || s.TokenType != "Bearer" || s.ExpiresIn != 900 {
		t.Errorf("session = %+v, want a Bearer token for 900 seconds", s)
	}
	if s.RefreshToken != "" || s.RefreshExpiresIn != 0 {
		t.Error("the web app got its refresh token in the body; it belongs in the HttpOnly cookie only")
	}
	if s.User.UserID != userID || s.User.Name != "Budi" || s.User.Language != "id" || s.User.OutletID == nil {
		t.Errorf("user = %+v", s.User)
	}

	c := r.refreshCookie()
	if c == nil || c.Value == "" {
		t.Fatal("no maki_refresh cookie")
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/api/v1/auth" || c.MaxAge != 30*24*3600 {
		t.Errorf("cookie = %+v, want HttpOnly, Secure, SameSite=Strict, Path=/api/v1/auth, 30 days", c)
	}

	var lastLogin *time.Time
	h.scalar(&lastLogin, `SELECT last_login_at FROM users WHERE user_id = $1`, userID)
	if lastLogin == nil {
		t.Error("last_login_at not set")
	}
	if got := h.actions(); !slices.Equal(got, []string{"auth.login"}) {
		t.Errorf("Log Action = %v, want [auth.login]", got)
	}

	// The access token works.
	h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: s.AccessToken}).status(http.StatusOK, "")
}

func TestLoginMobileGetsTheRefreshTokenInTheBody(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})

	r := h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("kasir1", "kopi susu 123", "mobile")}).
		status(http.StatusOK, "")

	s := r.session()
	if s.RefreshToken == "" || s.RefreshExpiresIn != 30*24*3600 {
		t.Errorf("session = %+v, want the refresh token in the body", s)
	}
	if r.refreshCookie() != nil {
		t.Error("the mobile app got a cookie")
	}
}

func TestLoginIgnoresUsernameCase(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir.bdg01", password: "kopi susu 123"})

	h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("  Kasir.BDG01 ", "kopi susu 123", "web")}).
		status(http.StatusOK, "")
}

func TestLoginFailuresLookAlike(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	knownID := h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	h.createUser(testUser{username: "kasir2", password: "kopi susu 123", inactive: true})

	wrong := h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("kasir1", "salah", "web")}).
		status(http.StatusUnauthorized, "invalid_credentials")
	unknown := h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("nobody", "salah", "web")}).
		status(http.StatusUnauthorized, "invalid_credentials")
	if strings.ReplaceAll(wrong.Body.String(), wrongRequestID(wrong), "") != strings.ReplaceAll(unknown.Body.String(), wrongRequestID(unknown), "") {
		t.Errorf("a wrong password and an unknown user answer differently:\n%s\n%s", wrong.Body, unknown.Body)
	}

	// A deactivated account: the right password says so, a wrong one doesn't.
	h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("kasir2", "salah", "web")}).
		status(http.StatusUnauthorized, "invalid_credentials")
	h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("kasir2", "kopi susu 123", "web")}).
		status(http.StatusForbidden, "account_disabled")

	var withUser, withoutUser int
	h.scalar(&withUser, `SELECT count(*) FROM action_logs WHERE action = 'auth.login_failed' AND user_id = $1`, knownID)
	h.scalar(&withoutUser, `SELECT count(*) FROM action_logs WHERE action = 'auth.login_failed' AND user_id IS NULL`)
	if withUser != 1 || withoutUser != 1 {
		t.Errorf("failed sign-ins logged: %d for the user, %d without one; want 1 and 1", withUser, withoutUser)
	}
}

// wrongRequestID returns the request ID in a response, which differs per
// request, so two bodies can be compared without it.
func wrongRequestID(r reply) string {
	return r.Header().Get("X-Request-ID")
}

func TestLoginIsRateLimitedPerAccountAndIP(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	h.createUser(testUser{username: "kasir2", password: "kopi susu 123"})

	for i := range 5 {
		h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("kasir1", "salah", "web"), ip: "203.0.113." + strconv.Itoa(i)}).
			status(http.StatusUnauthorized, "invalid_credentials")
	}
	// Five wrong passwords from five addresses lock the account — even for
	// the right password, from a new address.
	r := h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("kasir1", "kopi susu 123", "web"), ip: "198.51.100.1"}).
		status(http.StatusTooManyRequests, "rate_limited")
	if retry, err := strconv.Atoi(r.Header().Get("Retry-After")); err != nil || retry < 800 || retry > 900 {
		t.Errorf("Retry-After = %q, want about 15 minutes", r.Header().Get("Retry-After"))
	}
	// Another account is fine.
	h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("kasir2", "kopi susu 123", "web"), ip: "198.51.100.1"}).
		status(http.StatusOK, "")

	// Twenty failures from one address block it for every account.
	for i := range 20 {
		h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("nobody"+strconv.Itoa(i), "salah", "web"), ip: "192.0.2.50"}).
			status(http.StatusUnauthorized, "invalid_credentials")
	}
	h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("kasir2", "kopi susu 123", "web"), ip: "192.0.2.50"}).
		status(http.StatusTooManyRequests, "rate_limited")
}

func TestLoginSuccessClearsTheAccountsFailures(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})

	for range 4 {
		h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("kasir1", "salah", "web")}).
			status(http.StatusUnauthorized, "invalid_credentials")
	}
	h.login("kasir1", "kopi susu 123", "web")
	// The four earlier failures no longer count.
	for range 4 {
		h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("kasir1", "salah", "web")}).
			status(http.StatusUnauthorized, "invalid_credentials")
	}
	h.login("kasir1", "kopi susu 123", "web")
}

func TestLoginRehashesWeakerHashes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	old := password.NewHasher(password.Params{Memory: 512, Time: 1, Threads: 1}, 1)
	hash, err := old.Hash(t.Context(), "kopi susu 123")
	if err != nil {
		t.Fatal(err)
	}
	userID := h.createUser(testUser{username: "kasir1", hash: hash})

	h.login("kasir1", "kopi susu 123", "web")

	var stored string
	h.scalar(&stored, `SELECT password_hash FROM users WHERE user_id = $1`, userID)
	if !strings.HasPrefix(stored, "$argon2id$v=19$m=1024,t=1,p=1$") {
		t.Errorf("hash after sign-in = %q, want it rehashed with the current parameters", stored)
	}
	h.login("kasir1", "kopi susu 123", "web") // and it still works
}

func TestLoginValidatesTheRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: "username=kasir1"}).
		status(http.StatusBadRequest, "invalid_request")

	r := h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: map[string]string{"username": "kasir1", "client": "desktop"}}).
		status(http.StatusUnprocessableEntity, "validation_failed")
	fields, _ := r.errorDetails()["fields"].(map[string]any)
	if fields["password"] != "required" || fields["client"] != "oneof" || len(fields) != 2 {
		t.Errorf("fields = %v, want password: required and client: oneof", fields)
	}
}

func TestLoginErrorsFollowAcceptLanguage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	en := h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("nobody", "salah", "web"), lang: "en-US"})
	id := h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("nobody", "salah", "web")})
	if !strings.Contains(en.Body.String(), "Wrong username or password") || !strings.Contains(id.Body.String(), "Username atau password salah") {
		t.Errorf("messages: en %s, id %s", en.Body, id.Body)
	}
}

func TestMustChangePasswordLimitsTheSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "sandi sementara", mustChange: true})

	r := h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("kasir1", "sandi sementara", "web")}).
		status(http.StatusOK, "")
	s := r.session()
	if !s.User.MustChangePassword {
		t.Fatal("must_change_password = false")
	}

	h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: s.AccessToken}).status(http.StatusOK, "")
	h.do(request{method: http.MethodPatch, path: "/api/v1/me", bearer: s.AccessToken, body: map[string]any{"locale": "en"}}).
		status(http.StatusForbidden, "password_change_required")

	h.do(request{method: http.MethodPut, path: "/api/v1/me/password", bearer: s.AccessToken, body: map[string]string{
		"current_password": "sandi sementara", "new_password": "sandi pilihan sendiri",
	}}).status(http.StatusNoContent, "")

	// Loaded on each request, so the same token is now let through.
	h.do(request{method: http.MethodPatch, path: "/api/v1/me", bearer: s.AccessToken, body: map[string]any{"locale": "en"}}).
		status(http.StatusOK, "")
}
