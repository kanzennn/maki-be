// Package password hashes passwords with argon2id and checks them against
// their hash.
//
// Each hash is a PHC string that carries its own parameters, so the cost can
// be raised later: Verify reports when a hash was made with parameters other
// than the Hasher's, and the caller stores a fresh hash after that successful
// sign-in. Never bcrypt, never a fast hash, never reversible encryption.
package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// Params are argon2id's cost parameters.
type Params struct {
	Memory  uint32 // KiB
	Time    uint32 // passes over the memory
	Threads uint8
}

// Default is RFC 9106's second recommended option — 64 MiB and 3 passes — with
// 2 lanes instead of 4 for a small server.
var Default = Params{Memory: 64 * 1024, Time: 3, Threads: 2}

const (
	saltLen = 16
	keyLen  = 32

	// A stored hash asking for more than this is refused rather than
	// computed, so a corrupted row can't exhaust the server's memory.
	maxMemory = 1024 * 1024 // 1 GiB
	maxTime   = 20
)

// Length limits for a new password, in characters.
const (
	MinLength = 8
	MaxLength = 128
)

// ErrMalformedHash means a stored hash couldn't be parsed.
var ErrMalformedHash = errors.New("password: malformed hash")

// Check reports whether pw may become the password of the account named
// username: MinLength to MaxLength characters, and not the username itself.
// Any characters are allowed and nothing else is required, following
// NIST SP 800-63B. A password that breaks the rules is
// apperror.ErrPasswordTooShort or ErrPasswordTooLong, with the limit in its
// details, or ErrPasswordSameAsUsername.
func Check(pw, username string) error {
	switch n := utf8.RuneCountInString(pw); {
	case n < MinLength:
		return apperror.ErrPasswordTooShort.WithDetails(map[string]any{"min": MinLength})
	case n > MaxLength:
		return apperror.ErrPasswordTooLong.WithDetails(map[string]any{"max": MaxLength})
	}
	if username != "" && strings.EqualFold(pw, username) {
		return apperror.ErrPasswordSameAsUsername
	}
	return nil
}

// Hasher hashes and verifies passwords. At most a few hashes run at once, so
// a burst of sign-ins waits its turn instead of each taking Params.Memory at
// the same moment.
type Hasher struct {
	params Params
	slots  chan struct{}
}

// NewHasher returns a Hasher that makes new hashes with params and runs at
// most concurrency of them at once.
func NewHasher(params Params, concurrency int) *Hasher {
	return &Hasher{params: params, slots: make(chan struct{}, max(concurrency, 1))}
}

// Hash returns the PHC string for pw:
// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<key>, salt and key in unpadded
// base64.
func (h *Hasher) Hash(ctx context.Context, pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: salt: %w", err)
	}
	key, err := h.derive(ctx, pw, salt, h.params, keyLen)
	if err != nil {
		return "", err
	}
	return encode(h.params, salt, key), nil
}

// Verify reports whether pw matches encoded, and whether encoded was made with
// parameters other than the Hasher's, in which case the caller should store
// a new Hash of pw now that it is known to be right.
func (h *Hasher) Verify(ctx context.Context, pw, encoded string) (match, rehash bool, err error) {
	params, salt, want, err := decode(encoded)
	if err != nil {
		return false, false, err
	}
	got, err := h.derive(ctx, pw, salt, params, uint32(len(want))) //nolint:gosec // decode caps the key length
	if err != nil {
		return false, false, err
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}
	return true, params != h.params || len(salt) != saltLen || len(want) != keyLen, nil
}

// Waste does the work of a Verify that fails, for a sign-in with a username
// that doesn't exist. That answer then takes as long as a wrong password
// does, so timing can't tell anyone which usernames exist.
func (h *Hasher) Waste(ctx context.Context, pw string) error {
	_, err := h.derive(ctx, pw, make([]byte, saltLen), h.params, keyLen)
	return err
}

func (h *Hasher) derive(ctx context.Context, pw string, salt []byte, p Params, n uint32) ([]byte, error) {
	select {
	case h.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-h.slots }()
	return argon2.IDKey([]byte(pw), salt, p.Time, p.Memory, p.Threads, n), nil
}

var b64 = base64.RawStdEncoding

func encode(p Params, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

func decode(encoded string) (Params, []byte, []byte, error) {
	var p Params
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, ErrMalformedHash
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return p, nil, nil, ErrMalformedHash
	}
	if p.Memory < 8*uint32(p.Threads) || p.Memory > maxMemory || p.Time < 1 || p.Time > maxTime || p.Threads < 1 {
		return p, nil, nil, ErrMalformedHash
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return p, nil, nil, ErrMalformedHash
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return p, nil, nil, ErrMalformedHash
	}
	return p, salt, key, nil
}
