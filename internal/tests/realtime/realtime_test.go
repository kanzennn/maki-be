package realtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/middlewares"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/routes"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/realtime"
)

// These tests connect real WebSockets to a real server, with the routes and
// middleware the API has, and stand-ins for the writes they follow. No database:
// a change is the handler's answer.

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

type anyOutlet struct{}

func (anyOutlet) OutletExists(context.Context, string) (bool, error) { return true, nil }

type harness struct {
	t   *testing.T
	srv *httptest.Server
	ctl *controllers.RealtimeController
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	hub := realtime.NewHub(time.Now)
	ctl := controllers.NewRealtimeController(hub, logger, []string{"app.maki.test"})

	r := gin.New()
	r.Use(middlewares.RequestID(), middlewares.Recovery(logger), middlewares.ErrorHandler(logger))
	public := r.Group("/api/v1")
	protected := public.Group("", func(c *gin.Context) {
		if who := c.GetHeader("X-Test-Outlet"); who != "" {
			p := models.Principal{UserID: "u1", OutletID: who}
			c.Request = c.Request.WithContext(models.WithPrincipal(c.Request.Context(), p))
		}
		c.Next()
	})
	scoped := protected.Group("/outlets/:oid", middlewares.OutletScope(anyOutlet{}, logger), middlewares.PublishChanges(hub, routes.ChangeTopics))
	routes.RegisterRealtimeRoutes(public, scoped, ctl)
	answer := func(status int) gin.HandlerFunc {
		return func(c *gin.Context) { c.Status(status); c.Writer.WriteHeaderNow() }
	}
	scoped.POST("/orders", answer(http.StatusCreated))
	scoped.PUT("/menus/:mid", answer(http.StatusOK))
	scoped.POST("/tables", answer(http.StatusConflict))
	scoped.POST("/devices", answer(http.StatusCreated))
	scoped.POST("/orders/:orid/fail", func(c *gin.Context) { _ = c.Error(apperror.ErrOrderNotFound) })

	srv := httptest.NewUnstartedServer(r)
	// As the API's server has it: a connection outlives its write timeout.
	srv.Config.WriteTimeout = 200 * time.Millisecond
	srv.Config.ReadTimeout = 200 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	return &harness{t: t, srv: srv, ctl: ctl}
}

func (h *harness) post(method, path, outlet string) int {
	h.t.Helper()
	req, err := http.NewRequestWithContext(h.t.Context(), method, h.srv.URL+path, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("X-Test-Outlet", outlet)
	res, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	return res.StatusCode
}

func (h *harness) ticket(outlet string) string {
	h.t.Helper()
	req, err := http.NewRequestWithContext(h.t.Context(), http.MethodPost, h.srv.URL+"/api/v1/outlets/"+outlet+"/realtime/ticket", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("X-Test-Outlet", outlet)
	res, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusCreated {
		h.t.Fatalf("ticket: status %d", res.StatusCode)
	}
	var out models.RealtimeTicketResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		h.t.Fatal(err)
	}
	if out.ExpiresIn != 30 || out.Ticket == "" {
		h.t.Fatalf("ticket = %+v", out)
	}
	return out.Ticket
}

func (h *harness) wsURL(outlet, ticket string) string {
	return "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/api/v1/outlets/" + outlet + "/realtime?ticket=" + ticket
}

func (h *harness) dial(outlet string) *websocket.Conn {
	h.t.Helper()
	conn, res, err := websocket.Dial(h.t.Context(), h.wsURL(outlet, h.ticket(outlet)), nil)
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = conn.CloseNow() })
	// The subscription starts just after the upgrade answers; a request
	// made at once could come first.
	time.Sleep(50 * time.Millisecond)
	return conn
}

func (h *harness) next(conn *websocket.Conn) realtime.Message {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(h.t.Context(), 3*time.Second)
	defer cancel()
	var m realtime.Message
	if err := wsjson.Read(ctx, conn, &m); err != nil {
		h.t.Fatalf("read: %v", err)
	}
	return m
}

func TestAClientHearsWhatChanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	conn := h.dial("o1")
	other := h.dial("o2")

	// Longer than the server's timeouts: the connection stays.
	time.Sleep(400 * time.Millisecond)
	if got := h.post(http.MethodPost, "/api/v1/outlets/o1/orders", "o1"); got != http.StatusCreated {
		t.Fatalf("sale: %d", got)
	}
	if got := strings.Join(h.next(conn).Topics, ","); got != "catalog,tables,orders" {
		t.Errorf("after a sale = %s", got)
	}

	// A refused write and one nobody follows say nothing; the next write comes
	// through right after them.
	h.post(http.MethodPost, "/api/v1/outlets/o1/tables", "o1")
	h.post(http.MethodPost, "/api/v1/outlets/o1/orders/x/fail", "o1")
	h.post(http.MethodPost, "/api/v1/outlets/o1/devices", "o1")
	h.post(http.MethodPut, "/api/v1/outlets/o1/menus/m1", "o1")
	if got := strings.Join(h.next(conn).Topics, ","); got != "catalog" {
		t.Errorf("after a menu's change = %s", got)
	}

	// Another outlet's client heard none of it.
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	var m realtime.Message
	if err := wsjson.Read(ctx, other, &m); err == nil {
		t.Errorf("o2 heard %+v", m)
	}
}

func TestATicketLetsOneConnectionIn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	refused := func(url string, want int) {
		t.Helper()
		conn, res, err := websocket.Dial(t.Context(), url, nil)
		if res != nil && res.Body != nil {
			defer func() { _ = res.Body.Close() }()
		}
		if err == nil {
			_ = conn.CloseNow()
			t.Fatalf("%s connected", url)
		}
		if res == nil || res.StatusCode != want {
			t.Fatalf("%s: %+v, want %d", url, res, want)
		}
	}
	refused(h.wsURL("o1", ""), http.StatusUnauthorized)
	refused(h.wsURL("o1", "made-up"), http.StatusUnauthorized)

	// Another outlet's ticket opens nothing here.
	refused(h.wsURL("o1", h.ticket("o2")), http.StatusUnauthorized)

	ticket := h.ticket("o1")
	conn, res, err := websocket.Dial(t.Context(), h.wsURL("o1", ticket), nil)
	if res != nil && res.Body != nil {
		defer func() { _ = res.Body.Close() }()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	refused(h.wsURL("o1", ticket), http.StatusUnauthorized)
}

func TestAnotherSiteCantConnect(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dial := func(origin string) (*websocket.Conn, error) {
		conn, res, err := websocket.Dial(t.Context(), h.wsURL("o1", h.ticket("o1")), &websocket.DialOptions{
			HTTPHeader: http.Header{"Origin": {origin}},
		})
		if res != nil && res.Body != nil {
			_ = res.Body.Close()
		}
		return conn, err
	}
	if conn, err := dial("https://evil.test"); err == nil {
		_ = conn.CloseNow()
		t.Error("another site connected")
	}
	conn, err := dial("https://app.maki.test")
	if err != nil {
		t.Fatalf("an allowed origin: %v", err)
	}
	_ = conn.CloseNow()
}

func TestAConnectionEndsAndAsksToRenew(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.ctl.WithMaxAge(100 * time.Millisecond)
	conn := h.dial("o1")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var m realtime.Message
	err := wsjson.Read(ctx, conn, &m)
	var closed websocket.CloseError
	if !errors.As(err, &closed) || closed.Code != 4000 {
		t.Errorf("read = %v, want close 4000", err)
	}
}

func TestChangeTopics(t *testing.T) {
	t.Parallel()
	for route, want := range map[string]string{
		"/api/v1/outlets/:oid":                        "catalog",
		"/api/v1/outlets/:oid/status":                 "catalog",
		"/api/v1/outlets/:oid/menus/:mid/stock-alert": "catalog",
		"/api/v1/outlets/:oid/categories/order":       "catalog",
		"/api/v1/outlets/:oid/payment-methods":        "catalog",
		"/api/v1/outlets/:oid/orders/:orid/void":      "catalog,tables,orders",
		"/api/v1/outlets/:oid/tables/:tid/release":    "tables",
		"/api/v1/outlets/:oid/areas/:aid/layout":      "tables",
		"/api/v1/outlets/:oid/table-schedules":        "tables",
		"/api/v1/outlets/:oid/dine-in-mode":           "tables,catalog",
		"/api/v1/outlets/:oid/users":                  "",
		"/api/v1/outlets/:oid/realtime/ticket":        "",
		"/api/v1/outlets":                             "",
		"/api/v1/vouchers":                            "",
	} {
		if got := strings.Join(routes.ChangeTopics(route), ","); got != want {
			t.Errorf("%s = %q, want %q", route, got, want)
		}
	}
}
