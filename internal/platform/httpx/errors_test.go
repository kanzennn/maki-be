package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/i18n"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func newContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequestWithContext(WithRequestID(t.Context(), "req-1"), http.MethodGet, "/", nil)
	return c, w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not the error envelope: %v\n%s", err, w.Body)
	}
	return env.Error
}

func TestFailRendersCodedError(t *testing.T) {
	c, w := newContext(t)
	errNotReady := NewError(http.StatusServiceUnavailable, "not_ready")

	Fail(c, slog.New(slog.DiscardHandler), fmt.Errorf("check database: %w", errNotReady.WithDetails(map[string]any{"retry_after": 5})))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	body := decode(t, w)
	if body.Code != "not_ready" || body.Message != i18n.For("id").Error("not_ready", nil) || body.RequestID != "req-1" {
		t.Errorf("body = %+v", body)
	}
	if body.Details["retry_after"] != float64(5) {
		t.Errorf("details = %v, want retry_after=5", body.Details)
	}
	if got := errNotReady.Error(); got != "not_ready" {
		t.Errorf("Error() = %q, want the code, for logs", got)
	}
}

func TestMessageFollowsAcceptLanguage(t *testing.T) {
	for _, tt := range []struct {
		header string
		lang   string
	}{
		{"", "id"},
		{"en-US,en;q=0.9", "en"},
		{"id-ID,id;q=0.9,en;q=0.8", "id"},
		{"fr-FR", "id"},
	} {
		c, w := newContext(t)
		if tt.header != "" {
			c.Request.Header.Set("Accept-Language", tt.header)
		}

		WriteError(c, http.StatusNotFound, "not_found")

		body := decode(t, w)
		if want := i18n.For(tt.lang).Error("not_found", nil); body.Message != want {
			t.Errorf("Accept-Language %q: message = %q, want %q", tt.header, body.Message, want)
		}
		if got := w.Header().Get("Content-Language"); got != tt.lang {
			t.Errorf("Accept-Language %q: Content-Language = %q, want %q", tt.header, got, tt.lang)
		}
	}
	if i18n.For("id").Error("not_found", nil) == i18n.For("en").Error("not_found", nil) {
		t.Fatal("the id and en messages are identical, so this test proves nothing")
	}
}

func TestFailHidesUnknownErrors(t *testing.T) {
	c, w := newContext(t)
	var logs bytes.Buffer

	Fail(c, slog.New(slog.NewJSONHandler(&logs, nil)), errors.New("password authentication failed for user maki"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if body := decode(t, w); body.Code != "internal_error" {
		t.Errorf("code = %q, want internal_error", body.Code)
	}
	if strings.Contains(w.Body.String(), "password") {
		t.Errorf("internal error text leaked to the client: %s", w.Body)
	}
	if !strings.Contains(logs.String(), "password authentication failed") || !strings.Contains(logs.String(), "req-1") {
		t.Errorf("the internal error must be logged with the request ID, got: %s", logs.String())
	}
}

func TestFailMapsOversizedBody(t *testing.T) {
	c, w := newContext(t)

	Fail(c, slog.New(slog.DiscardHandler), fmt.Errorf("bind: %w", &http.MaxBytesError{Limit: 1 << 20}))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
	if body := decode(t, w); body.Code != "payload_too_large" {
		t.Errorf("code = %q, want payload_too_large", body.Code)
	}
}

func TestFailSendsRetryAfter(t *testing.T) {
	c, w := newContext(t)
	limited := NewError(http.StatusServiceUnavailable, "not_ready").WithDetails(map[string]any{"kept": true})

	Fail(c, slog.New(slog.DiscardHandler), limited.WithRetryAfter(89500*time.Millisecond))

	if got := w.Header().Get("Retry-After"); got != "90" {
		t.Errorf("Retry-After = %q, want 90 (rounded up)", got)
	}
	body := decode(t, w)
	if body.Details["retry_after"] != float64(90) || body.Details["kept"] != true {
		t.Errorf("details = %v, want retry_after=90 and the earlier details", body.Details)
	}
	if limited.Details()["retry_after"] != nil {
		t.Error("WithRetryAfter changed the error it was called on")
	}
	if got := NewError(http.StatusServiceUnavailable, "not_ready").WithRetryAfter(time.Millisecond).Details()["retry_after"]; got != 1 {
		t.Errorf("a sub-second wait gives retry_after = %v, want 1", got)
	}
}
