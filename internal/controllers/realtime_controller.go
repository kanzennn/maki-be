package controllers

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/realtime"
)

// How a connection stays honest. It ends after maxConnectionAge, so a client
// asks for a new ticket, and with that its session is checked again: one
// revoked since stops hearing. A ping finds a client that went away.
const (
	maxConnectionAge = 15 * time.Minute
	pingEvery        = 30 * time.Second
	writeTimeout     = 10 * time.Second
	// closeRenew tells the client to connect again, with a new ticket.
	closeRenew websocket.StatusCode = 4000
)

// RealtimeController serves an outlet's live messages.
type RealtimeController struct {
	hub    *realtime.Hub
	logger *slog.Logger
	// originPatterns are the hosts of CORS_ALLOWED_ORIGINS. A request from
	// the API's own host needs no listing.
	originPatterns []string
	// maxAge is maxConnectionAge; tests shorten it.
	maxAge time.Duration
}

// NewRealtimeController returns the live messages' controller.
func NewRealtimeController(hub *realtime.Hub, logger *slog.Logger, originPatterns []string) *RealtimeController {
	return &RealtimeController{hub: hub, logger: logger, originPatterns: originPatterns, maxAge: maxConnectionAge}
}

// WithMaxAge returns the controller with connections ending after d.
func (ctl *RealtimeController) WithMaxAge(d time.Duration) *RealtimeController {
	ctl.maxAge = d
	return ctl
}

// Ticket handles POST /outlets/{oid}/realtime/ticket.
func (ctl *RealtimeController) Ticket(c *gin.Context) {
	c.JSON(http.StatusCreated, models.RealtimeTicketResponse{
		Ticket:    ctl.hub.IssueTicket(c.Param("oid")),
		ExpiresIn: int(realtime.TicketTTL.Seconds()),
	})
}

// Connect handles GET /outlets/{oid}/realtime?ticket=, upgrading to a
// WebSocket that sends a message each time something of the outlet changes. A
// missing, used or expired ticket is 401.
func (ctl *RealtimeController) Connect(c *gin.Context) {
	outletID := c.Param("oid")
	if !ctl.hub.RedeemTicket(c.Query("ticket"), outletID) {
		_ = c.Error(apperror.ErrUnauthorized)
		return
	}
	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{OriginPatterns: ctl.originPatterns})
	if err != nil {
		// Accept has answered already.
		ctl.logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "realtime: not upgraded", slog.String("error", err.Error()))
		return
	}
	defer func() { _ = conn.CloseNow() }()

	sub := ctl.hub.Subscribe(outletID)
	defer sub.Close()

	// Clients send nothing; this also notices them leaving.
	ctx := conn.CloseRead(c.Request.Context())
	age := time.NewTimer(ctl.maxAge)
	defer age.Stop()
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-age.C:
			_ = conn.Close(closeRenew, "connect again")
			return
		case <-ping.C:
			if err := send(ctx, func(ctx context.Context) error { return conn.Ping(ctx) }); err != nil {
				return
			}
		case m, open := <-sub.C:
			if !open {
				_ = conn.Close(closeRenew, "connect again")
				return
			}
			if err := send(ctx, func(ctx context.Context) error { return wsjson.Write(ctx, conn, m) }); err != nil {
				return
			}
		}
	}
}

func send(ctx context.Context, write func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return write(ctx)
}
