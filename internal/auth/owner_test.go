package auth

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

var validOwner = OwnerInput{Name: "Faiz", Username: " Pemilik ", Email: " Owner@Maki.test ", Password: "sandi owner panjang"}

func TestCreateOwner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	userID, err := h.svc.CreateOwner(t.Context(), validOwner)
	if err != nil {
		t.Fatalf("CreateOwner: %v", err)
	}

	// Stored normalised, as the Owner, with the email not yet confirmed.
	var username, email string
	var owner, verified bool
	h.scalar(&username, `SELECT username FROM users WHERE user_id = $1`, userID)
	h.scalar(&email, `SELECT email FROM users WHERE user_id = $1`, userID)
	h.scalar(&owner, `SELECT is_owner FROM users WHERE user_id = $1`, userID)
	h.scalar(&verified, `SELECT email_verified_at IS NOT NULL FROM users WHERE user_id = $1`, userID)
	if username != "pemilik" || email != "owner@maki.test" || !owner || verified {
		t.Errorf("stored %s / %s, owner %v, verified %v", username, email, owner, verified)
	}
	if got := h.actions(); len(got) != 1 || got[0] != "auth.owner_created" {
		t.Errorf("Log Action = %v, want [auth.owner_created]", got)
	}

	// The Owner signs in, and the emailed link confirms the address.
	access, _ := h.login("pemilik", "sandi owner panjang", "web")
	emails := h.runJobs()
	if len(emails) != 1 || emails[0].To != "owner@maki.test" {
		t.Fatalf("emails = %+v, want the confirmation link", emails)
	}
	verify(h, tokenIn(t, emails[0], "/verify-email")).status(http.StatusNoContent, "")
	if me := h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).me(); !me.EmailVerified || !me.IsOwner {
		t.Errorf("me = %+v, want a verified Owner", me)
	}

	// There is only ever one.
	second := validOwner
	second.Username, second.Email = "pemilik2", "other@maki.test"
	if _, err := h.svc.CreateOwner(t.Context(), second); !errors.Is(err, ErrOwnerExists) {
		t.Errorf("second CreateOwner = %v, want ErrOwnerExists", err)
	}
}

func TestCreateOwnerChecksItsInput(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	for name, tt := range map[string]struct {
		edit func(*OwnerInput)
		want string
	}{
		"no name":          {func(in *OwnerInput) { in.Name = "  " }, "name"},
		"short username":   {func(in *OwnerInput) { in.Username = "ab" }, "username"},
		"spaced username":  {func(in *OwnerInput) { in.Username = "pe milik" }, "username"},
		"bad email":        {func(in *OwnerInput) { in.Email = "owner" }, "email"},
		"short password":   {func(in *OwnerInput) { in.Password = "pendek" }, "password_too_short"},
		"password is name": {func(in *OwnerInput) { in.Password = "PEMILIK.utama"; in.Username = "pemilik.utama" }, "password_same_as_username"},
	} {
		in := validOwner
		tt.edit(&in)
		if _, err := h.svc.CreateOwner(t.Context(), in); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: error = %v, want one about %s", name, err, tt.want)
		}
	}
	var n int
	h.scalar(&n, `SELECT count(*) FROM users`)
	if n != 0 {
		t.Errorf("%d users created from bad input", n)
	}
}
