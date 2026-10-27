// Package controllers holds the HTTP handlers: each binds and checks a
// request, calls a service, and writes the response. Errors go to the error
// middleware, which renders them as the error envelope.
package controllers

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/utils/requestid"
	"github.com/kervzent/maki-be/internal/utils/response"
)

// Pinger is the part of the database pool the readiness probe needs.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthController serves the liveness and readiness probes, /healthz and
// /readyz. It is infrastructure rather than a business resource, so it has no
// service or repository.
type HealthController struct {
	db     Pinger
	logger *slog.Logger
}

// NewHealthController returns the probes' controller.
func NewHealthController(db Pinger, logger *slog.Logger) *HealthController {
	return &HealthController{db: db, logger: logger}
}

type statusResponse struct {
	Status string `json:"status"`
}

// Live reports that the process is up. It never touches dependencies, so a
// slow database can't make the orchestrator restart a healthy process.
func (h *HealthController) Live(c *gin.Context) {
	c.JSON(http.StatusOK, statusResponse{Status: "ok"})
}

// Ready reports whether the service can do useful work: the database answers
// within two seconds.
func (h *HealthController) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	if err := h.db.Ping(ctx); err != nil {
		h.logger.LogAttrs(ctx, slog.LevelWarn, "readiness check failed",
			slog.String("request_id", requestid.RequestID(ctx)),
			slog.String("error", err.Error()),
		)
		response.WriteError(c, http.StatusServiceUnavailable, "not_ready")
		return
	}
	c.JSON(http.StatusOK, statusResponse{Status: "ready"})
}
