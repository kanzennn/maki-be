package controllers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/jwt"
	"github.com/kervzent/maki-be/internal/validations"
)

// The web app's refresh token cookie. Scoped to the auth endpoints, so the
// browser sends it to refresh and sign-out only, and HttpOnly, so no page
// script — nor an attacker's injected one — can read it.
const (
	refreshCookie     = "maki_refresh"
	refreshCookiePath = "/api/v1/auth"
)

// AuthController serves signing in and out, the signed-in user's account
// (/me), and the emailed links' endpoints.
type AuthController struct {
	auth   *services.AuthService
	tokens *jwt.Tokens
	// secureCookies marks the refresh cookie Secure. Only development over
	// plain HTTP turns it off.
	secureCookies bool
}

// NewAuthController returns the auth endpoints' controller. tokens reads
// the access token that signing out may carry.
func NewAuthController(auth *services.AuthService, tokens *jwt.Tokens, secureCookies bool) *AuthController {
	return &AuthController{auth: auth, tokens: tokens, secureCookies: secureCookies}
}

func client(c *gin.Context) models.Client {
	return models.Client{IP: c.ClientIP()}
}

// Login handles POST /auth/login.
func (ctl *AuthController) Login(c *gin.Context) {
	var req validations.LoginRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	sess, err := ctl.auth.Login(c.Request.Context(), client(c), req.Username, req.Password)
	if err != nil {
		_ = c.Error(err)
		return
	}
	ctl.writeSession(c, sess, req.Client == "mobile")
}

// Refresh handles POST /auth/refresh: the Android app's refresh_token in the
// body, or else the web app's cookie. The new token goes back the same way.
func (ctl *AuthController) Refresh(c *gin.Context) {
	var req validations.RefreshRequest
	if err := validations.BindOptionalJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	token, fromCookie := req.RefreshToken, false
	if token == "" {
		if ck, err := c.Cookie(refreshCookie); err == nil {
			token, fromCookie = ck, true
		}
	}
	sess, err := ctl.auth.Refresh(c.Request.Context(), token)
	if err != nil {
		// A cookie that can't be refreshed is dead: drop it. Not on
		// refresh_superseded, whose request arrived with the old cookie
		// while the browser already holds the new one.
		if fromCookie && errors.Is(err, apperror.ErrUnauthorized) {
			ctl.clearRefreshCookie(c)
		}
		_ = c.Error(err)
		return
	}
	ctl.writeSession(c, sess, !fromCookie)
}

// Logout handles POST /auth/logout.
func (ctl *AuthController) Logout(c *gin.Context) {
	var req validations.RefreshRequest
	if err := validations.BindOptionalJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	token := req.RefreshToken
	if token == "" {
		token, _ = c.Cookie(refreshCookie)
	}
	var access *jwt.Session
	if raw, ok := jwt.BearerToken(c.GetHeader("Authorization")); ok {
		if s, err := ctl.tokens.Verify(raw); err == nil {
			access = &s
		}
	}
	if err := ctl.auth.Logout(c.Request.Context(), client(c), token, access); err != nil {
		_ = c.Error(err)
		return
	}
	ctl.clearRefreshCookie(c)
	c.Status(http.StatusNoContent)
}

// ForgotPassword handles POST /auth/password/forgot.
func (ctl *AuthController) ForgotPassword(c *gin.Context) {
	var req validations.ForgotPasswordRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	if err := ctl.auth.ForgotPassword(c.Request.Context(), client(c), req.Login); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusAccepted)
}

// ResetPassword handles POST /auth/password/reset.
func (ctl *AuthController) ResetPassword(c *gin.Context) {
	var req validations.ResetPasswordRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	if err := ctl.auth.ResetPassword(c.Request.Context(), client(c), req.Token, req.NewPassword); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// VerifyEmail handles POST /auth/email/verify.
func (ctl *AuthController) VerifyEmail(c *gin.Context) {
	var req validations.VerifyEmailRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	if err := ctl.auth.VerifyEmail(c.Request.Context(), client(c), req.Token); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// GetMe handles GET /me.
func (ctl *AuthController) GetMe(c *gin.Context) {
	p, ok := signedIn(c)
	if !ok {
		return
	}
	account, err := ctl.auth.Me(c.Request.Context(), p.UserID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewMeResponse(account))
}

// UpdateMe handles PATCH /me.
func (ctl *AuthController) UpdateMe(c *gin.Context) {
	p, ok := signedIn(c)
	if !ok {
		return
	}
	var req validations.UpdateMeRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	if !req.Locale.Set {
		_ = c.Error(apperror.Invalid(map[string]string{"locale": "required"}))
		return
	}
	account, err := ctl.auth.SetLocale(c.Request.Context(), p, req.Locale.Ptr())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewMeResponse(account))
}

// ChangePassword handles PUT /me/password.
func (ctl *AuthController) ChangePassword(c *gin.Context) {
	p, ok := signedIn(c)
	if !ok {
		return
	}
	var req validations.ChangePasswordRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	if err := ctl.auth.ChangePassword(c.Request.Context(), client(c), p, req.CurrentPassword, req.NewPassword); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ChangeEmail handles PUT /me/email.
func (ctl *AuthController) ChangeEmail(c *gin.Context) {
	p, ok := signedIn(c)
	if !ok {
		return
	}
	var req validations.ChangeEmailRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	if err := ctl.auth.ChangeEmail(c.Request.Context(), client(c), p, req.Email, req.CurrentPassword); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusAccepted)
}

// signedIn returns the signed-in user, whom the Authenticate middleware has
// put on every route that calls this; without one, the request is
// unauthorized.
func signedIn(c *gin.Context) (models.Principal, bool) {
	p, ok := models.PrincipalFrom(c.Request.Context())
	if !ok {
		_ = c.Error(apperror.ErrUnauthorized)
	}
	return p, ok
}

func (ctl *AuthController) writeSession(c *gin.Context, sess models.AuthSession, refreshInBody bool) {
	resp := models.SessionResponse{
		AccessToken: sess.AccessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int(services.AccessTokenTTL.Seconds()),
		User:        models.NewMeResponse(sess.User),
	}
	if refreshInBody {
		resp.RefreshToken = sess.RefreshToken
		resp.RefreshExpiresIn = int(services.RefreshTokenTTL.Seconds())
	} else {
		ctl.setRefreshCookie(c, sess.RefreshToken, int(services.RefreshTokenTTL.Seconds()))
	}
	c.JSON(http.StatusOK, resp)
}

func (ctl *AuthController) setRefreshCookie(c *gin.Context, value string, maxAge int) {
	//nolint:gosec // Secure is off only in development over plain HTTP; HttpOnly and SameSite=Strict always hold
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     refreshCookie,
		Value:    value,
		Path:     refreshCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   ctl.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})
}

func (ctl *AuthController) clearRefreshCookie(c *gin.Context) {
	ctl.setRefreshCookie(c, "", -1)
}
