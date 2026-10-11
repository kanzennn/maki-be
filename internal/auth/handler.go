package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// The web app's refresh token cookie. Scoped to the auth endpoints, so the
// browser sends it to refresh and sign-out only, and HttpOnly, so no page
// script — nor an attacker's injected one — can read it.
const (
	refreshCookie     = "maki_refresh"
	refreshCookiePath = "/api/v1/auth"
)

// Handler serves the auth endpoints.
type Handler struct {
	svc    *Service
	tokens *authn.Tokens
	logger *slog.Logger
	// secureCookies marks the refresh cookie Secure. Only development over
	// plain HTTP turns it off.
	secureCookies bool
}

// NewHandler returns the auth endpoints' handler.
func NewHandler(svc *Service, tokens *authn.Tokens, logger *slog.Logger, secureCookies bool) *Handler {
	return &Handler{svc: svc, tokens: tokens, logger: logger, secureCookies: secureCookies}
}

func client(c *gin.Context) Client {
	return Client{IP: c.ClientIP()}
}

// Login handles POST /auth/login.
func (h *Handler) Login(c *gin.Context) {
	var req loginRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	sess, err := h.svc.Login(c.Request.Context(), client(c), req.Username, req.Password)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	h.writeSession(c, sess, req.Client == "mobile")
}

// Refresh handles POST /auth/refresh: the Android app's refresh_token in the
// body, or else the web app's cookie. The new token goes back the same way.
func (h *Handler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := httpx.BindOptionalJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	token, fromCookie := req.RefreshToken, false
	if token == "" {
		if ck, err := c.Cookie(refreshCookie); err == nil {
			token, fromCookie = ck, true
		}
	}
	sess, err := h.svc.Refresh(c.Request.Context(), token)
	if err != nil {
		// A cookie that can't be refreshed is dead: drop it. Not on
		// refresh_superseded, whose request arrived with the old cookie
		// while the browser already holds the new one.
		if fromCookie && errors.Is(err, ErrUnauthorized) {
			h.clearRefreshCookie(c)
		}
		httpx.Fail(c, h.logger, err)
		return
	}
	h.writeSession(c, sess, !fromCookie)
}

// Logout handles POST /auth/logout.
func (h *Handler) Logout(c *gin.Context) {
	var req refreshRequest
	if err := httpx.BindOptionalJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	token := req.RefreshToken
	if token == "" {
		token, _ = c.Cookie(refreshCookie)
	}
	var access *authn.Session
	if raw, ok := authn.BearerToken(c.GetHeader("Authorization")); ok {
		if s, err := h.tokens.Verify(raw); err == nil {
			access = &s
		}
	}
	if err := h.svc.Logout(c.Request.Context(), client(c), token, access); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	h.clearRefreshCookie(c)
	c.Status(http.StatusNoContent)
}

// ForgotPassword handles POST /auth/password/forgot.
func (h *Handler) ForgotPassword(c *gin.Context) {
	var req forgotPasswordRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	if err := h.svc.ForgotPassword(c.Request.Context(), client(c), req.Login); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusAccepted)
}

// ResetPassword handles POST /auth/password/reset.
func (h *Handler) ResetPassword(c *gin.Context) {
	var req resetPasswordRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	if err := h.svc.ResetPassword(c.Request.Context(), client(c), req.Token, req.NewPassword); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// VerifyEmail handles POST /auth/email/verify.
func (h *Handler) VerifyEmail(c *gin.Context) {
	var req tokenRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	if err := h.svc.VerifyEmail(c.Request.Context(), client(c), req.Token); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// GetMe handles GET /me.
func (h *Handler) GetMe(c *gin.Context) {
	p, ok := h.principal(c)
	if !ok {
		return
	}
	account, err := h.svc.Me(c.Request.Context(), p.UserID)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toMe(account))
}

// UpdateMe handles PATCH /me.
func (h *Handler) UpdateMe(c *gin.Context) {
	p, ok := h.principal(c)
	if !ok {
		return
	}
	var req updateMeRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	if !req.Locale.Set {
		httpx.Fail(c, h.logger, httpx.Invalid(map[string]string{"locale": "required"}))
		return
	}
	account, err := h.svc.SetLocale(c.Request.Context(), p, req.Locale.Ptr())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toMe(account))
}

// ChangePassword handles PUT /me/password.
func (h *Handler) ChangePassword(c *gin.Context) {
	p, ok := h.principal(c)
	if !ok {
		return
	}
	var req changePasswordRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	if err := h.svc.ChangePassword(c.Request.Context(), client(c), p, req.CurrentPassword, req.NewPassword); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ChangeEmail handles PUT /me/email.
func (h *Handler) ChangeEmail(c *gin.Context) {
	p, ok := h.principal(c)
	if !ok {
		return
	}
	var req changeEmailRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	if err := h.svc.ChangeEmail(c.Request.Context(), client(c), p, req.Email, req.CurrentPassword); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusAccepted)
}

// principal returns the signed-in user, which the Authenticate middleware
// has put on every route that calls this.
func (h *Handler) principal(c *gin.Context) (authn.Principal, bool) {
	p, ok := authn.PrincipalFrom(c.Request.Context())
	if !ok {
		httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")
	}
	return p, ok
}

func (h *Handler) writeSession(c *gin.Context, sess Session, refreshInBody bool) {
	resp := sessionResponse{
		AccessToken: sess.AccessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int(AccessTokenTTL.Seconds()),
		User:        toMe(sess.User),
	}
	if refreshInBody {
		resp.RefreshToken = sess.RefreshToken
		resp.RefreshExpiresIn = int(RefreshTokenTTL.Seconds())
	} else {
		h.setRefreshCookie(c, sess.RefreshToken, int(RefreshTokenTTL.Seconds()))
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) setRefreshCookie(c *gin.Context, value string, maxAge int) {
	//nolint:gosec // Secure is off only in development over plain HTTP; HttpOnly and SameSite=Strict always hold
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     refreshCookie,
		Value:    value,
		Path:     refreshCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *Handler) clearRefreshCookie(c *gin.Context) {
	h.setRefreshCookie(c, "", -1)
}
