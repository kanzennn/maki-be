package middleware

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// Authenticate requires a valid access token — "Authorization: Bearer
// <token>" — and puts the principal it names on the request's context, where
// handlers read it with authn.PrincipalFrom.
//
// A missing, invalid or expired token, or a session that no longer exists,
// is 401 unauthorized: the client refreshes its token once and signs the user
// out if that fails too. Failing to load the principal for any other reason,
// such as the database being down, is a 500, so a hiccup never signs anyone
// out.
func Authenticate(tokens *authn.Tokens, loader authn.PrincipalLoader, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := authn.BearerToken(c.GetHeader("Authorization"))
		if !ok {
			unauthorized(c)
			return
		}
		session, err := tokens.Verify(token)
		if err != nil {
			unauthorized(c)
			return
		}
		p, err := loader.LoadPrincipal(c.Request.Context(), session)
		if errors.Is(err, authn.ErrNoSession) {
			unauthorized(c)
			return
		}
		if err != nil {
			httpx.Fail(c, logger, fmt.Errorf("load principal: %w", err))
			return
		}
		c.Request = c.Request.WithContext(authn.WithPrincipal(c.Request.Context(), p))
		c.Next()
	}
}

// RequirePasswordChanged refuses — 403 password_change_required — a user who
// must first replace a password someone else set. It goes after Authenticate
// on every authenticated route except the few that stay open until then:
// reading /me and changing the password. Without a principal it answers 401,
// so a route wired without Authenticate fails closed.
func RequirePasswordChanged() gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := authn.PrincipalFrom(c.Request.Context())
		switch {
		case !ok:
			unauthorized(c)
		case p.MustChangePassword:
			httpx.WriteError(c, http.StatusForbidden, "password_change_required")
		default:
			c.Next()
		}
	}
}

func unauthorized(c *gin.Context) {
	// RFC 6750: a 401 names the scheme the client should use.
	c.Header("WWW-Authenticate", `Bearer realm="maki"`)
	httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
}
