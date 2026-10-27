package middlewares

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
)

// fakeOutlets knows the outlets in exists; err, when set, is returned instead.
type fakeOutlets struct {
	exists map[string]bool
	err    error
}

func (f fakeOutlets) OutletExists(_ context.Context, outletID string) (bool, error) {
	return f.exists[outletID], f.err
}

// signedIn stands in for Authenticate: the request acts for p.
func signedIn(p models.Principal) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(models.WithPrincipal(c.Request.Context(), p))
		c.Next()
	}
}

func accessRouter(p *models.Principal, outlets OutletFinder) *gin.Engine {
	r := gin.New()
	ok := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
	g := r.Group("/")
	if p != nil {
		g.Use(signedIn(*p))
	}
	g.GET("/outlets", RequireOwner(), ok)
	scoped := g.Group("/outlets/:oid", OutletScope(outlets, slog.New(slog.DiscardHandler)))
	scoped.GET("/menus", RequirePermission("menu.view"), ok)
	scoped.POST("/menus", RequirePermission("menu.manage"), ok)
	scoped.GET("/tables", RequireAnyPermission("table.view", "menu.manage"), ok)
	scoped.POST("/devices", RequireManager(), ok)
	scoped.GET("/devices", RequireManagerOrOwner(), ok)
	return r
}

func serve(t *testing.T, r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), method, path, nil))
	return w
}

func TestAccessMatrix(t *testing.T) {
	owner := models.Principal{UserID: "owner", IsOwner: true, Permissions: models.NewPermissions("menu.view")}
	manager := models.Principal{UserID: "m1", IsManager: true, OutletID: "o1",
		Permissions: models.NewPermissions("menu.view", "menu.manage")}
	cashier := models.Principal{UserID: "k1", OutletID: "o1", Permissions: models.NewPermissions("menu.view")}
	outlets := fakeOutlets{exists: map[string]bool{"o1": true, "o2": true}}

	tests := []struct {
		name         string
		who          models.Principal
		method, path string
		status       int
		code         string
	}{
		{"owner lists outlets", owner, http.MethodGet, "/outlets", http.StatusOK, ""},
		{"manager can't", manager, http.MethodGet, "/outlets", http.StatusForbidden, "forbidden"},
		{"owner in any outlet", owner, http.MethodGet, "/outlets/o2/menus", http.StatusOK, ""},
		{"owner in an unknown outlet", owner, http.MethodGet, "/outlets/o9/menus", http.StatusNotFound, "outlet_not_found"},
		{"owner without the permission", owner, http.MethodPost, "/outlets/o1/menus", http.StatusForbidden, "forbidden"},
		{"manager in their outlet", manager, http.MethodPost, "/outlets/o1/menus", http.StatusOK, ""},
		{"manager in another outlet", manager, http.MethodGet, "/outlets/o2/menus", http.StatusNotFound, "outlet_not_found"},
		{"a made-up outlet looks the same", manager, http.MethodGet, "/outlets/o9/menus", http.StatusNotFound, "outlet_not_found"},
		{"cashier with the permission", cashier, http.MethodGet, "/outlets/o1/menus", http.StatusOK, ""},
		{"cashier without it", cashier, http.MethodPost, "/outlets/o1/menus", http.StatusForbidden, "forbidden"},
		{"one of two permissions is enough", manager, http.MethodGet, "/outlets/o1/tables", http.StatusOK, ""},
		{"none of them isn't", cashier, http.MethodGet, "/outlets/o1/tables", http.StatusForbidden, "forbidden"},
		{"manager activates a device", manager, http.MethodPost, "/outlets/o1/devices", http.StatusOK, ""},
		{"owner can't", owner, http.MethodPost, "/outlets/o1/devices", http.StatusForbidden, "forbidden"},
		{"nor can a cashier", cashier, http.MethodPost, "/outlets/o1/devices", http.StatusForbidden, "forbidden"},
		{"manager lists devices", manager, http.MethodGet, "/outlets/o1/devices", http.StatusOK, ""},
		{"and so does the owner", owner, http.MethodGet, "/outlets/o2/devices", http.StatusOK, ""},
		{"but not a cashier", cashier, http.MethodGet, "/outlets/o1/devices", http.StatusForbidden, "forbidden"},
		{"another outlet's manager", manager, http.MethodPost, "/outlets/o2/devices", http.StatusNotFound, "outlet_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := serve(t, accessRouter(&tt.who, outlets), tt.method, tt.path)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.status, w.Body)
			}
			if tt.code != "" && errorCode(t, w) != tt.code {
				t.Errorf("code = %q, want %q", errorCode(t, w), tt.code)
			}
		})
	}
}

func TestAccessFailsClosedWithoutAPrincipal(t *testing.T) {
	r := accessRouter(nil, fakeOutlets{exists: map[string]bool{"o1": true}})
	for _, path := range []string{"/outlets", "/outlets/o1/menus", "/outlets/o1/tables", "/outlets/o1/devices"} {
		if w := serve(t, r, http.MethodGet, path); w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without Authenticate = %d, want 401", path, w.Code)
		}
	}
}

func TestOutletScopeDoesNotHideAFailure(t *testing.T) {
	owner := models.Principal{UserID: "owner", IsOwner: true, Permissions: models.NewPermissions("menu.view")}
	w := serve(t, accessRouter(&owner, fakeOutlets{err: errors.New("database down")}), http.MethodGet, "/outlets/o1/menus")
	if w.Code != http.StatusInternalServerError || errorCode(t, w) != "internal_error" {
		t.Errorf("status = %d %s, want 500 internal_error rather than a 404", w.Code, w.Body)
	}
}
