// Package httpx holds the HTTP conventions every module shares: the error
// envelope, how errors map onto it, and the request ID on the context.
package httpx

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/i18n"
)

// CodedError is implemented by errors that map to a specific API response.
// Modules declare their domain errors with NewError, or with their own type
// that implements this interface, so platform code can render them without
// importing the module.
type CodedError interface {
	error
	// Status is the HTTP status the error is sent with.
	Status() int
	// Code is the stable code clients switch on. The message sent with it
	// is the "error.<code>" entry of the i18n catalogs, in the language of
	// the request's Accept-Language header.
	Code() string
}

// Error is a ready-made CodedError.
type Error struct {
	status     int
	code       string
	details    map[string]any
	retryAfter time.Duration
}

var _ CodedError = (*Error)(nil)

// NewError declares an error with its HTTP status and stable code, which
// needs an "error.<code>" message in every i18n catalog.
// TestEveryErrorCodeHasAMessage fails until it has one.
func NewError(status int, code string) *Error {
	return &Error{status: status, code: code}
}

// WithDetails returns a copy of e that carries machine-readable details, such
// as the remaining stock. They also fill the message's template fields.
func (e *Error) WithDetails(details map[string]any) *Error {
	cp := *e
	cp.details = details
	return &cp
}

// WithRetryAfter returns a copy of e that tells the client when to try again:
// Fail sends it as the Retry-After header, in whole seconds, and as
// details.retry_after.
func (e *Error) WithRetryAfter(d time.Duration) *Error {
	cp := *e
	cp.retryAfter = d
	cp.details = maps.Clone(e.details)
	if cp.details == nil {
		cp.details = make(map[string]any, 1)
	}
	cp.details["retry_after"] = seconds(d)
	return &cp
}

// RetryAfter is how long the client should wait before trying again; zero
// when it needn't.
func (e *Error) RetryAfter() time.Duration { return e.retryAfter }

// seconds rounds d up to whole seconds, never below one: a client told to
// wait 0 seconds would retry at once.
func seconds(d time.Duration) int {
	return max(1, int(math.Ceil(d.Seconds())))
}

// Error returns the code, which is what logs show.
func (e *Error) Error() string { return e.code }
func (e *Error) Status() int   { return e.status }
func (e *Error) Code() string  { return e.code }

// Details returns the machine-readable details, if any.
func (e *Error) Details() map[string]any { return e.details }

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
	if coded, ok := errors.AsType[CodedError](err); ok {
		var details map[string]any
		if d, ok := coded.(interface{ Details() map[string]any }); ok {
			details = d.Details()
		}
		if r, ok := coded.(interface{ RetryAfter() time.Duration }); ok && r.RetryAfter() > 0 {
			c.Header("Retry-After", strconv.Itoa(seconds(r.RetryAfter())))
		}
		write(c, coded.Status(), coded.Code(), details)
		return
	}

	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		WriteError(c, http.StatusRequestEntityTooLarge, "payload_too_large")
		return
	}

	logger.LogAttrs(c.Request.Context(), slog.LevelError, "unhandled error",
		slog.String("request_id", RequestID(c.Request.Context())),
		slog.String("error", err.Error()),
	)
	WriteError(c, http.StatusInternalServerError, "internal_error")
}

// WriteError writes the error envelope for code and stops the handler chain.
// Like NewError, code needs an "error.<code>" message in the i18n catalogs.
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
		RequestID: RequestID(c.Request.Context()),
	}})
}

type requestIDKey struct{}

// WithRequestID returns a context carrying the request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request ID carried by ctx, or "" if there is none.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
