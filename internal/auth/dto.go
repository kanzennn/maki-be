package auth

import "github.com/kervzent/maki-be/internal/platform/httpx"

type loginRequest struct {
	Username string `json:"username" binding:"required,max=64"`
	// No password policy on sign-in — a password may predate the rules — but
	// a cap, so nobody makes the server hash a megabyte.
	Password string `json:"password" binding:"required,max=1024"`
	Client   string `json:"client" binding:"required,oneof=web mobile"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type updateMeRequest struct {
	Locale httpx.Nullable[string] `json:"locale"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required,max=1024"`
	NewPassword     string `json:"new_password" binding:"required"`
}

type forgotPasswordRequest struct {
	Login string `json:"login" binding:"required,max=254"`
}

type resetPasswordRequest struct {
	Token       string `json:"token" binding:"required,max=128"`
	NewPassword string `json:"new_password" binding:"required"`
}

type tokenRequest struct {
	Token string `json:"token" binding:"required,max=128"`
}

type changeEmailRequest struct {
	Email           string `json:"email" binding:"required,max=254"`
	CurrentPassword string `json:"current_password" binding:"required,max=1024"`
}

type sessionResponse struct {
	AccessToken      string     `json:"access_token"`
	TokenType        string     `json:"token_type"`
	ExpiresIn        int        `json:"expires_in"`
	RefreshToken     string     `json:"refresh_token,omitempty"`
	RefreshExpiresIn int        `json:"refresh_expires_in,omitempty"`
	User             meResponse `json:"user"`
}

type meResponse struct {
	UserID             string         `json:"user_id"`
	Username           string         `json:"username"`
	Name               string         `json:"name"`
	Email              *string        `json:"email"`
	EmailVerified      bool           `json:"email_verified"`
	PendingEmail       *string        `json:"pending_email"`
	Locale             *string        `json:"locale"`
	Language           string         `json:"language"`
	IsOwner            bool           `json:"is_owner"`
	IsManager          bool           `json:"is_manager"`
	OutletID           *string        `json:"outlet_id"`
	MustChangePassword bool           `json:"must_change_password"`
	Permissions        []string       `json:"permissions"`
	Outlet             *OutletSummary `json:"outlet"`
}

// toMe converts an Account, whose fields meResponse repeats with JSON tags;
// a field added to one but not the other stops this compiling.
func toMe(a Account) meResponse {
	return meResponse(a)
}
