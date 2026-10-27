package password

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// Cheap parameters keep the tests fast; Default is exercised by
// TestDefaultCost.
var testParams = Params{Memory: 1024, Time: 1, Threads: 1}

func TestHashAndVerify(t *testing.T) {
	h := NewHasher(testParams, 2)
	ctx := t.Context()

	encoded, err := h.Hash(ctx, "kopi susu 123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=1024,t=1,p=1$") {
		t.Errorf("hash = %q, want a PHC argon2id string with its parameters", encoded)
	}

	match, rehash, err := h.Verify(ctx, "kopi susu 123", encoded)
	if err != nil || !match || rehash {
		t.Errorf("Verify(right password) = %v, %v, %v; want true, false, nil", match, rehash, err)
	}
	for _, wrong := range []string{"kopi susu 12", "Kopi susu 123", "", "kopi susu 123 "} {
		if match, _, err := h.Verify(ctx, wrong, encoded); err != nil || match {
			t.Errorf("Verify(%q) = %v, %v; want false, nil", wrong, match, err)
		}
	}
}

func TestHashUsesAFreshSalt(t *testing.T) {
	h := NewHasher(testParams, 1)
	a, _ := h.Hash(t.Context(), "same password")
	b, _ := h.Hash(t.Context(), "same password")
	if a == b {
		t.Error("two hashes of the same password are identical: the salt isn't random")
	}
}

func TestVerifyAsksForRehashWhenParametersChange(t *testing.T) {
	old := NewHasher(testParams, 1)
	encoded, err := old.Hash(t.Context(), "kopi susu 123")
	if err != nil {
		t.Fatal(err)
	}

	stronger := NewHasher(Params{Memory: 2048, Time: 2, Threads: 1}, 1)
	match, rehash, err := stronger.Verify(t.Context(), "kopi susu 123", encoded)
	if err != nil || !match || !rehash {
		t.Errorf("Verify with new parameters = %v, %v, %v; want true, true, nil", match, rehash, err)
	}
	// A wrong password never asks for a rehash.
	if _, rehash, _ := stronger.Verify(t.Context(), "wrong", encoded); rehash {
		t.Error("a failed Verify asked for a rehash")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	h := NewHasher(testParams, 1)
	good, _ := h.Hash(t.Context(), "kopi susu 123")
	parts := strings.Split(good, "$")

	for name, encoded := range map[string]string{
		"empty":          "",
		"bcrypt":         "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy",
		"argon2i":        strings.Replace(good, "argon2id", "argon2i", 1),
		"other version":  strings.Replace(good, "v=19", "v=16", 1),
		"missing part":   strings.Join(parts[:5], "$"),
		"bad salt":       strings.Replace(good, parts[4], "!!!", 1),
		"huge memory":    strings.Replace(good, "m=1024", "m=4194304", 1),
		"zero passes":    strings.Replace(good, "t=1", "t=0", 1),
		"zero threads":   strings.Replace(good, "p=1", "p=0", 1),
		"short key":      strings.Replace(good, parts[5], "AAAA", 1),
		"garbage params": strings.Replace(good, "m=1024,t=1,p=1", "m=x,t=y,p=z", 1),
	} {
		if match, _, err := h.Verify(t.Context(), "kopi susu 123", encoded); !errors.Is(err, ErrMalformedHash) || match {
			t.Errorf("%s: Verify = %v, %v; want false, ErrMalformedHash", name, match, err)
		}
	}
}

func TestWasteTakesAsLongAsAFailedVerify(t *testing.T) {
	h := NewHasher(Params{Memory: 16 * 1024, Time: 2, Threads: 1}, 1)
	encoded, _ := h.Hash(t.Context(), "kopi susu 123")

	// The fastest of several runs: other work on the machine, such as the
	// rest of the test suite, only ever adds time, so the minimum is the cost
	// of the call itself.
	fastest := func(fn func()) time.Duration {
		var best time.Duration
		for i := range 7 {
			start := time.Now()
			fn()
			if d := time.Since(start); i == 0 || d < best {
				best = d
			}
		}
		return best
	}
	verify := fastest(func() { _, _, _ = h.Verify(t.Context(), "wrong password", encoded) })
	waste := fastest(func() { _ = h.Waste(t.Context(), "wrong password") })

	// Same work, so within a generous factor; a Waste that did nothing
	// would be orders of magnitude faster.
	if waste < verify/3 || waste > verify*3 {
		t.Errorf("Waste took %v, a failed Verify %v; want about the same", waste, verify)
	}
}

func TestHasherWaitsForAFreeSlot(t *testing.T) {
	h := NewHasher(testParams, 1)
	h.slots <- struct{}{} // every slot busy

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := h.Hash(ctx, "kopi susu 123"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Hash with no free slot = %v, want the context's error", err)
	}

	<-h.slots
	if _, err := h.Hash(t.Context(), "kopi susu 123"); err != nil {
		t.Errorf("Hash once a slot is free: %v", err)
	}
}

func TestCheck(t *testing.T) {
	tests := []struct {
		pw, username string
		want         error
	}{
		{"kopi susu", "owner", nil},
		{"12345678", "owner", nil},        // no composition rules
		{"pässwörd", "owner", nil},        // 8 characters, more bytes
		{"sandi rahasia 🔑", "owner", nil}, // any characters
		{"1234567", "owner", apperror.ErrPasswordTooShort},
		{strings.Repeat("a", MaxLength), "owner", nil},
		{strings.Repeat("a", MaxLength+1), "owner", apperror.ErrPasswordTooLong},
		{"Pemilik1", "pemilik1", apperror.ErrPasswordSameAsUsername},
		{"pemilik1", "", nil},
	}
	for _, tt := range tests {
		err := Check(tt.pw, tt.username)
		if tt.want == nil && err != nil || tt.want != nil && (err == nil || err.Error() != tt.want.Error()) {
			t.Errorf("Check(%q, %q) = %v, want %v", tt.pw, tt.username, err, tt.want)
		}
	}
	if d := Check("short", "").(interface{ Details() map[string]any }).Details(); d["min"] != MinLength {
		t.Errorf("ErrPasswordTooShort details = %v, want min=%d", d, MinLength)
	}
}

func TestDefaultCost(t *testing.T) {
	if testing.Short() {
		t.Skip("computes a full-cost hash")
	}
	h := NewHasher(Default, 1)
	start := time.Now()
	encoded, err := h.Hash(t.Context(), "kopi susu 123")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("one Default hash took %v", time.Since(start))
	if match, rehash, err := h.Verify(t.Context(), "kopi susu 123", encoded); err != nil || !match || rehash {
		t.Errorf("Verify = %v, %v, %v", match, rehash, err)
	}
}
