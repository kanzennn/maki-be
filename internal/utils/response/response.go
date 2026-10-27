// Package response writes the API's responses: the error envelope, which
// carries the coded errors of utils/apperror in the request's language, and
// the list shape of list endpoints.
package response

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/i18n"
	"github.com/kervzent/maki-be/internal/utils/requestid"
)

type envelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
}

// Fail writes err as the error envelope and stops the handler chain. Errors
// that don't implement CodedError are logged with the request ID and rendered
// as a generic 500, so internal details never reach the client.
func Fail(c *gin.Context, logger *slog.Logger, err error) {
	if coded, ok := errors.AsType[apperror.CodedError](err); ok {
		var details map[string]any
		if d, ok := coded.(interface{ Details() map[string]any }); ok {
			details = d.Details()
		}
		if r, ok := coded.(interface{ RetryAfter() time.Duration }); ok && r.RetryAfter() > 0 {
			c.Header("Retry-After", strconv.Itoa(apperror.Seconds(r.RetryAfter())))
		}
		write(c, coded.Status(), coded.Code(), details)
		return
	}

	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		WriteError(c, http.StatusRequestEntityTooLarge, "payload_too_large")
		return
	}

	logger.LogAttrs(c.Request.Context(), slog.LevelError, "unhandled error",
		slog.String("request_id", requestid.RequestID(c.Request.Context())),
		slog.String("error", err.Error()),
	)
	WriteError(c, http.StatusInternalServerError, "internal_error")
}

// WriteError writes the error envelope for code and stops the handler chain.
// Like apperror.NewError, code needs an "error.<code>" message in the i18n
// catalogs.
func WriteError(c *gin.Context, status int, code string) {
	write(c, status, code, nil)
}

func write(c *gin.Context, status int, code string, details map[string]any) {
	loc := i18n.For(c.GetHeader("Accept-Language"))
	// Tells the client which language the message is in.
	c.Header("Content-Language", loc.Language())
	c.AbortWithStatusJSON(status, envelope{Error: errorBody{
		Code:      code,
		Message:   loc.Error(code, details),
		Details:   details,
		RequestID: requestid.RequestID(c.Request.Context()),
	}})
}
