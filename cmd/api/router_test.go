package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/auth"
	"github.com/kervzent/maki-be/internal/device"
	"github.com/kervzent/maki-be/internal/menu"
	"github.com/kervzent/maki-be/internal/order"
	"github.com/kervzent/maki-be/internal/outlet"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/config"
	"github.com/kervzent/maki-be/internal/user"
	"github.com/kervzent/maki-be/internal/voucher"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

type okPinger struct{}

func (okPinger) Ping(context.Context) error { return nil }

// noSessions knows no session, so every authenticated route answers 401.
type noSessions struct{}

func (noSessions) LoadPrincipal(context.Context, authn.Session) (authn.Principal, error) {
	return authn.Principal{}, authn.ErrNoSession
}

// testDeps wires the router without a database: the auth endpoints exist but
// only the middleware in front of them runs in these tests.
func testDeps(t *testing.T) routerDeps {
	t.Helper()
	tokens, err := authn.NewTokens([]byte("0123456789abcdef0123456789abcdef"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return routerDeps{
		pinger:     okPinger{},
		tokens:     tokens,
		principals: noSessions{},
		auth:       auth.NewHandler(nil, tokens, slog.New(slog.DiscardHandler), true),
		outlets:    outlet.NewHandler(nil, slog.New(slog.DiscardHandler)),
		users:      user.NewHandler(nil, slog.New(slog.DiscardHandler)),
		menus:      menu.NewHandler(nil, slog.New(slog.DiscardHandler)),
		devices:    device.NewHandler(nil, slog.New(slog.DiscardHandler), true),
		vouchers:   voucher.NewHandler(nil, slog.New(slog.DiscardHandler)),
		orders:     order.NewHandler(nil, slog.New(slog.DiscardHandler), true),
	}
}

func newTestRouter(t *testing.T, cfg config.Config) *gin.Engine {
	t.Helper()
	r, err := newRouter(cfg, slog.New(slog.DiscardHandler), testDeps(t))
	if err != nil {
		t.Fatalf("newRouter() error = %v", err)
	}
	return r
}

func TestAuthenticatedRoutesNeedAToken(t *testing.T) {
	r := newTestRouter(t, config.Config{})
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/me"},
		{http.MethodPatch, "/api/v1/me"},
		{http.MethodPut, "/api/v1/me/password"},
		{http.MethodPut, "/api/v1/me/email"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), route.method, route.path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a token: %d, want 401", route.method, route.path, w.Code)
		}
	}
}

func TestUnknownRoutesUseTheErrorEnvelope(t *testing.T) {
	r := newTestRouter(t, config.Config{})

	for _, tc := range []struct {
		method, path string
		status       int
		code         string
	}{
		{http.MethodGet, "/api/v1/nope", http.StatusNotFound, "not_found"},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed, "method_not_allowed"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, nil))

		var body struct {
			Error struct {
				Code      string `json:"code"`
				RequestID string `json:"request_id"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s %s: body is not the error envelope: %s", tc.method, tc.path, w.Body)
		}
		if w.Code != tc.status || body.Error.Code != tc.code {
			t.Errorf("%s %s: got %d %q, want %d %q", tc.method, tc.path, w.Code, body.Error.Code, tc.status, tc.code)
		}
		if body.Error.RequestID == "" || body.Error.RequestID != w.Header().Get("X-Request-ID") {
			t.Errorf("%s %s: the envelope must carry the same request ID as the header", tc.method, tc.path)
		}
	}
}

func TestEveryResponseCarriesSecurityHeaders(t *testing.T) {
	r := newTestRouter(t, config.Config{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", w.Code)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("headers = %v", w.Header())
	}
}

func TestForwardedForIsIgnoredWithoutTrustedProxies(t *testing.T) {
	r := newTestRouter(t, config.Config{})
	r.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ip", nil)
	req.RemoteAddr = "203.0.113.7:51000"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Body.String() != "203.0.113.7" {
		t.Errorf("ClientIP = %q, want the socket address 203.0.113.7", w.Body.String())
	}
}

func TestInvalidTrustedProxiesFailStartup(t *testing.T) {
	_, err := newRouter(config.Config{TrustedProxies: []string{"not-an-ip"}}, slog.New(slog.DiscardHandler), testDeps(t))
	if err == nil {
		t.Error("newRouter() accepted an invalid TRUSTED_PROXIES entry")
	}
}
