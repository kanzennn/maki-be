// Package middlewares holds the Gin middleware chain every route runs through,
// in the order the architecture standard (§4) fixes:
// RequestID → Logger → Recovery → ErrorHandler → SecurityHeaders → BodyLimit →
// CORS, then the auth, scope and permission middleware on authenticated route
// groups.
package middlewares

import (
	"crypto/rand"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/utils/requestid"
)

// RequestIDHeader carries the request ID in both directions.
const RequestIDHeader = "X-Request-ID"

// RequestID reuses a well-formed incoming X-Request-ID or generates a new one,
// and puts it on the request context and the response.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if !validRequestID(id) {
			id = rand.Text()
		}
		c.Request = c.Request.WithContext(requestid.WithRequestID(c.Request.Context(), id))
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

// validRequestID keeps client-supplied IDs short and free of characters that
// could break log lines.
func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !isRequestIDChar(r) {
			return false
		}
	}
	return true
}

func isRequestIDChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}
