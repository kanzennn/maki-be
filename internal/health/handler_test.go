package health

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func serve(t *testing.T, db Pinger, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	NewHandler(db, slog.New(slog.DiscardHandler)).Routes(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return w
}

func TestLiveIgnoresTheDatabase(t *testing.T) {
	w := serve(t, fakePinger{err: errors.New("connection refused")}, "/healthz")

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 even when the database is down", w.Code)
	}
}

func TestReady(t *testing.T) {
	if w := serve(t, fakePinger{}, "/readyz"); w.Code != http.StatusOK {
		t.Errorf("database up: status = %d, want 200", w.Code)
	}

	w := serve(t, fakePinger{err: errors.New("connection refused")}, "/readyz")
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("database down: status = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"code":"not_ready"`) {
		t.Errorf("database down: body = %s, want the not_ready envelope", w.Body)
	}
	if strings.Contains(w.Body.String(), "connection refused") {
		t.Errorf("the database error leaked to the client: %s", w.Body)
	}
}
