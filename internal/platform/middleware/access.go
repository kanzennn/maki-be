package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// These go after Authenticate, in the order of the architecture standard:
// scope, then permission. Without a principal each answers 401, so a route
// wired without Authenticate fails closed.

// RequireOwner refuses — 403 forbidden — anyone but the Owner.
func RequireOwner() gin.HandlerFunc {
	return requireAccount(func(p authn.Principal) bool { return p.IsOwner })
}

// RequireManager refuses — 403 forbidden — anyone but a Manager. On an
// outlet's routes it goes after OutletScope, so it is that outlet's Manager:
// for what the fixed roles alone may do, such as activating a cashier
// device.
func RequireManager() gin.HandlerFunc {
	return requireAccount(func(p authn.Principal) bool { return p.IsManager })
}

// RequireManagerOrOwner is RequireManager that also lets the Owner through,
// who looks into every outlet.
func RequireManagerOrOwner() gin.HandlerFunc {
	return requireAccount(func(p authn.Principal) bool { return p.IsManager || p.IsOwner })
}

// requireAccount lets through a principal whose account allowed accepts.
func requireAccount(allowed func(authn.Principal) bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := authn.PrincipalFrom(c.Request.Context())
		switch {
		case !ok:
			unauthorized(c)
		case !allowed(p):
			httpx.WriteError(c, http.StatusForbidden, "forbidden")
		default:
			c.Next()
		}
	}
}

// OutletFinder tells whether an outlet exists and isn't deleted; the outlet
// module implements it.
type OutletFinder interface {
	OutletExists(ctx context.Context, outletID string) (bool, error)
}

// OutletScope lets a request act in the outlet its path names (:oid) only
// if the principal may: the Owner in any outlet that exists, deactivated
// ones included, and everyone else in their own, which Authenticate has
// already found active. Anything else is 404 outlet_not_found, the same
// answer for an outlet that doesn't exist and one that isn't theirs, so it
// tells nobody which outlets exist.
func OutletScope(outlets OutletFinder, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := authn.PrincipalFrom(c.Request.Context())
		if !ok {
			unauthorized(c)
			return
		}
		outletID := c.Param("oid")
		if !p.IsOwner {
			if outletID == "" || outletID != p.OutletID {
				httpx.WriteError(c, http.StatusNotFound, "outlet_not_found")
				return
			}
			c.Next()
			return
		}
		exists, err := outlets.OutletExists(c.Request.Context(), outletID)
		if err != nil {
			httpx.Fail(c, logger, fmt.Errorf("find outlet %s: %w", outletID, err))
			return
		}
		if !exists {
			httpx.WriteError(c, http.StatusNotFound, "outlet_not_found")
			return
		}
		c.Next()
	}
}

// RequirePermission refuses — 403 forbidden — a principal without the
// permission code. On an outlet's routes it goes after OutletScope, so the
// permission always applies to an outlet the principal may act in.
func RequirePermission(code string) gin.HandlerFunc {
	return RequireAnyPermission(code)
}

// RequireAnyPermission is RequirePermission for a route that more than one
// permission opens: the principal needs at least one of codes.
func RequireAnyPermission(codes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := authn.PrincipalFrom(c.Request.Context())
		switch {
		case !ok:
			unauthorized(c)
		case !slices.ContainsFunc(codes, p.Permissions.Has):
			httpx.WriteError(c, http.StatusForbidden, "forbidden")
		default:
			c.Next()
		}
	}
}
