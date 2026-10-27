package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/i18n"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/utils/jwt"
	"github.com/kervzent/maki-be/internal/utils/outbox"
	"github.com/kervzent/maki-be/internal/utils/password"
	"github.com/kervzent/maki-be/internal/utils/ratelimit"
	"github.com/kervzent/maki-be/internal/validations"
)

// Lifetimes.
const (
	AccessTokenTTL = 15 * time.Minute
	// RefreshTokenTTL counts from the token's last use: a session used at
	// least once a month never has to sign in again.
	RefreshTokenTTL = 30 * 24 * time.Hour
	ResetLinkTTL    = 30 * time.Minute
	VerifyLinkTTL   = 24 * time.Hour

	// A refresh token replaced less than this long ago gets
	// refresh_superseded instead of ending the session: two browser tabs
	// refreshing at once both send the old cookie.
	refreshGrace = 30 * time.Second
)

// AuthService signs people in and out and looks after their accounts'
// credentials: passwords, sessions, password reset and email verification.
// The accounts themselves are UserService's.
//
// A session is a family of refresh tokens. Signing in starts one; each
// refresh replaces the family's current token, and presenting a token that
// was already replaced ends the whole family, since only a copy could still
// be using it. Access tokens name the family, and the Authenticate
// middleware asks LoadPrincipal on every request whether it is still alive.
type AuthService struct {
	db       *config.Database
	sessions *repositories.SessionRepository
	links    *AuthTokenService
	users    *UserService
	outlets  *OutletService
	tokens   *jwt.Tokens
	hasher   *password.Hasher
	audit    *AuditService
	limits   limits
}

// NewAuthService returns the auth service. tokens issues the access tokens;
// hasher checks and hashes the passwords.
func NewAuthService(db *config.Database, sessions *repositories.SessionRepository, links *AuthTokenService,
	users *UserService, outlets *OutletService, tokens *jwt.Tokens, hasher *password.Hasher, audit *AuditService,
) *AuthService {
	return &AuthService{
		db:       db,
		sessions: sessions,
		links:    links,
		users:    users,
		outlets:  outlets,
		tokens:   tokens,
		hasher:   hasher,
		audit:    audit,
		limits:   newLimits(),
	}
}

// limits are the rate limits of the endpoints an attacker would hammer,
// keyed by client IP and by account.
type limits struct {
	loginIP      *ratelimit.Limiter // failed sign-ins from one address
	loginAccount *ratelimit.Limiter // failed sign-ins to one username
	forgotIP     *ratelimit.Limiter // reset requests from one address
	forgotUser   *ratelimit.Limiter // reset emails to one account
	tokenIP      *ratelimit.Limiter // bad link tokens from one address
	password     *ratelimit.Limiter // wrong current passwords for one account
	email        *ratelimit.Limiter // email changes for one account
}

func newLimits() limits {
	return limits{
		loginIP:      ratelimit.New(20, 15*time.Minute),
		loginAccount: ratelimit.New(5, 15*time.Minute),
		forgotIP:     ratelimit.New(5, time.Hour),
		forgotUser:   ratelimit.New(3, time.Hour),
		tokenIP:      ratelimit.New(10, time.Hour),
		password:     ratelimit.New(5, 15*time.Minute),
		email:        ratelimit.New(5, time.Hour),
	}
}

// blocked returns rate_limited when l blocks key.
func blocked(l *ratelimit.Limiter, key string) error {
	if ok, wait := l.Check(key); !ok {
		return ratelimit.Limited(wait)
	}
	return nil
}

// Login checks a username and password and starts a session.
func (s *AuthService) Login(ctx context.Context, c models.Client, username, pw string) (models.AuthSession, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if err := blocked(s.limits.loginIP, c.IP); err != nil {
		return models.AuthSession{}, err
	}
	if err := blocked(s.limits.loginAccount, username); err != nil {
		return models.AuthSession{}, err
	}

	u, err := s.users.ByUsername(ctx, username)
	if errors.Is(err, apperror.ErrNoAccount) {
		// The same work and the same answer as a wrong password, so
		// neither timing nor the response tells which usernames exist.
		if err := s.hasher.Waste(ctx, pw); err != nil {
			return models.AuthSession{}, err
		}
		return models.AuthSession{}, s.loginFailed(ctx, c, username, nil, "")
	}
	if err != nil {
		return models.AuthSession{}, fmt.Errorf("find user: %w", err)
	}

	match, rehash, err := s.hasher.Verify(ctx, pw, u.PasswordHash)
	if err != nil {
		return models.AuthSession{}, fmt.Errorf("verify password of %s: %w", u.UserID, err)
	}
	if !match {
		return models.AuthSession{}, s.loginFailed(ctx, c, username, &u, "wrong_password")
	}
	if !u.Active {
		// Only someone who knows the password learns the account exists.
		if err := s.loginFailed(ctx, c, username, &u, "account_disabled"); !errors.Is(err, apperror.ErrInvalidCredentials) {
			return models.AuthSession{}, err
		}
		return models.AuthSession{}, apperror.ErrAccountDisabled
	}
	if _, open, err := outletOf(ctx, s.outlets, u); err != nil {
		return models.AuthSession{}, err
	} else if !open {
		if err := s.loginFailed(ctx, c, username, &u, "outlet_inactive"); !errors.Is(err, apperror.ErrInvalidCredentials) {
			return models.AuthSession{}, err
		}
		return models.AuthSession{}, apperror.ErrOutletInactive
	}
	s.limits.loginAccount.Reset(username)

	// Hash before the transaction: it takes a tenth of a second.
	var newHash string
	if rehash {
		if newHash, err = s.hasher.Hash(ctx, pw); err != nil {
			return models.AuthSession{}, err
		}
	}

	var refresh, family string
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		if newHash != "" {
			if err := s.users.SetPasswordHash(ctx, u.UserID, newHash); err != nil {
				return err
			}
		}
		if err := s.users.RecordLogin(ctx, u.UserID); err != nil {
			return err
		}
		family = id.New()
		token, _, err := s.issueRefresh(ctx, u.UserID, family)
		if err != nil {
			return err
		}
		refresh = token
		return s.audit.Record(ctx, models.AuditEntry{
			Action:     "auth.login",
			UserID:     u.UserID,
			OutletID:   deref(u.OutletID),
			TargetType: "session",
			TargetID:   family,
			IP:         c.IP,
		})
	})
	if err != nil {
		return models.AuthSession{}, fmt.Errorf("start session: %w", err)
	}
	return s.session(ctx, u.UserID, family, refresh)
}

// loginFailed counts a failed sign-in against the address and the username,
// records it, and returns ErrInvalidCredentials. u is nil for an unknown
// username, which isn't recorded: people type passwords into the username
// field by mistake.
func (s *AuthService) loginFailed(ctx context.Context, c models.Client, username string, u *models.User, reason string) error {
	s.limits.loginIP.Hit(c.IP)
	s.limits.loginAccount.Hit(username)
	e := models.AuditEntry{Action: "auth.login_failed", IP: c.IP}
	if u != nil {
		e.UserID, e.OutletID = u.UserID, deref(u.OutletID)
		e.After = map[string]any{"reason": reason}
	}
	if err := s.audit.Record(ctx, e); err != nil {
		return fmt.Errorf("record failed sign-in: %w", err)
	}
	return apperror.ErrInvalidCredentials
}

// Refresh exchanges a refresh token for a new access token and a new refresh
// token. The old refresh token stops working; presenting it again later
// ends the session.
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (models.AuthSession, error) {
	if refreshToken == "" {
		return models.AuthSession{}, apperror.ErrUnauthorized
	}
	var (
		userID, family, refresh string
		ended                   bool // the session was ended; commit that, then refuse
	)
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		rt, err := s.sessions.LockForRefresh(ctx, hashSecret(refreshToken), refreshGrace)
		if err != nil {
			return err
		}
		switch {
		case rt.Revoked || rt.Expired:
			return apperror.ErrUnauthorized
		case rt.Replaced && rt.RecentlyReplaced:
			return apperror.ErrRefreshSuperseded
		case rt.Replaced:
			// Only a copy would still hold a token that was already
			// exchanged: the session is compromised, so end it for
			// everyone holding any of its tokens.
			ended = true
			return s.endSession(ctx, rt.UserID, rt.FamilyID, "refresh_token_reused")
		}

		u, err := s.users.ByID(ctx, rt.UserID)
		if errors.Is(err, apperror.ErrNoAccount) || err == nil && !u.Active {
			ended = true
			return s.endSession(ctx, rt.UserID, rt.FamilyID, "account_disabled")
		}
		if err != nil {
			return err
		}
		if _, open, err := outletOf(ctx, s.outlets, u); err != nil {
			return err
		} else if !open {
			ended = true
			return s.endSession(ctx, rt.UserID, rt.FamilyID, "outlet_inactive")
		}

		token, newID, err := s.issueRefresh(ctx, rt.UserID, rt.FamilyID)
		if err != nil {
			return err
		}
		userID, family, refresh = rt.UserID, rt.FamilyID, token
		return s.sessions.MarkReplaced(ctx, rt.RefreshTokenID, newID)
	})
	if err != nil {
		return models.AuthSession{}, err
	}
	if ended {
		return models.AuthSession{}, apperror.ErrUnauthorized
	}
	return s.session(ctx, userID, family, refresh)
}

// endSession revokes every token of a session and records why.
func (s *AuthService) endSession(ctx context.Context, userID, family, reason string) error {
	if _, err := s.sessions.RevokeFamily(ctx, family); err != nil {
		return err
	}
	return s.audit.Record(ctx, models.AuditEntry{
		Action:     "auth.session_revoked",
		UserID:     userID,
		TargetType: "session",
		TargetID:   family,
		After:      map[string]any{"reason": reason},
	})
}

// Logout ends the session named by refreshToken or, without one, by the
// access token's session. Unknown or already ended sessions are fine:
// signing out twice isn't an error.
func (s *AuthService) Logout(ctx context.Context, c models.Client, refreshToken string, access *jwt.Session) error {
	var userID, family string
	if refreshToken != "" {
		owner, session, err := s.sessions.FamilyOf(ctx, hashSecret(refreshToken))
		switch {
		case err == nil:
			userID, family = owner, session
		case !errors.Is(err, apperror.ErrUnauthorized):
			return err
		}
	}
	if family == "" && access != nil {
		userID, family = access.UserID, access.SessionID
	}
	if family == "" {
		return nil
	}
	return s.db.InTx(ctx, func(ctx context.Context) error {
		n, err := s.sessions.RevokeFamily(ctx, family)
		if err != nil || n == 0 {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "auth.logout", UserID: userID, TargetType: "session", TargetID: family, IP: c.IP,
		})
	})
}

// LoadPrincipal returns the user of a live session, for the Authenticate
// middleware. The session must still have a current refresh token and its
// user must be active; otherwise it is models.ErrNoSession.
func (s *AuthService) LoadPrincipal(ctx context.Context, sess jwt.Session) (models.Principal, error) {
	alive, err := s.sessions.Alive(ctx, sess.UserID, sess.SessionID)
	if err != nil {
		return models.Principal{}, fmt.Errorf("load session %s: %w", sess.SessionID, err)
	}
	if !alive {
		return models.Principal{}, models.ErrNoSession
	}
	u, err := s.users.ByID(ctx, sess.UserID)
	if errors.Is(err, apperror.ErrNoAccount) || err == nil && !u.Active {
		return models.Principal{}, models.ErrNoSession
	}
	if err != nil {
		return models.Principal{}, fmt.Errorf("load session %s: %w", sess.SessionID, err)
	}
	o, open, err := outletOf(ctx, s.outlets, u)
	if err != nil {
		return models.Principal{}, err
	}
	if !open {
		// Deactivating an outlet ends its users' access at once, not when
		// their access tokens expire.
		return models.Principal{}, models.ErrNoSession
	}
	permissions, err := s.users.Permissions(ctx, u)
	if err != nil {
		return models.Principal{}, err
	}
	return models.Principal{
		UserID:             u.UserID,
		SessionID:          sess.SessionID,
		Username:           u.Username,
		Name:               u.Name,
		IsOwner:            u.IsOwner,
		IsManager:          u.IsManager,
		OutletID:           deref(u.OutletID),
		Locale:             language(u.Locale, o.Locale),
		MustChangePassword: u.MustChangePassword,
		Permissions:        permissions,
	}, nil
}

// Me returns the user's account.
func (s *AuthService) Me(ctx context.Context, userID string) (models.Account, error) {
	u, err := s.users.ByID(ctx, userID)
	if err != nil {
		return models.Account{}, fmt.Errorf("load account %s: %w", userID, err)
	}
	pending, err := s.links.PendingEmail(ctx, userID)
	if err != nil {
		return models.Account{}, err
	}
	o, _, err := outletOf(ctx, s.outlets, u)
	if err != nil {
		return models.Account{}, err
	}
	permissions, err := s.users.Permissions(ctx, u)
	if err != nil {
		return models.Account{}, err
	}
	_, verified := u.VerifiedEmail()
	a := models.Account{
		UserID:             u.UserID,
		Username:           u.Username,
		Name:               u.Name,
		Email:              u.Email,
		EmailVerified:      verified,
		Locale:             u.Locale,
		Language:           language(u.Locale, o.Locale),
		IsOwner:            u.IsOwner,
		IsManager:          u.IsManager,
		OutletID:           u.OutletID,
		MustChangePassword: u.MustChangePassword,
		Permissions:        permissions.List(),
	}
	if pending != "" {
		a.PendingEmail = &pending
	}
	if o.OutletID != "" {
		a.Outlet = &models.OutletSummary{OutletID: o.OutletID, Code: o.Code, Name: o.Name, Timezone: o.Timezone, Locale: o.Locale}
	}
	return a, nil
}

// SetLocale sets the user's own language; nil follows their outlet's.
func (s *AuthService) SetLocale(ctx context.Context, p models.Principal, locale *string) (models.Account, error) {
	if locale != nil && !i18n.Supported(*locale) {
		return models.Account{}, apperror.Invalid(map[string]string{"locale": "oneof"})
	}
	if err := s.users.SetLocale(ctx, p.UserID, locale); err != nil {
		return models.Account{}, fmt.Errorf("set locale: %w", err)
	}
	return s.Me(ctx, p.UserID)
}

// ChangePassword replaces the user's password after checking the current
// one. Every other session of the account ends; this one stays.
func (s *AuthService) ChangePassword(ctx context.Context, c models.Client, p models.Principal, current, next string) error {
	u, err := s.checkPassword(ctx, p.UserID, current)
	if err != nil {
		return err
	}
	if err := password.Check(next, u.Username); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, next)
	if err != nil {
		return err
	}
	return s.db.InTx(ctx, func(ctx context.Context) error {
		return s.replacePassword(ctx, u, hash, p.SessionID, "auth.password_changed", c)
	})
}

// checkPassword loads the user and checks their current password, counting
// wrong ones against the account.
func (s *AuthService) checkPassword(ctx context.Context, userID, current string) (models.User, error) {
	if err := blocked(s.limits.password, userID); err != nil {
		return models.User{}, err
	}
	u, err := s.users.ByID(ctx, userID)
	if err != nil {
		return models.User{}, fmt.Errorf("load user %s: %w", userID, err)
	}
	match, _, err := s.hasher.Verify(ctx, current, u.PasswordHash)
	if err != nil {
		return models.User{}, fmt.Errorf("verify password of %s: %w", userID, err)
	}
	if !match {
		s.limits.password.Hit(userID)
		return models.User{}, apperror.ErrWrongPassword
	}
	return u, nil
}

// replacePassword stores a new password hash, clears must_change_password,
// ends every session but keepFamily (empty ends them all), records action,
// and emails a notice to a verified address, in the transaction ctx
// carries.
func (s *AuthService) replacePassword(ctx context.Context, u models.User, hash, keepFamily, action string, c models.Client) error {
	if err := s.users.ChangePassword(ctx, u.UserID, hash); err != nil {
		return err
	}
	if err := s.sessions.RevokeUserSessions(ctx, u.UserID, keepFamily); err != nil {
		return err
	}
	if err := s.audit.Record(ctx, models.AuditEntry{
		Action: action, UserID: u.UserID, OutletID: deref(u.OutletID), IP: c.IP,
	}); err != nil {
		return err
	}
	if _, verified := u.VerifiedEmail(); verified {
		if err := s.enqueue(ctx, models.JobSendPasswordChanged, models.JobUser{UserID: u.UserID}); err != nil {
			return err
		}
	}
	return nil
}

// ForgotPassword emails the Owner a reset link, if login is the Owner's
// username or verified email. It answers the same either way.
func (s *AuthService) ForgotPassword(ctx context.Context, c models.Client, login string) error {
	if err := blocked(s.limits.forgotIP, c.IP); err != nil {
		return err
	}
	s.limits.forgotIP.Hit(c.IP)

	owner, err := s.users.OwnerByLogin(ctx, strings.ToLower(strings.TrimSpace(login)))
	if errors.Is(err, apperror.ErrNoAccount) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find owner: %w", err)
	}
	if _, verified := owner.VerifiedEmail(); !verified {
		return nil
	}
	// Past its own limit an account is skipped quietly: answering
	// differently would confirm that it exists.
	if ok, _ := s.limits.forgotUser.Check(owner.UserID); !ok {
		return nil
	}
	s.limits.forgotUser.Hit(owner.UserID)

	return s.db.InTx(ctx, func(ctx context.Context) error {
		if err := s.enqueue(ctx, models.JobSendPasswordReset, models.JobUser{UserID: owner.UserID}); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{Action: "auth.password_reset_requested", UserID: owner.UserID, IP: c.IP})
	})
}

// ResetPassword sets a new password with the token from a reset link. Every
// session of the account ends.
func (s *AuthService) ResetPassword(ctx context.Context, c models.Client, token, next string) error {
	if err := blocked(s.limits.tokenIP, c.IP); err != nil {
		return err
	}

	// Find the account first, outside a transaction, so the new password is
	// checked and hashed before any row is locked.
	t, err := s.links.lock(ctx, token, purposePasswordReset)
	if errors.Is(err, apperror.ErrTokenInvalid) {
		s.limits.tokenIP.Hit(c.IP)
		return err
	}
	if err != nil {
		return err
	}
	u, err := s.users.ByID(ctx, t.UserID)
	if errors.Is(err, apperror.ErrNoAccount) || err == nil && !u.Active {
		return apperror.ErrTokenInvalid
	}
	if err != nil {
		return fmt.Errorf("load user %s: %w", t.UserID, err)
	}
	if err := password.Check(next, u.Username); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, next)
	if err != nil {
		return err
	}

	return s.db.InTx(ctx, func(ctx context.Context) error {
		// Again, locked: the link may have been used in the meantime.
		if _, err := s.links.lock(ctx, token, purposePasswordReset); err != nil {
			return err
		}
		if err := s.links.useAll(ctx, u.UserID, purposePasswordReset); err != nil {
			return err
		}
		return s.replacePassword(ctx, u, hash, "", "auth.password_reset", c)
	})
}

// ChangeEmail starts changing the user's email address: a link goes to the
// new address, a notice to the verified current one. The current address
// stays until the new one is confirmed.
func (s *AuthService) ChangeEmail(ctx context.Context, c models.Client, p models.Principal, email, current string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if !validations.ValidEmail(email) {
		return apperror.Invalid(map[string]string{"email": "email"})
	}
	if err := blocked(s.limits.email, p.UserID); err != nil {
		return err
	}
	u, err := s.checkPassword(ctx, p.UserID, current)
	if err != nil {
		return err
	}
	old, verified := u.VerifiedEmail()
	if verified && old == email {
		return apperror.ErrEmailUnchanged
	}
	s.limits.email.Hit(p.UserID)

	return s.db.InTx(ctx, func(ctx context.Context) error {
		if err := s.enqueue(ctx, models.JobSendEmailVerification, models.JobEmail{UserID: u.UserID, Email: email}); err != nil {
			return err
		}
		if verified {
			if err := s.enqueue(ctx, models.JobSendEmailChangeNotice, models.JobEmailChange{
				UserID: u.UserID, OldEmail: old, NewEmail: email,
			}); err != nil {
				return err
			}
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action:   "auth.email_change_requested",
			UserID:   u.UserID,
			OutletID: deref(u.OutletID),
			Before:   map[string]any{"email": u.Email},
			After:    map[string]any{"email": email},
			IP:       c.IP,
		})
	})
}

// VerifyEmail confirms an address with the token from a verification link,
// making it the account's email.
func (s *AuthService) VerifyEmail(ctx context.Context, c models.Client, token string) error {
	if err := blocked(s.limits.tokenIP, c.IP); err != nil {
		return err
	}
	return s.db.InTx(ctx, func(ctx context.Context) error {
		t, err := s.links.lock(ctx, token, purposeEmailVerify)
		if errors.Is(err, apperror.ErrTokenInvalid) {
			s.limits.tokenIP.Hit(c.IP)
			return err
		}
		if err != nil {
			return err
		}
		if t.Email == nil {
			return fmt.Errorf("email_verify token %s has no address", t.AuthTokenID)
		}
		if err := s.users.SetVerifiedEmail(ctx, t.UserID, *t.Email); err != nil {
			return err
		}
		if err := s.links.useAll(ctx, t.UserID, purposeEmailVerify); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "auth.email_verified", UserID: t.UserID, After: map[string]any{"email": t.Email}, IP: c.IP,
		})
	})
}

// OwnerExists reports whether the Owner account exists.
func (s *AuthService) OwnerExists(ctx context.Context) (bool, error) {
	return s.users.OwnerExists(ctx)
}

// CreateOwner creates the one Owner account, records it in the Log Action,
// and emails a link to confirm its address — which password reset needs. It
// returns the Owner's user ID. The database refuses a second Owner, even
// from a concurrent run.
func (s *AuthService) CreateOwner(ctx context.Context, in models.OwnerInput) (string, error) {
	owner, err := s.users.PrepareOwner(ctx, in)
	if err != nil {
		return "", err
	}
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		if err := s.users.InsertOwner(ctx, owner); err != nil {
			return err
		}
		if err := s.enqueue(ctx, models.JobSendEmailVerification, models.JobEmail{UserID: owner.UserID, Email: owner.Email}); err != nil {
			return err
		}
		// Run on the server, so there is no client address.
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "auth.owner_created", UserID: owner.UserID, TargetType: "user", TargetID: owner.UserID,
			After: map[string]any{"username": owner.Username, "email": owner.Email},
		})
	})
	if err != nil {
		return "", err
	}
	return owner.UserID, nil
}

// enqueue adds an outbox job in the transaction ctx carries, so the job
// exists only if the change it is about commits.
func (s *AuthService) enqueue(ctx context.Context, kind string, payload any) error {
	_, err := outbox.Enqueue(ctx, s.db.Conn(ctx), kind, payload)
	return err
}

// issueRefresh stores a new refresh token of the family and returns it and
// its row's ID.
func (s *AuthService) issueRefresh(ctx context.Context, userID, family string) (token, tokenID string, err error) {
	token, hash := newSecret()
	tokenID = id.New()
	err = s.sessions.Insert(ctx, tokenID, userID, family, hash, RefreshTokenTTL)
	return token, tokenID, err
}

// session completes an AuthSession: a fresh access token and the account.
func (s *AuthService) session(ctx context.Context, userID, family, refresh string) (models.AuthSession, error) {
	access, _, err := s.tokens.Issue(jwt.Session{UserID: userID, SessionID: family})
	if err != nil {
		return models.AuthSession{}, err
	}
	account, err := s.Me(ctx, userID)
	if err != nil {
		return models.AuthSession{}, err
	}
	return models.AuthSession{AccessToken: access, RefreshToken: refresh, User: account}, nil
}

// newSecret returns a random token — a refresh token, a device's, or the
// token in an emailed link — and the hash stored in its place. The token
// itself is never stored.
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

// deref is s, or "" for nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
