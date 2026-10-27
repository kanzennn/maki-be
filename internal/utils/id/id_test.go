package id_test

import (
	"testing"

	"github.com/kervzent/maki-be/internal/utils/id"
)

func TestNew(t *testing.T) {
	seen := make(map[string]bool)
	for range 1000 {
		v := id.New()
		if len(v) != 24 {
			t.Fatalf("New() = %q, want 24 characters", v)
		}
		if !id.Valid(v) {
			t.Fatalf("New() = %q, which Valid rejects", v)
		}
		if seen[v] {
			t.Fatalf("New() returned %q twice", v)
		}
		seen[v] = true
	}
}

func TestValid(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"tz4a98xxat96iws9zmbrgj3a", true},
		{"ab", true},
		{"abcdefghijklmnopqrstuvwxyz012345", true}, // 32, the column limit
		{"abcdefghijklmnopqrstuvwxyz0123456", false},
		{"a", false},
		{"", false},
		{"1z4a98xxat96iws9zmbrgj3a", false}, // must start with a letter
		{"TZ4A98XXAT96IWS9ZMBRGJ3A", false},
		{"tz4a98xx-t96iws9zmbrgj3a", false},
		{"tz4a98xxat96iws9zmbrgj3a\n", false},
	}
	for _, tt := range tests {
		if got := id.Valid(tt.in); got != tt.want {
			t.Errorf("Valid(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
