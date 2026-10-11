package auth

import (
	"net/http"
	"reflect"
	"testing"
)

func TestLogoutWithTheCookie(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	access, cookie := h.login("kasir1", "kopi susu 123", "web")
	otherAccess, _ := h.login("kasir1", "kopi susu 123", "web") // a second device

	r := h.do(request{method: http.MethodPost, path: "/api/v1/auth/logout", cookie: cookie}).status(http.StatusNoContent, "")
	if c := r.refreshCookie(); c == nil || c.MaxAge >= 0 || c.Path != "/api/v1/auth" {
		t.Errorf("cookie = %v, want it cleared on the same path", c)
	}

	h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).status(http.StatusUnauthorized, "unauthorized")
	refresh(h, cookie, "").status(http.StatusUnauthorized, "unauthorized")
	// Only that session ended.
	h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: otherAccess}).status(http.StatusOK, "")

	// Signing out twice is fine.
	h.do(request{method: http.MethodPost, path: "/api/v1/auth/logout", cookie: cookie}).status(http.StatusNoContent, "")

	var logouts int
	h.scalar(&logouts, `SELECT count(*) FROM action_logs WHERE action = 'auth.logout'`)
	if logouts != 1 {
		t.Errorf("%d auth.logout entries, want 1: the repeat ended nothing", logouts)
	}
}

func TestLogoutWithTheBodyOrTheAccessToken(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})

	access, token := h.login("kasir1", "kopi susu 123", "mobile")
	h.do(request{method: http.MethodPost, path: "/api/v1/auth/logout", body: map[string]string{"refresh_token": token}}).
		status(http.StatusNoContent, "")
	h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).status(http.StatusUnauthorized, "unauthorized")

	// Without a refresh token, the access token names the session.
	access, token = h.login("kasir1", "kopi susu 123", "mobile")
	h.do(request{method: http.MethodPost, path: "/api/v1/auth/logout", bearer: access}).status(http.StatusNoContent, "")
	refresh(h, "", token).status(http.StatusUnauthorized, "unauthorized")

	// Nothing at all is fine too.
	h.do(request{method: http.MethodPost, path: "/api/v1/auth/logout"}).status(http.StatusNoContent, "")
}

func TestGetMe(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	userID := h.createUser(testUser{username: "owner", password: "kopi susu 123", name: "Faiz", owner: true, email: "owner@maki.test", verified: true})
	access, _ := h.login("owner", "kopi susu 123", "web")

	me := h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).status(http.StatusOK, "").me()
	want := meResponse{
		UserID: userID, Username: "owner", Name: "Faiz", Email: me.Email, EmailVerified: true,
		Language: "id", IsOwner: true,
		// The Owner looks into every outlet but runs none.
		Permissions: []string{"dashboard.view", "log.view", "menu.view", "role.view", "table.view", "user.manage", "user.view"},
	}
	if !reflect.DeepEqual(me, want) || me.Email == nil || *me.Email != "owner@maki.test" {
		t.Errorf("me = %+v, want %+v with email owner@maki.test", me, want)
	}
}

func TestPatchMeSetsTheLanguage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	access, _ := h.login("kasir1", "kopi susu 123", "web")
	patch := func(body any) response {
		return h.do(request{method: http.MethodPatch, path: "/api/v1/me", bearer: access, body: body})
	}

	me := patch(map[string]any{"locale": "en"}).status(http.StatusOK, "").me()
	if me.Locale == nil || *me.Locale != "en" || me.Language != "en" {
		t.Errorf("after en: locale %v, language %q", me.Locale, me.Language)
	}
	// The next request's principal sees it too: the language follows the
	// user to every device.
	me = h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).status(http.StatusOK, "").me()
	if me.Language != "en" {
		t.Errorf("GET /me language = %q, want en", me.Language)
	}

	me = patch(map[string]any{"locale": nil}).status(http.StatusOK, "").me()
	if me.Locale != nil || me.Language != "id" {
		t.Errorf("after null: locale %v, language %q; want nil and id", me.Locale, me.Language)
	}

	r := patch(map[string]any{"locale": "fr"}).status(http.StatusUnprocessableEntity, "validation_failed")
	if f, _ := r.errorDetails()["fields"].(map[string]any); f["locale"] != "oneof" {
		t.Errorf("fields = %v", r.errorDetails())
	}
	r = patch(map[string]any{}).status(http.StatusUnprocessableEntity, "validation_failed")
	if f, _ := r.errorDetails()["fields"].(map[string]any); f["locale"] != "required" {
		t.Errorf("fields = %v", r.errorDetails())
	}
	patch(map[string]any{"locale": 5}).status(http.StatusBadRequest, "invalid_request")
}
