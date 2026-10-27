package middlewares

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/jwt"
	"github.com/kervzent/maki-be/internal/utils/response"
)

// PrincipalLoader loads the principal of a verified session; the auth
// service implements it. It returns models.ErrNoSession for a session that
// can't be used, and any other error for a failure to find out, such as the
// database being down.
type PrincipalLoader interface {
	LoadPrincipal(ctx context.Context, s jwt.Session) (models.Principal, error)
}

// Authenticate requires a valid access token — "Authorization: Bearer
// <token>" — and puts the principal it names on the request's context, where
// handlers read it with models.PrincipalFrom.
//
// A missing, invalid or expired token, or a session that no longer exists,
// is 401 unauthorized: the client refreshes its token once and signs the user
// out if that fails too. Failing to load the principal for any other reason,
// such as the database being down, is a 500, so a hiccup never signs anyone
// out.
func Authenticate(tokens *jwt.Tokens, loader PrincipalLoader, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := jwt.BearerToken(c.GetHeader("Authorization"))
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
		if errors.Is(err, models.ErrNoSession) {
			unauthorized(c)
			return
		}
		if err != nil {
			response.Fail(c, logger, fmt.Errorf("load principal: %w", err))
			return
		}
		c.Request = c.Request.WithContext(models.WithPrincipal(c.Request.Context(), p))
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
		p, ok := models.PrincipalFrom(c.Request.Context())
		switch {
		case !ok:
			unauthorized(c)
		case p.MustChangePassword:
			response.WriteError(c, http.StatusForbidden, "password_change_required")
		default:
			c.Next()
		}
	}
}

func unauthorized(c *gin.Context) {
	// RFC 6750: a 401 names the scheme the client should use.
	c.Header("WWW-Authenticate", `Bearer realm="maki"`)
	response.WriteError(c, http.StatusUnauthorized, "unauthorized")
}
