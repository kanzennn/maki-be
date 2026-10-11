package auth

import (
	"net/http"
	"strings"
	"testing"
)

func changePassword(h *harness, access, current, next string) response {
	return h.do(request{method: http.MethodPut, path: "/api/v1/me/password", bearer: access, body: map[string]string{
		"current_password": current, "new_password": next,
	}})
}

func TestChangePassword(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "pemilik.toko", password: "kopi susu 123", owner: true, email: "owner@maki.test", verified: true})
	access, cookie := h.login("pemilik.toko", "kopi susu 123", "web")
	otherAccess, otherCookie := h.login("pemilik.toko", "kopi susu 123", "web")

	changePassword(h, access, "salah", "sandi baru panjang").status(http.StatusUnprocessableEntity, "wrong_password")
	r := changePassword(h, access, "kopi susu 123", "pendek").status(http.StatusUnprocessableEntity, "password_too_short")
	if r.errorDetails()["min"] != float64(8) {
		t.Errorf("details = %v, want min 8", r.errorDetails())
	}
	changePassword(h, access, "kopi susu 123", "Pemilik.Toko").status(http.StatusUnprocessableEntity, "password_same_as_username")
	changePassword(h, access, "kopi susu 123", "sandi baru panjang").status(http.StatusNoContent, "")

	// This session carries on; every other one ends.
	h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).status(http.StatusOK, "")
	refresh(h, cookie, "").status(http.StatusOK, "")
	h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: otherAccess}).status(http.StatusUnauthorized, "unauthorized")
	refresh(h, otherCookie, "").status(http.StatusUnauthorized, "unauthorized")

	h.do(request{method: http.MethodPost, path: "/api/v1/auth/login", body: loginBody("pemilik.toko", "kopi susu 123", "web")}).
		status(http.StatusUnauthorized, "invalid_credentials")
	h.login("pemilik.toko", "sandi baru panjang", "web")

	emails := h.runJobs()
	if len(emails) != 1 || emails[0].To != "owner@maki.test" || !strings.Contains(emails[0].Subject, "Password") {
		t.Fatalf("emails = %+v, want the password-changed notice to the owner", emails)
	}
}

func TestChangePasswordNoticeNeedsAVerifiedEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123", email: "kasir1@maki.test"}) // not verified
	access, _ := h.login("kasir1", "kopi susu 123", "web")

	changePassword(h, access, "kopi susu 123", "sandi baru panjang").status(http.StatusNoContent, "")
	if emails := h.runJobs(); len(emails) != 0 {
		t.Errorf("sent %d emails to an unverified address, want none", len(emails))
	}
}

func TestChangePasswordLimitsWrongGuesses(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	access, _ := h.login("kasir1", "kopi susu 123", "web")

	for range 5 {
		changePassword(h, access, "tebakan", "sandi baru panjang").status(http.StatusUnprocessableEntity, "wrong_password")
	}
	// A stolen session can't be used to guess the password.
	changePassword(h, access, "kopi susu 123", "sandi baru panjang").status(http.StatusTooManyRequests, "rate_limited")
}

func forgot(h *harness, login, ip string) response {
	return h.do(request{method: http.MethodPost, path: "/api/v1/auth/password/forgot", body: map[string]string{"login": login}, ip: ip})
}

func reset(h *harness, token, next string) response {
	return h.do(request{method: http.MethodPost, path: "/api/v1/auth/password/reset", body: map[string]string{
		"token": token, "new_password": next,
	}})
}

func TestPasswordResetByEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "owner", password: "kopi susu 123", name: "Faiz", owner: true, email: "owner@maki.test", verified: true})
	access, cookie := h.login("owner", "kopi susu 123", "web")

	forgot(h, "owner", "").status(http.StatusAccepted, "")
	emails := h.runJobs()
	if len(emails) != 1 || emails[0].To != "owner@maki.test" {
		t.Fatalf("emails = %+v, want one to the owner", emails)
	}
	if !strings.Contains(emails[0].Text, "Halo Faiz") || !strings.Contains(emails[0].Text, "30 menit") {
		t.Errorf("email text:\n%s", emails[0].Text)
	}
	token := tokenIn(t, emails[0], "/reset-password")

	// The plain token was never stored: only its hash.
	var stored int
	h.scalar(&stored, `SELECT count(*) FROM auth_tokens WHERE token_hash = $1`, token)
	if stored != 0 {
		t.Error("the plain reset token is in the database")
	}
	var queued int
	h.scalar(&queued, `SELECT count(*) FROM outbox_jobs WHERE payload::text LIKE '%' || $1 || '%'`, token)
	if queued != 0 {
		t.Error("the plain reset token is in the outbox")
	}

	reset(h, token, "pendek").status(http.StatusUnprocessableEntity, "password_too_short")
	// A rejected password doesn't spend the link.
	reset(h, token, "sandi baru panjang").status(http.StatusNoContent, "")
	reset(h, token, "sandi lain lagi").status(http.StatusBadRequest, "token_invalid")

	// Every session ended, the new password works, and a notice went out.
	h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).status(http.StatusUnauthorized, "unauthorized")
	refresh(h, cookie, "").status(http.StatusUnauthorized, "unauthorized")
	h.login("owner", "sandi baru panjang", "web")
	notices := h.runJobs()
	if len(notices) != 1 || notices[0].To != "owner@maki.test" {
		t.Errorf("notices = %+v, want the password-changed email", notices)
	}

	got := h.actions()
	want := []string{"auth.login", "auth.password_reset_requested", "auth.password_reset", "auth.login"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Log Action = %v, want %v", got, want)
	}
}

func TestForgotPasswordAnswersTheSameForEveryone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "owner", password: "kopi susu 123", owner: true, email: "owner@maki.test", verified: true})
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123", email: "kasir1@maki.test", verified: true})

	for _, login := range []string{"nobody", "nobody@maki.test", "kasir1", "kasir1@maki.test"} {
		r := forgot(h, login, "")
		if r.Code != http.StatusAccepted || r.Body.Len() != 0 {
			t.Errorf("%s: %d %q, want 202 with no body", login, r.Code, r.Body)
		}
	}
	if emails := h.runJobs(); len(emails) != 0 {
		t.Fatalf("sent %d emails for non-owners, want none", len(emails))
	}

	// The Owner's email works like the username, in any case.
	forgot(h, "  Owner@MAKI.test ", "").status(http.StatusAccepted, "")
	if emails := h.runJobs(); len(emails) != 1 {
		t.Errorf("sent %d emails for the owner's address, want 1", len(emails))
	}
}

func TestForgotPasswordNeedsAVerifiedOwnerEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "owner", password: "kopi susu 123", owner: true, email: "owner@maki.test"}) // not verified

	forgot(h, "owner", "").status(http.StatusAccepted, "")
	forgot(h, "owner@maki.test", "").status(http.StatusAccepted, "")
	if emails := h.runJobs(); len(emails) != 0 {
		t.Errorf("sent %d reset emails to an unverified address, want none", len(emails))
	}
}

func TestForgotPasswordLimits(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "owner", password: "kopi susu 123", owner: true, email: "owner@maki.test", verified: true})

	// Past three an hour the account gets no more emails, but the answer
	// doesn't change, so it can't be used to find accounts.
	for i, ip := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3", "198.51.100.4"} {
		forgot(h, "owner", ip).status(http.StatusAccepted, "")
		_ = i
	}
	if emails := h.runJobs(); len(emails) != 3 {
		t.Errorf("sent %d emails, want 3", len(emails))
	}

	// Past five an hour from one address, the address is refused.
	for range 5 {
		forgot(h, "nobody", "203.0.113.9").status(http.StatusAccepted, "")
	}
	forgot(h, "nobody", "203.0.113.9").status(http.StatusTooManyRequests, "rate_limited")
}

func TestResetRejectsBadTokens(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "owner", password: "kopi susu 123", owner: true, email: "owner@maki.test", verified: true})

	reset(h, "not-a-token", "sandi baru panjang").status(http.StatusBadRequest, "token_invalid")

	forgot(h, "owner", "").status(http.StatusAccepted, "")
	token := tokenIn(t, h.runJobs()[0], "/reset-password")
	h.exec(`UPDATE auth_tokens SET expires_at = now() - interval '1 second'`)
	reset(h, token, "sandi baru panjang").status(http.StatusBadRequest, "token_invalid")

	// An older link dies with a newer one's use.
	forgot(h, "owner", "").status(http.StatusAccepted, "")
	forgot(h, "owner", "").status(http.StatusAccepted, "")
	emails := h.runJobs()
	older, newer := tokenIn(t, emails[0], "/reset-password"), tokenIn(t, emails[1], "/reset-password")
	reset(h, newer, "sandi baru panjang").status(http.StatusNoContent, "")
	reset(h, older, "sandi lain lagi").status(http.StatusBadRequest, "token_invalid")
}

func TestResetIsRateLimited(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for range 10 {
		h.do(request{method: http.MethodPost, path: "/api/v1/auth/password/reset", ip: "203.0.113.7", body: map[string]string{
			"token": "guess", "new_password": "sandi baru panjang",
		}}).status(http.StatusBadRequest, "token_invalid")
	}
	h.do(request{method: http.MethodPost, path: "/api/v1/auth/password/reset", ip: "203.0.113.7", body: map[string]string{
		"token": "guess", "new_password": "sandi baru panjang",
	}}).status(http.StatusTooManyRequests, "rate_limited")
}
