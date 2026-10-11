// Package authn holds what every module needs to know about who is calling:
// the access tokens the auth module issues, and the Principal that the
// Authenticate middleware puts on each request's context.
package authn

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// An access token names the user (sub) and the session (sid) — the auth
// module's refresh token family — and nothing else. Everything else about
// the user is loaded on each request rather than trusted from the token, so
// a deactivated account or a revoked session stops working at once instead of
// when the token expires.
const (
	issuer   = "maki"
	audience = "maki-api"

	// MinSecretLength is the shortest JWT_SECRET accepted, in bytes: HS256
	// is only as strong as a key of at least its hash size.
	MinSecretLength = 32
)

// ErrInvalidToken wraps every reason an access token is rejected.
var ErrInvalidToken = errors.New("authn: invalid access token")

// Tokens issues and verifies access tokens: HS256 JWTs signed with one
// secret. Changing the secret only makes clients refresh early, since
// refresh tokens don't depend on it.
type Tokens struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

// NewTokens returns Tokens that sign with secret and last for ttl.
func NewTokens(secret []byte, ttl time.Duration) (*Tokens, error) {
	if len(secret) < MinSecretLength {
		return nil, fmt.Errorf("authn: the signing secret must be at least %d bytes", MinSecretLength)
	}
	return &Tokens{key: secret, ttl: ttl, now: time.Now}, nil
}

// BearerToken extracts the token from an Authorization header value
// ("Bearer <token>"). The scheme is case-insensitive (RFC 7235).
func BearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

// Session is who a verified access token speaks for.
type Session struct {
	UserID    string
	SessionID string
}

type claims struct {
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

// Issue returns a signed access token for the user's session, and when it
// expires.
func (t *Tokens) Issue(s Session) (string, time.Time, error) {
	if s.UserID == "" || s.SessionID == "" {
		return "", time.Time{}, errors.New("authn: a token needs a user and a session")
	}
	now := t.now()
	expires := now.Add(t.ttl)
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		SessionID: s.SessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			Subject:   s.UserID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expires),
		},
	}).SignedString(t.key)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("authn: sign access token: %w", err)
	}
	return token, expires, nil
}

// Verify checks token's signature, algorithm (HS256 only — never "none" or
// another algorithm an attacker picked), issuer, audience, issue time and
// expiry, and returns the session it names.
func (t *Tokens) Verify(token string) (Session, error) {
	var c claims
	_, err := jwt.ParseWithClaims(token, &c,
		func(*jwt.Token) (any, error) { return t.key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil {
		return Session{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	if c.Subject == "" || c.SessionID == "" {
		return Session{}, fmt.Errorf("%w: no user or session", ErrInvalidToken)
	}
	return Session{UserID: c.Subject, SessionID: c.SessionID}, nil
}
