package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/httpx"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func TestRequestIDGeneratedAndEchoed(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, httpx.RequestID(c.Request.Context())) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	id := w.Header().Get(RequestIDHeader)
	if id == "" || id != w.Body.String() {
		t.Errorf("header ID %q and context ID %q must be the same non-empty value", id, w.Body.String())
	}
}

func TestRequestIDReusesOnlyWellFormedIncomingIDs(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	for incoming, reused := range map[string]bool{
		"abc-123_DEF":           true,
		"has spaces":            false,
		"new\nline":             false,
		strings.Repeat("a", 65): false,
		`"><script>`:            false,
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.Header.Set(RequestIDHeader, incoming)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if got := w.Header().Get(RequestIDHeader) == incoming; got != reused {
			t.Errorf("incoming %q reused = %v, want %v", incoming, got, reused)
		}
	}
}

// Logger sits outside Recovery, so a panicking request still gets its access
// log line — with the 500 — next to the recovered panic.
func TestPanicIsRecoveredAndStillLogged(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	r := gin.New()
	r.Use(RequestID(), Logger(logger), Recovery(logger))
	r.GET("/boom", func(*gin.Context) { panic("secret internal state") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/boom", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "secret internal state") {
		t.Errorf("panic value leaked to the client: %s", w.Body)
	}

	var accessLog map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err == nil && entry["msg"] == "http request" {
			accessLog = entry
		}
	}
	if accessLog == nil {
		t.Fatalf("no access log line for the panicking request; logs:\n%s", logs.String())
	}
	if accessLog["status"] != float64(500) || accessLog["route"] != "/boom" {
		t.Errorf("access log = %v, want status 500 on route /boom", accessLog)
	}
	if !strings.Contains(logs.String(), "panic recovered") {
		t.Errorf("the recovered panic was not logged")
	}
}

func TestBodyLimitRejectsLargeBodies(t *testing.T) {
	r := gin.New()
	r.Use(BodyLimit(16))
	r.POST("/", func(c *gin.Context) {
		var body map[string]string
		if err := c.ShouldBindJSON(&body); err != nil {
			httpx.Fail(c, slog.New(slog.DiscardHandler), err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{"name":"a very long value"}`)))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
}

func TestCORSAllowsOnlyConfiguredOrigins(t *testing.T) {
	r := gin.New()
	r.Use(CORS([]string{"http://localhost:3002"}))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	preflight := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/", nil)
	preflight.Header.Set("Origin", "http://localhost:3002")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, preflight)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3002" {
		t.Errorf("allowed origin: Access-Control-Allow-Origin = %q", got)
	}

	other := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	other.Header.Set("Origin", "https://evil.example.com")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, other)
	if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("unknown origin: status = %d, allow-origin = %q; want 403 and no header",
			w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
}

// The web app behind the same host sends Origin on every POST; it must get
// through without being in CORS_ALLOWED_ORIGINS.
func TestCORSLetsSameOriginRequestsThrough(t *testing.T) {
	r := gin.New()
	r.Use(CORS([]string{"http://localhost:3002"}))
	r.POST("/api/v1/auth/login", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, tc := range []struct {
		host, origin string
		want         int
	}{
		{"localhost:4173", "http://localhost:4173", http.StatusOK},    // vite preview's proxy
		{"admin.maki.test", "https://admin.maki.test", http.StatusOK}, // Caddy, TLS ends there
		{"ADMIN.maki.test", "https://admin.MAKI.test", http.StatusOK}, // host names ignore case
		{"admin.maki.test", "https://evil.test", http.StatusForbidden},
		{"admin.maki.test", "https://admin.maki.test.evil.test", http.StatusForbidden},
		{"admin.maki.test", "null", http.StatusForbidden},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/login", nil)
		req.Host = tc.host
		req.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("Host %s, Origin %s: status %d, want %d", tc.host, tc.origin, w.Code, tc.want)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	r := gin.New()
	r.Use(SecurityHeaders())
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("headers = %v", w.Header())
	}
}
