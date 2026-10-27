package middlewares

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/utils/response"
)

// ErrorHandler writes the error a handler left with c.Error as the error
// envelope, once the handler returns: a coded error of utils/apperror with
// its status, code and message, anything else as a logged 500 that shows the
// client nothing of it. A response the handler already wrote is left as it
// is.
func ErrorHandler(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 || c.Writer.Written() {
			return
		}
		response.Fail(c, logger, c.Errors.Last().Err)
	}
}
