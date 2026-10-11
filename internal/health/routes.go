package health

import "github.com/gin-gonic/gin"

// Routes registers the probes. They are public and live outside /api/v1.
func (h *Handler) Routes(r gin.IRouter) {
	r.GET("/healthz", h.Live)
	r.GET("/readyz", h.Ready)
}
