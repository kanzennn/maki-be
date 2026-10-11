package middleware

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// SecurityHeaders sets the headers every API response carries. API responses
// hold per-user data, so nothing is cacheable by default.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

// BodyLimit caps request bodies at limit bytes. Reading past the limit fails
// with *http.MaxBytesError, which httpx.Fail renders as 413. File uploads
// never pass through the API, so the limit can stay small.
func BodyLimit(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

// CORS allows exactly the configured origins, with credentials. With no
// origins configured it does nothing: same-origin requests don't need CORS,
// and browsers block every cross-origin one by default.
func CORS(allowedOrigins []string) gin.HandlerFunc {
	if len(allowedOrigins) == 0 {
		return func(c *gin.Context) { c.Next() }
	}
	handle := cors.New(cors.Config{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete},
		AllowHeaders:     []string{"Authorization", "Content-Type", "Accept-Language", "Idempotency-Key", RequestIDHeader},
		ExposeHeaders:    []string{RequestIDHeader},
		AllowCredentials: true,
		MaxAge:           10 * time.Minute,
	})
	return func(c *gin.Context) {
		// Browsers send Origin on same-origin requests too — on every POST —
		// and gin-contrib/cors refuses any origin it wasn't given, so the web
		// app served from the API's own host (Caddy in production, the Vite
		// proxy in development) would be locked out. Same-origin needs no
		// CORS; only other origins go through the allowlist.
		if sameOrigin(c.GetHeader("Origin"), c.Request.Host) {
			c.Next()
			return
		}
		handle(c)
	}
}

// sameOrigin reports whether origin names the host the request was sent to.
// The reverse proxy passes the browser's Host through, so the two match for
// the app it serves itself.
func sameOrigin(origin, host string) bool {
	if origin == "" || host == "" {
		return false
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, host)
}
