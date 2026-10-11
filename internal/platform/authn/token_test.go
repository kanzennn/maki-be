package authn

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
	signWith := func(method jwt.SigningMethod, key any, edit func(*claims)) string {
		c := claims{SessionID: session.SessionID, RegisteredClaims: jwt.RegisteredClaims{
			Issuer: issuer, Audience: jwt.ClaimStrings{audience}, Subject: session.UserID,
			IssuedAt: jwt.NewNumericDate(base), ExpiresAt: jwt.NewNumericDate(base.Add(time.Hour)),
		}}
		if edit != nil {
			edit(&c)
		}
		s, err := jwt.NewWithClaims(method, c).SignedString(key)
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
		"expired":          {good, base.Add(15*time.Minute + time.Second), jwt.ErrTokenExpired},
		"tampered payload": {tamper(t, good, func(m map[string]any) { m["sub"] = "someone-else" }), base, jwt.ErrTokenSignatureInvalid},
		"other secret":     {signWith(jwt.SigningMethodHS256, otherKey, nil), base, jwt.ErrTokenSignatureInvalid},
		"alg none":         {unsigned(t, good), base, jwt.ErrTokenSignatureInvalid},
		"HS512":            {signWith(jwt.SigningMethodHS512, secret, nil), base, jwt.ErrTokenSignatureInvalid},
		"other issuer":     {signWith(jwt.SigningMethodHS256, secret, func(c *claims) { c.Issuer = "evil" }), base, jwt.ErrTokenInvalidIssuer},
		"other audience":   {signWith(jwt.SigningMethodHS256, secret, func(c *claims) { c.Audience = jwt.ClaimStrings{"other"} }), base, jwt.ErrTokenInvalidAudience},
		"no expiry":        {signWith(jwt.SigningMethodHS256, secret, func(c *claims) { c.ExpiresAt = nil }), base, jwt.ErrTokenRequiredClaimMissing},
		"issued in future": {signWith(jwt.SigningMethodHS256, secret, func(c *claims) { c.IssuedAt = jwt.NewNumericDate(base.Add(time.Hour)) }), base, jwt.ErrTokenUsedBeforeIssued},
		"no session":       {signWith(jwt.SigningMethodHS256, secret, func(c *claims) { c.SessionID = "" }), base, nil},
		"no subject":       {signWith(jwt.SigningMethodHS256, secret, func(c *claims) { c.Subject = "" }), base, nil},
		"garbage":          {"not.a.token", base, jwt.ErrTokenMalformed},
		"empty":            {"", base, jwt.ErrTokenMalformed},
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

func TestPrincipalOnContext(t *testing.T) {
	if _, ok := PrincipalFrom(context.Background()); ok {
		t.Error("PrincipalFrom found a principal on an empty context")
	}
	p := Principal{UserID: "u1", IsOwner: true, Permissions: NewPermissions("menu.view")}
	got, ok := PrincipalFrom(WithPrincipal(context.Background(), p))
	if !ok || !reflect.DeepEqual(got, p) {
		t.Errorf("PrincipalFrom = %+v, %v; want %+v", got, ok, p)
	}
}

func TestPermissions(t *testing.T) {
	p := NewPermissions("menu.view", "checkout.create", "menu.view")
	if !p.Has("menu.view") || !p.Has("checkout.create") || p.Has("menu.manage") {
		t.Errorf("Has on %v", p)
	}
	if got := p.List(); !slices.Equal(got, []string{"checkout.create", "menu.view"}) {
		t.Errorf("List = %v, want sorted and without duplicates", got)
	}
	var none Permissions
	if none.Has("menu.view") || none.List() == nil || len(none.List()) != 0 {
		t.Error("the zero Permissions should hold nothing, and list as empty rather than nil")
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
