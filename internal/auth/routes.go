package auth

import "github.com/gin-gonic/gin"

// Routes registers the module's endpoints. public has no authentication;
// authed requires an access token; protected also requires that the user has
// no password to change first. Reading /me and changing the password stay on
// authed, so a user who must change their password can do exactly that.
func (h *Handler) Routes(public, authed, protected *gin.RouterGroup) {
	public.POST("/auth/login", h.Login)
	public.POST("/auth/refresh", h.Refresh)
	public.POST("/auth/logout", h.Logout)
	public.POST("/auth/password/forgot", h.ForgotPassword)
	public.POST("/auth/password/reset", h.ResetPassword)
	public.POST("/auth/email/verify", h.VerifyEmail)

	authed.GET("/me", h.GetMe)
	authed.PUT("/me/password", h.ChangePassword)

	protected.PATCH("/me", h.UpdateMe)
	protected.PUT("/me/email", h.ChangeEmail)
}
