package models

// Client is where a request came from.
type Client struct {
	IP string
}

// Account is a user as GET /me shows them.
type Account struct {
	UserID             string
	Username           string
	Name               string
	Email              *string
	EmailVerified      bool
	PendingEmail       *string
	Locale             *string // their own choice; nil follows the outlet
	Language           string  // what they see, after the fallbacks
	IsOwner            bool
	IsManager          bool
	OutletID           *string
	MustChangePassword bool
	// Permissions are what the user may do, sorted: in their outlet, or for
	// the Owner in any outlet. Clients show what these allow; the server
	// checks them regardless.
	Permissions []string
	// Outlet is the user's outlet; nil for the Owner.
	Outlet *OutletSummary
}

// OutletSummary is the outlet a user belongs to, as GET /me shows it. Its
// JSON tags let MeResponse share it with Account.
type OutletSummary struct {
	OutletID string `json:"outlet_id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
	Locale   string `json:"locale"`
}

// AuthSession is what signing in or refreshing hands the client.
type AuthSession struct {
	AccessToken  string
	RefreshToken string
	User         Account
}

// RefreshToken is a session's refresh token, as a refresh finds it. The
// booleans come from the database clock.
type RefreshToken struct {
	RefreshTokenID string
	UserID         string
	FamilyID       string
	Revoked        bool
	Expired        bool
	// Replaced is set once the token was exchanged; RecentlyReplaced, if
	// that was within the grace a second browser tab gets.
	Replaced         bool
	RecentlyReplaced bool
}

// AuthToken is the single-use token of an emailed link, as the link finds
// it.
type AuthToken struct {
	AuthTokenID string
	UserID      string
	// Email is the address the link confirms; nil for a password reset.
	Email *string
}

// SessionResponse is what signing in or refreshing sends. The Android app
// gets its refresh token in the body; the browser, as a cookie.
type SessionResponse struct {
	AccessToken      string     `json:"access_token"`
	TokenType        string     `json:"token_type"`
	ExpiresIn        int        `json:"expires_in"`
	RefreshToken     string     `json:"refresh_token,omitempty"`
	RefreshExpiresIn int        `json:"refresh_expires_in,omitempty"`
	User             MeResponse `json:"user"`
}

// MeResponse is the account, as GET /me sends it.
type MeResponse struct {
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

// NewMeResponse converts an Account, whose fields MeResponse repeats with
// JSON tags; a field added to one but not the other stops this compiling.
func NewMeResponse(a Account) MeResponse {
	return MeResponse(a)
}
