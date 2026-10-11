package auth

import (
	"net/http"
	"testing"
)

func changeEmail(h *harness, access, email, current string) response {
	return h.do(request{method: http.MethodPut, path: "/api/v1/me/email", bearer: access, body: map[string]string{
		"email": email, "current_password": current,
	}})
}

func verify(h *harness, token string) response {
	return h.do(request{method: http.MethodPost, path: "/api/v1/auth/email/verify", body: map[string]string{"token": token}})
}

func TestChangeEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "owner", password: "kopi susu 123", owner: true, email: "old@maki.test", verified: true})
	access, _ := h.login("owner", "kopi susu 123", "web")

	changeEmail(h, access, "new@maki.test", "salah").status(http.StatusUnprocessableEntity, "wrong_password")
	r := changeEmail(h, access, "not an address", "kopi susu 123").status(http.StatusUnprocessableEntity, "validation_failed")
	if f, _ := r.errorDetails()["fields"].(map[string]any); f["email"] != "email" {
		t.Errorf("fields = %v", r.errorDetails())
	}
	changeEmail(h, access, " OLD@maki.test ", "kopi susu 123").status(http.StatusUnprocessableEntity, "email_unchanged")

	changeEmail(h, access, "New@Maki.test", "kopi susu 123").status(http.StatusAccepted, "")
	emails := h.runJobs()
	if len(emails) != 2 {
		t.Fatalf("sent %d emails, want a link to the new address and a notice to the old", len(emails))
	}
	byTo := map[string]int{emails[0].To: 0, emails[1].To: 1}
	link, notice := emails[byTo["new@maki.test"]], emails[byTo["old@maki.test"]]
	if link.To != "new@maki.test" || notice.To != "old@maki.test" {
		t.Fatalf("emails went to %s and %s", emails[0].To, emails[1].To)
	}
	token := tokenIn(t, link, "/verify-email")

	// Until confirmed, the old address stays and the new one waits.
	me := h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).status(http.StatusOK, "").me()
	if *me.Email != "old@maki.test" || !me.EmailVerified || me.PendingEmail == nil || *me.PendingEmail != "new@maki.test" {
		t.Errorf("before confirming: email %v (verified %v), pending %v", *me.Email, me.EmailVerified, me.PendingEmail)
	}

	verify(h, token).status(http.StatusNoContent, "")
	me = h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).status(http.StatusOK, "").me()
	if *me.Email != "new@maki.test" || !me.EmailVerified || me.PendingEmail != nil {
		t.Errorf("after confirming: email %v (verified %v), pending %v", *me.Email, me.EmailVerified, me.PendingEmail)
	}
	verify(h, token).status(http.StatusBadRequest, "token_invalid")

	// The reset link now goes to the new address.
	forgot(h, "new@maki.test", "").status(http.StatusAccepted, "")
	if emails := h.runJobs(); len(emails) != 1 || emails[0].To != "new@maki.test" {
		t.Errorf("reset email went to %+v, want new@maki.test", emails)
	}
}

func TestOnlyTheLatestEmailChangeCanBeConfirmed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"}) // no email yet
	access, _ := h.login("kasir1", "kopi susu 123", "web")

	changeEmail(h, access, "first@maki.test", "kopi susu 123").status(http.StatusAccepted, "")
	first := h.runJobs()
	changeEmail(h, access, "second@maki.test", "kopi susu 123").status(http.StatusAccepted, "")
	second := h.runJobs()
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("emails: %d then %d, want one link each and no notice (there was no old address)", len(first), len(second))
	}

	verify(h, tokenIn(t, first[0], "/verify-email")).status(http.StatusBadRequest, "token_invalid")
	verify(h, tokenIn(t, second[0], "/verify-email")).status(http.StatusNoContent, "")

	me := h.do(request{method: http.MethodGet, path: "/api/v1/me", bearer: access}).status(http.StatusOK, "").me()
	if me.Email == nil || *me.Email != "second@maki.test" {
		t.Errorf("email = %v, want second@maki.test", me.Email)
	}
}

func TestVerifyEmailRejectsBadTokens(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	access, _ := h.login("kasir1", "kopi susu 123", "web")

	verify(h, "not-a-token").status(http.StatusBadRequest, "token_invalid")

	changeEmail(h, access, "kasir1@maki.test", "kopi susu 123").status(http.StatusAccepted, "")
	token := tokenIn(t, h.runJobs()[0], "/verify-email")
	h.exec(`UPDATE auth_tokens SET expires_at = now() - interval '1 second'`)
	verify(h, token).status(http.StatusBadRequest, "token_invalid")

	// A reset token isn't a verification token, and the other way round.
	h.exec(`UPDATE auth_tokens SET purpose = 'password_reset', email = NULL, expires_at = now() + interval '1 hour'`)
	verify(h, token).status(http.StatusBadRequest, "token_invalid")
}

func TestChangeEmailIsRateLimited(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.createUser(testUser{username: "kasir1", password: "kopi susu 123"})
	access, _ := h.login("kasir1", "kopi susu 123", "web")

	for i := range 5 {
		changeEmail(h, access, "kasir"+string(rune('a'+i))+"@maki.test", "kopi susu 123").status(http.StatusAccepted, "")
	}
	changeEmail(h, access, "kasirz@maki.test", "kopi susu 123").status(http.StatusTooManyRequests, "rate_limited")
}
