package middlewares

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/utils/requestid"
	"github.com/kervzent/maki-be/internal/utils/response"
)

// Recovery turns a panic into the generic 500 envelope. The stack is logged
// server-side with the request ID and never sent to the client.
func Recovery(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// net/http uses this panic to abort a response on purpose.
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}
			logger.LogAttrs(c.Request.Context(), slog.LevelError, "panic recovered",
				slog.String("request_id", requestid.RequestID(c.Request.Context())),
				slog.Any("panic", rec),
				slog.String("stack", string(debug.Stack())),
			)
			if c.Writer.Written() {
				c.Abort()
				return
			}
			response.WriteError(c, http.StatusInternalServerError, "internal_error")
		}()
		c.Next()
	}
}
