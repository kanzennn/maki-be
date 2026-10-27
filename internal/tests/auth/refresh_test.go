package auth

import (
	"net/http"
	"testing"
)

func refresh(h *harness, cookie, body string) reply {
	h.t.Helper()
	r := request{method: http.MethodPost, path: "/api/v1/auth/refresh", cookie: cookie}
	if body != "" {
		r.body = map[string]string{"refresh_token": body}
	}
	return h.do(r)
}

func TestRefreshRotatesTheCookie(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	_, first := h.login("kasir1", "kopi susu 123", "web")

	r := refresh(h, first, "").status(http.StatusOK, "")
	second := r.refreshCookie()
	if second == nil || second.Value == "" || second.Value == first {
		t.Fatalf("refresh cookie = %v, want a new token", second)
	}
	if s := r.session(); s.RefreshToken != "" || s.AccessToken == "" {
		t.Errorf("session = %+v, want an access token and no refresh token in the body", s)
	}
	h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: r.session().AccessToken}).status(http.StatusOK, "")

	// The new token refreshes again; the chain goes on.
	refresh(h, second.Value, "").status(http.StatusOK, "")
}

func TestRefreshInTheBodyForMobile(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	_, first := h.login("kasir1", "kopi susu 123", "mobile")

	r := refresh(h, "", first).status(http.StatusOK, "")
	s := r.session()
	if s.RefreshToken == "" || s.RefreshToken == first || r.refreshCookie() != nil {
		t.Errorf("session = %+v, cookie %v; want a new refresh token in the body and no cookie", s, r.refreshCookie())
	}
}

// Two tabs refreshing at once both send the old cookie: the second is told to
// retry, and the session survives.
func TestRefreshRaceIsSuperseded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	_, first := h.login("kasir1", "kopi susu 123", "web")
	second := refresh(h, first, "").status(http.StatusOK, "").refreshCookie().Value

	r := refresh(h, first, "").status(http.StatusConflict, "refresh_superseded")
	if r.refreshCookie() != nil {
		t.Error("refresh_superseded touched the cookie; the browser already holds the new one")
	}
	refresh(h, second, "").status(http.StatusOK, "")
}

// A token presented again after the grace period can only be a copy: the
// whole session ends, for the thief and the owner alike.
func TestRefreshReuseEndsTheSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	userID := h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	access, first := h.login("kasir1", "kopi susu 123", "web")
	second := refresh(h, first, "").status(http.StatusOK, "").refreshCookie().Value
	h.exec(`UPDATE refresh_tokens SET replaced_at = replaced_at - interval '1 minute' WHERE replaced_at IS NOT NULL`)

	r := refresh(h, first, "").status(http.StatusUnauthorized, "unauthorized")
	if c := r.refreshCookie(); c == nil || c.MaxAge >= 0 {
		t.Errorf("cookie after a refused refresh = %v, want it cleared", c)
	}

	refresh(h, second, "").status(http.StatusUnauthorized, "unauthorized")
	h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).status(http.StatusUnauthorized, "unauthorized")

	var logged int
	h.scalar(&logged, `SELECT count(*) FROM action_logs WHERE action = 'auth.session_revoked' AND user_id = $1
		AND after_json->>'reason' = 'refresh_token_reused'`, userID)
	if logged != 1 {
		t.Errorf("%d auth.session_revoked entries, want 1", logged)
	}
}

func TestRefreshRejects(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	h.createUser(testUser{username: "kasir2", password: "kopi susu 123"})

	refresh(h, "", "").status(http.StatusUnauthorized, "unauthorized")
	refresh(h, "not-a-token", "").status(http.StatusUnauthorized, "unauthorized")

	_, expired := h.login("kasir1", "kopi susu 123", "web")
	h.exec(`UPDATE refresh_tokens SET expires_at = now() - interval '1 second'`)
	refresh(h, expired, "").status(http.StatusUnauthorized, "unauthorized")

	// A deactivated user's session ends at its next refresh, if the access
	// token hasn't already been refused.
	_, disabled := h.login("kasir2", "kopi susu 123", "mobile")
	h.exec(`UPDATE users SET status = 'inactive' WHERE username = 'kasir2'`)
	refresh(h, "", disabled).status(http.StatusUnauthorized, "unauthorized")
	h.exec(`UPDATE users SET status = 'active' WHERE username = 'kasir2'`)
	refresh(h, "", disabled).status(http.StatusUnauthorized, "unauthorized") // stays ended
}

func TestDeactivatingAUserEndsTheirAccessAtOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	access, _ := h.login("kasir1", "kopi susu 123", "mobile")

	h.exec(`UPDATE users SET status = 'inactive' WHERE username = 'kasir1'`)
	// No waiting for the 15-minute token to expire.
	h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).status(http.StatusUnauthorized, "unauthorized")
}
