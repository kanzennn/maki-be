package middleware

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/authn"
)

// fakeLoader answers LoadPrincipal from a map of session ID → principal.
type fakeLoader struct {
	principals map[string]authn.Principal
	err        error // returned instead, when set
}

func (f fakeLoader) LoadPrincipal(_ context.Context, s authn.Session) (authn.Principal, error) {
	if f.err != nil {
		return authn.Principal{}, f.err
	}
	p, ok := f.principals[s.SessionID]
	if !ok || p.UserID != s.UserID {
		return authn.Principal{}, authn.ErrNoSession
	}
	return p, nil
}

type authFixture struct {
	router *gin.Engine
	tokens *authn.Tokens
	logs   *bytes.Buffer
}

func newAuthFixture(t *testing.T, loader authn.PrincipalLoader) authFixture {
	t.Helper()
	tokens, err := authn.NewTokens([]byte("0123456789abcdef0123456789abcdef"), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	r := gin.New()
	r.Use(Logger(logger))
	authed := r.Group("/", Authenticate(tokens, loader, logger))
	authed.GET("/me", func(c *gin.Context) {
		p, _ := authn.PrincipalFrom(c.Request.Context())
		c.String(http.StatusOK, p.UserID)
	})
	authed.Group("/", RequirePasswordChanged()).GET("/orders", func(c *gin.Context) {
		c.String(http.StatusOK, "orders")
	})
	r.Group("/", RequirePasswordChanged()).GET("/miswired", func(c *gin.Context) {
		c.String(http.StatusOK, "reached without a principal")
	})
	return authFixture{router: r, tokens: tokens, logs: &logs}
}

func (f authFixture) get(t *testing.T, path, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func (f authFixture) token(t *testing.T, userID, sessionID string) string {
	t.Helper()
	token, _, err := f.tokens.Issue(authn.Session{UserID: userID, SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("not the error envelope: %s", w.Body)
	}
	return env.Error.Code
}

var activeUser = authn.Principal{UserID: "u1", SessionID: "s1", Username: "kasir1"}

func TestAuthenticateAcceptsAValidToken(t *testing.T) {
	f := newAuthFixture(t, fakeLoader{principals: map[string]authn.Principal{"s1": activeUser}})

	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		w := f.get(t, "/me", scheme+" "+f.token(t, "u1", "s1"))
		if w.Code != http.StatusOK || w.Body.String() != "u1" {
			t.Errorf("%s: %d %s, want 200 with the principal's ID", scheme, w.Code, w.Body)
		}
	}
	if !bytes.Contains(f.logs.Bytes(), []byte(`"user_id":"u1"`)) {
		t.Errorf("the access log doesn't name the user: %s", f.logs)
	}
}

func TestAuthenticateRejects(t *testing.T) {
	f := newAuthFixture(t, fakeLoader{principals: map[string]authn.Principal{"s1": activeUser}})
	valid := f.token(t, "u1", "s1")

	for name, header := range map[string]string{
		"no header":       "",
		"basic auth":      "Basic " + base64.StdEncoding.EncodeToString([]byte("u1:not-a-token")),
		"empty bearer":    "Bearer ",
		"bare JWT":        valid,
		"malformed JWT":   "Bearer not-a-jwt",
		"revoked session": "Bearer " + f.token(t, "u1", "s-signed-out"),
		"someone else's":  "Bearer " + f.token(t, "u2", "s1"),
		"tampered":        "Bearer " + valid[:len(valid)-2] + "xx",
	} {
		w := f.get(t, "/me", header)
		if w.Code != http.StatusUnauthorized || errorCode(t, w) != "unauthorized" {
			t.Errorf("%s: %d %s, want 401 unauthorized", name, w.Code, w.Body)
		}
		if got := w.Header().Get("WWW-Authenticate"); got != `Bearer realm="maki"` {
			t.Errorf("%s: WWW-Authenticate = %q", name, got)
		}
	}
}

func TestAuthenticateDoesNotSignOutOnLoaderFailure(t *testing.T) {
	f := newAuthFixture(t, fakeLoader{err: errors.New("dial tcp: connection refused")})

	w := f.get(t, "/me", "Bearer "+f.token(t, "u1", "s1"))
	if w.Code != http.StatusInternalServerError || errorCode(t, w) != "internal_error" {
		t.Errorf("%d %s, want 500 internal_error: a database hiccup mustn't look like a revoked session", w.Code, w.Body)
	}
	if !bytes.Contains(f.logs.Bytes(), []byte("connection refused")) {
		t.Errorf("the loader's error wasn't logged: %s", f.logs)
	}
}

func TestRequirePasswordChanged(t *testing.T) {
	mustChange := activeUser
	mustChange.SessionID, mustChange.MustChangePassword = "s2", true
	f := newAuthFixture(t, fakeLoader{principals: map[string]authn.Principal{"s1": activeUser, "s2": mustChange}})

	if w := f.get(t, "/orders", "Bearer "+f.token(t, "u1", "s1")); w.Code != http.StatusOK {
		t.Errorf("a user with a password of their own: %d %s, want 200", w.Code, w.Body)
	}

	must := "Bearer " + f.token(t, "u1", "s2")
	if w := f.get(t, "/orders", must); w.Code != http.StatusForbidden || errorCode(t, w) != "password_change_required" {
		t.Errorf("a user who must change their password: %d %s, want 403 password_change_required", w.Code, w.Body)
	}
	if w := f.get(t, "/me", must); w.Code != http.StatusOK {
		t.Errorf("/me stays open while the password must change: %d %s", w.Code, w.Body)
	}
}

func TestRequirePasswordChangedFailsClosedWithoutAuthenticate(t *testing.T) {
	f := newAuthFixture(t, fakeLoader{})
	if w := f.get(t, "/miswired", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("%d %s, want 401: a route wired without Authenticate must not be open", w.Code, w.Body)
	}
}
