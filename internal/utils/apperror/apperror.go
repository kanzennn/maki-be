// Package apperror holds the API's errors: the coded error type, which
// carries its HTTP status and the stable code clients switch on, and the
// catalog of every error the services and the utilities return (errors.go).
// The error middleware renders them as the error envelope.
package apperror

import (
	"maps"
	"math"
	"net/http"
	"time"
)

// CodedError is implemented by errors that map to a specific API response.
// The catalog declares them with NewError, or a type of its own that
// implements this interface, so the error middleware can render them.
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
// the envelope sends it as the Retry-After header, in whole seconds, and as
// details.retry_after.
func (e *Error) WithRetryAfter(d time.Duration) *Error {
	cp := *e
	cp.retryAfter = d
	cp.details = maps.Clone(e.details)
	if cp.details == nil {
		cp.details = make(map[string]any, 1)
	}
	cp.details["retry_after"] = Seconds(d)
	return &cp
}

// RetryAfter is how long the client should wait before trying again; zero
// when it needn't.
func (e *Error) RetryAfter() time.Duration { return e.retryAfter }

// Seconds rounds d up to whole seconds, never below one: a client told to
// wait 0 seconds would retry at once.
func Seconds(d time.Duration) int {
	return max(1, int(math.Ceil(d.Seconds())))
}

// Error returns the code, which is what logs show.
func (e *Error) Error() string { return e.code }
func (e *Error) Status() int   { return e.status }
func (e *Error) Code() string  { return e.code }

// Details returns the machine-readable details, if any.
func (e *Error) Details() map[string]any { return e.details }

// Errors for a request body that can't be used.
var (
	ErrInvalidRequest   = NewError(http.StatusBadRequest, "invalid_request")
	ErrValidationFailed = NewError(http.StatusUnprocessableEntity, "validation_failed")
)

// Invalid returns validation_failed for fields, which maps JSON field names
// to the rule each broke: {"fields": {"username": "required"}}.
func Invalid(fields map[string]string) error {
	return ErrValidationFailed.WithDetails(map[string]any{"fields": fields})
}
