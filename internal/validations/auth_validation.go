package validations

// The bodies of the auth and /me endpoints.
type (
	// LoginRequest is the body of POST /auth/login.
	LoginRequest struct {
		Username string `json:"username" binding:"required,max=64"`
		// No password policy on sign-in — a password may predate the rules —
		// but a cap, so nobody makes the server hash a megabyte.
		Password string `json:"password" binding:"required,max=1024"`
		Client   string `json:"client" binding:"required,oneof=web mobile"`
	}

	// RefreshRequest is the optional body of POST /auth/refresh and
	// /auth/logout: the Android app's refresh token. The browser sends its
	// cookie instead.
	RefreshRequest struct {
		RefreshToken string `json:"refresh_token"`
	}

	// UpdateMeRequest is the body of PATCH /me.
	UpdateMeRequest struct {
		Locale Nullable[string] `json:"locale"`
	}

	// ChangePasswordRequest is the body of PUT /me/password.
	ChangePasswordRequest struct {
		CurrentPassword string `json:"current_password" binding:"required,max=1024"`
		NewPassword     string `json:"new_password" binding:"required"`
	}

	// ForgotPasswordRequest is the body of POST /auth/password/forgot.
	ForgotPasswordRequest struct {
		Login string `json:"login" binding:"required,max=254"`
	}

	// ResetPasswordRequest is the body of POST /auth/password/reset.
	ResetPasswordRequest struct {
		Token       string `json:"token" binding:"required,max=128"`
		NewPassword string `json:"new_password" binding:"required"`
	}

	// VerifyEmailRequest is the body of POST /auth/email/verify.
	VerifyEmailRequest struct {
		Token string `json:"token" binding:"required,max=128"`
	}

	// ChangeEmailRequest is the body of PUT /me/email.
	ChangeEmailRequest struct {
		Email           string `json:"email" binding:"required,max=254"`
		CurrentPassword string `json:"current_password" binding:"required,max=1024"`
	}
)
