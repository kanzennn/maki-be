package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// Logger writes one structured line per request. It must sit outside
// Recovery so that requests which panic are logged too, with their 500.
//
// It logs the route pattern rather than the raw path, so IDs and other values
// in URLs stay out of the logs.
func Logger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		level := slog.LevelInfo
		if c.Writer.Status() >= 500 {
			level = slog.LevelError
		}
		attrs := []slog.Attr{
			slog.String("request_id", httpx.RequestID(c.Request.Context())),
			slog.String("method", c.Request.Method),
			slog.String("route", route),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("latency", time.Since(start)),
			slog.String("client_ip", c.ClientIP()),
		}
		// Authenticate, further down the chain, replaced c.Request with one
		// whose context carries the principal.
		if p, ok := authn.PrincipalFrom(c.Request.Context()); ok {
			attrs = append(attrs, slog.String("user_id", p.UserID))
		}
		logger.LogAttrs(c.Request.Context(), level, "http request", attrs...)
	}
}
