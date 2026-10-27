package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
)

// RegisterAuthRoutes registers signing in and out and the signed-in user's
// account. public has no authentication; authed requires an access token;
// protected also requires that the user has no password to change first.
// Reading /me and changing the password stay on authed, so a user who must
// change their password can do exactly that.
func RegisterAuthRoutes(public, authed, protected gin.IRouter, ctl *controllers.AuthController) {
	public.POST("/auth/login", ctl.Login)
	public.POST("/auth/refresh", ctl.Refresh)
	public.POST("/auth/logout", ctl.Logout)
	public.POST("/auth/password/forgot", ctl.ForgotPassword)
	public.POST("/auth/password/reset", ctl.ResetPassword)
	public.POST("/auth/email/verify", ctl.VerifyEmail)

	authed.GET("/me", ctl.GetMe)
	authed.PUT("/me/password", ctl.ChangePassword)

	protected.PATCH("/me", ctl.UpdateMe)
	protected.PUT("/me/email", ctl.ChangeEmail)
}
