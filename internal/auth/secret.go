package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// newSecret returns a random token — a refresh token, or the token in an
// emailed link — and the hash stored in its place. The token itself is
// never stored.
func newSecret() (token, hash string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // never fails since Go 1.24
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashSecret(token)
}

// hashSecret is how a token is stored and looked up: SHA-256, in hex. The
// tokens carry 256 random bits, so unlike a password there is nothing to
// guess and a fast hash is enough.
func hashSecret(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
