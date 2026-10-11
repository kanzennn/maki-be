// Package health serves the liveness and readiness probes. It is
// infrastructure rather than a business module, so it has a handler but no
// service or repository.
package health

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// Pinger is the part of the database pool the readiness probe needs.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Handler serves /healthz and /readyz.
type Handler struct {
	db     Pinger
	logger *slog.Logger
}

// NewHandler returns the probe handler.
func NewHandler(db Pinger, logger *slog.Logger) *Handler {
	return &Handler{db: db, logger: logger}
}

type statusResponse struct {
	Status string `json:"status"`
}

// Live reports that the process is up. It never touches dependencies, so a
// slow database can't make the orchestrator restart a healthy process.
func (h *Handler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, statusResponse{Status: "ok"})
}

// Ready reports whether the service can do useful work: the database answers
// within two seconds.
func (h *Handler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	if err := h.db.Ping(ctx); err != nil {
		h.logger.LogAttrs(ctx, slog.LevelWarn, "readiness check failed",
			slog.String("request_id", httpx.RequestID(ctx)),
			slog.String("error", err.Error()),
		)
		httpx.WriteError(c, http.StatusServiceUnavailable, "not_ready")
		return
	}
	c.JSON(http.StatusOK, statusResponse{Status: "ready"})
}
