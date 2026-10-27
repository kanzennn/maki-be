package jwt

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
)

var (
	secret  = []byte("0123456789abcdef0123456789abcdef")
	session = Session{UserID: "tz4a98xxat96iws9zmbrgj3a", SessionID: "p0r6zq2mn4xk8y1w3v5u7t9s"}
	base    = time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
)

// newTokens returns Tokens whose clock reads *clock.
func newTokens(t *testing.T, clock *time.Time) *Tokens {
	t.Helper()
	tk, err := NewTokens(secret, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	tk.now = func() time.Time { return *clock }
	return tk
}

func TestIssueAndVerify(t *testing.T) {
	clock := base
	tk := newTokens(t, &clock)

	token, expires, err := tk.Issue(session)
	if err != nil {
		t.Fatal(err)
	}
	if !expires.Equal(base.Add(15 * time.Minute)) {
		t.Errorf("expires = %v, want 15 minutes after issue", expires)
	}

	clock = base.Add(14 * time.Minute)
	got, err := tk.Verify(token)
	if err != nil || got != session {
		t.Errorf("Verify = %+v, %v; want %+v", got, err, session)
	}
}

func TestVerifyRejects(t *testing.T) {
	clock := base
	tk := newTokens(t, &clock)
	good, _, _ := tk.Issue(session)

	// signWith signs the claims of a normal token with another method or key.
	signWith := func(method gojwt.SigningMethod, key any, edit func(*claims)) string {
		c := claims{SessionID: session.SessionID, RegisteredClaims: gojwt.RegisteredClaims{
			Issuer: issuer, Audience: gojwt.ClaimStrings{audience}, Subject: session.UserID,
			IssuedAt: gojwt.NewNumericDate(base), ExpiresAt: gojwt.NewNumericDate(base.Add(time.Hour)),
		}}
		if edit != nil {
			edit(&c)
		}
		s, err := gojwt.NewWithClaims(method, c).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	otherKey := []byte("fedcba9876543210fedcba9876543210")

	tests := map[string]struct {
		token string
		clock time.Time
		cause error // the jwt error it must wrap, if a specific one
	}{
		"expired":          {good, base.Add(15*time.Minute + time.Second), gojwt.ErrTokenExpired},
		"tampered payload": {tamper(t, good, func(m map[string]any) { m["sub"] = "someone-else" }), base, gojwt.ErrTokenSignatureInvalid},
		"other secret":     {signWith(gojwt.SigningMethodHS256, otherKey, nil), base, gojwt.ErrTokenSignatureInvalid},
		"alg none":         {unsigned(t, good), base, gojwt.ErrTokenSignatureInvalid},
		"HS512":            {signWith(gojwt.SigningMethodHS512, secret, nil), base, gojwt.ErrTokenSignatureInvalid},
		"other issuer":     {signWith(gojwt.SigningMethodHS256, secret, func(c *claims) { c.Issuer = "evil" }), base, gojwt.ErrTokenInvalidIssuer},
		"other audience":   {signWith(gojwt.SigningMethodHS256, secret, func(c *claims) { c.Audience = gojwt.ClaimStrings{"other"} }), base, gojwt.ErrTokenInvalidAudience},
		"no expiry":        {signWith(gojwt.SigningMethodHS256, secret, func(c *claims) { c.ExpiresAt = nil }), base, gojwt.ErrTokenRequiredClaimMissing},
		"issued in future": {signWith(gojwt.SigningMethodHS256, secret, func(c *claims) { c.IssuedAt = gojwt.NewNumericDate(base.Add(time.Hour)) }), base, gojwt.ErrTokenUsedBeforeIssued},
		"no session":       {signWith(gojwt.SigningMethodHS256, secret, func(c *claims) { c.SessionID = "" }), base, nil},
		"no subject":       {signWith(gojwt.SigningMethodHS256, secret, func(c *claims) { c.Subject = "" }), base, nil},
		"garbage":          {"not.a.token", base, gojwt.ErrTokenMalformed},
		"empty":            {"", base, gojwt.ErrTokenMalformed},
	}
	for name, tt := range tests {
		clock = tt.clock
		_, err := tk.Verify(tt.token)
		if !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s: Verify error = %v, want ErrInvalidToken", name, err)
			continue
		}
		if tt.cause != nil && !errors.Is(err, tt.cause) {
			t.Errorf("%s: Verify error = %v, want it to wrap %v", name, err, tt.cause)
		}
	}
}

func TestNewTokensRejectsShortSecret(t *testing.T) {
	if _, err := NewTokens([]byte("too short"), time.Minute); err == nil {
		t.Error("NewTokens accepted a 9-byte secret")
	}
}

func TestIssueNeedsUserAndSession(t *testing.T) {
	clock := base
	tk := newTokens(t, &clock)
	for _, s := range []Session{{UserID: "u"}, {SessionID: "s"}} {
		if _, _, err := tk.Issue(s); err == nil {
			t.Errorf("Issue(%+v) succeeded, want an error", s)
		}
	}
}

// tamper rewrites token's payload and keeps the original signature.
func tamper(t *testing.T, token string, edit func(map[string]any)) string {
	t.Helper()
	parts := strings.Split(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	edit(payload)
	raw, _ = json.Marshal(payload)
	parts[1] = base64.RawURLEncoding.EncodeToString(raw)
	return strings.Join(parts, ".")
}

// unsigned turns token into an "alg": "none" token with the same payload.
func unsigned(t *testing.T, token string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	return header + "." + strings.Split(token, ".")[1] + "."
}
