package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
)

// registerHealthRoutes registers the probes. They are public and live outside
// /api/v1.
func registerHealthRoutes(r gin.IRouter, h *controllers.HealthController) {
	r.GET("/healthz", h.Live)
	r.GET("/readyz", h.Ready)
}
