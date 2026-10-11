// Package auth signs people in and out and looks after their accounts'
// credentials: passwords, sessions, password reset and email verification.
// It owns the refresh_tokens and auth_tokens tables; the accounts
// themselves belong to the user module, which it reaches through Users.
//
// A session is a family of refresh tokens. Signing in starts one; each
// refresh replaces the family's current token, and presenting a token that
// was already replaced ends the whole family, since only a copy could still
// be using it. Access tokens name the family, and the Authenticate
// middleware asks LoadPrincipal on every request whether it is still alive.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/auth/store"
	"github.com/kervzent/maki-be/internal/outlet"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/i18n"
	"github.com/kervzent/maki-be/internal/platform/id"
	"github.com/kervzent/maki-be/internal/platform/outbox"
	"github.com/kervzent/maki-be/internal/platform/password"
	"github.com/kervzent/maki-be/internal/platform/ratelimit"
	"github.com/kervzent/maki-be/internal/user"
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

// Auditor records Log Action entries inside a transaction; audit.Recorder
// is the real one.
type Auditor interface {
	Record(ctx context.Context, tx pgx.Tx, e audit.Entry) error
}

// Users is the user module, as far as auth needs it: the accounts that sign
// in, and the few changes signing in and its flows make to them. Methods
// that take a pgx.Tx write in auth's transaction.
type Users interface {
	ByUsername(ctx context.Context, username string) (user.User, error)
	ByID(ctx context.Context, userID string) (user.User, error)
	OwnerByLogin(ctx context.Context, login string) (user.User, error)
	OwnerExists(ctx context.Context) (bool, error)
	PrepareOwner(ctx context.Context, in user.OwnerInput) (user.NewOwner, error)
	InsertOwner(ctx context.Context, tx pgx.Tx, o user.NewOwner) error
	RecordLogin(ctx context.Context, tx pgx.Tx, userID string) error
	SetPasswordHash(ctx context.Context, tx pgx.Tx, userID, hash string) error
	ChangePassword(ctx context.Context, tx pgx.Tx, userID, hash string) error
	SetLocale(ctx context.Context, userID string, locale *string) error
	SetVerifiedEmail(ctx context.Context, tx pgx.Tx, userID, email string) error
	Permissions(ctx context.Context, u user.User) (authn.Permissions, error)
}

// Outlets is the outlet module, as far as auth needs it: whether a user's
// outlet is open, and its language.
type Outlets interface {
	Get(ctx context.Context, outletID string) (outlet.Outlet, error)
}

// Client is where a request came from.
type Client struct {
	IP string
}

// Account is a user as GET /me shows them.
type Account struct {
	UserID             string
	Username           string
	Name               string
	Email              *string
	EmailVerified      bool
	PendingEmail       *string
	Locale             *string // their own choice; nil follows the outlet
	Language           string  // what they see, after the fallbacks
	IsOwner            bool
	IsManager          bool
	OutletID           *string
	MustChangePassword bool
	// Permissions are what the user may do, sorted: in their outlet, or for
	// the Owner in any outlet. Clients show what these allow; the server
	// checks them regardless.
	Permissions []string
	// Outlet is the user's outlet; nil for the Owner.
	Outlet *OutletSummary
}

// OutletSummary is the outlet a user belongs to, as GET /me shows it. Its
// JSON tags let meResponse share it with Account.
type OutletSummary struct {
	OutletID string `json:"outlet_id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
	Locale   string `json:"locale"`
}

// Session is what signing in or refreshing hands the client.
type Session struct {
	AccessToken  string
	RefreshToken string
	User         Account
}

// Service implements the auth module's rules.
type Service struct {
	pool    *pgxpool.Pool
	users   Users
	outlets Outlets
	tokens  *authn.Tokens
	hasher  *password.Hasher
	audit   Auditor
	logger  *slog.Logger
	limits  limits
}

// NewService returns the auth service.
func NewService(pool *pgxpool.Pool, users Users, outlets Outlets, tokens *authn.Tokens, hasher *password.Hasher, auditor Auditor, logger *slog.Logger) *Service {
	return &Service{
		pool:    pool,
		users:   users,
		outlets: outlets,
		tokens:  tokens,
		hasher:  hasher,
		audit:   auditor,
		logger:  logger,
		limits:  newLimits(),
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
func (s *Service) Login(ctx context.Context, c Client, username, pw string) (Session, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if err := blocked(s.limits.loginIP, c.IP); err != nil {
		return Session{}, err
	}
	if err := blocked(s.limits.loginAccount, username); err != nil {
		return Session{}, err
	}

	u, err := s.users.ByUsername(ctx, username)
	if errors.Is(err, user.ErrNotFound) {
		// The same work and the same answer as a wrong password, so
		// neither timing nor the response tells which usernames exist.
		if err := s.hasher.Waste(ctx, pw); err != nil {
			return Session{}, err
		}
		return Session{}, s.loginFailed(ctx, c, username, nil, "")
	}
	if err != nil {
		return Session{}, fmt.Errorf("find user: %w", err)
	}

	match, rehash, err := s.hasher.Verify(ctx, pw, u.PasswordHash)
	if err != nil {
		return Session{}, fmt.Errorf("verify password of %s: %w", u.UserID, err)
	}
	if !match {
		return Session{}, s.loginFailed(ctx, c, username, &u, "wrong_password")
	}
	if !u.Active {
		// Only someone who knows the password learns the account exists.
		if err := s.loginFailed(ctx, c, username, &u, "account_disabled"); !errors.Is(err, ErrInvalidCredentials) {
			return Session{}, err
		}
		return Session{}, ErrAccountDisabled
	}
	if _, open, err := outletOf(ctx, s.outlets, u); err != nil {
		return Session{}, err
	} else if !open {
		if err := s.loginFailed(ctx, c, username, &u, "outlet_inactive"); !errors.Is(err, ErrInvalidCredentials) {
			return Session{}, err
		}
		return Session{}, ErrOutletInactive
	}
	s.limits.loginAccount.Reset(username)

	// Hash before the transaction: it takes a tenth of a second.
	var newHash string
	if rehash {
		if newHash, err = s.hasher.Hash(ctx, pw); err != nil {
			return Session{}, err
		}
	}

	var refresh, family string
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if newHash != "" {
			if err := s.users.SetPasswordHash(ctx, tx, u.UserID, newHash); err != nil {
				return err
			}
		}
		if err := s.users.RecordLogin(ctx, tx, u.UserID); err != nil {
			return err
		}
		family = id.New()
		if refresh, _, err = s.issueRefresh(ctx, store.New(tx), u.UserID, family); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action:     "auth.login",
			UserID:     u.UserID,
			OutletID:   deref(u.OutletID),
			TargetType: "session",
			TargetID:   family,
			IP:         c.IP,
		})
	})
	if err != nil {
		return Session{}, fmt.Errorf("start session: %w", err)
	}
	return s.session(ctx, u.UserID, family, refresh)
}

// loginFailed counts a failed sign-in against the address and the username,
// records it, and returns ErrInvalidCredentials. u is nil for an unknown
// username, which isn't recorded: people type passwords into the username
// field by mistake.
func (s *Service) loginFailed(ctx context.Context, c Client, username string, u *user.User, reason string) error {
	s.limits.loginIP.Hit(c.IP)
	s.limits.loginAccount.Hit(username)
	e := audit.Entry{Action: "auth.login_failed", IP: c.IP}
	if u != nil {
		e.UserID, e.OutletID = u.UserID, deref(u.OutletID)
		e.After = map[string]any{"reason": reason}
	}
	if err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error { return s.audit.Record(ctx, tx, e) }); err != nil {
		return fmt.Errorf("record failed sign-in: %w", err)
	}
	return ErrInvalidCredentials
}

// Refresh exchanges a refresh token for a new access token and a new refresh
// token. The old refresh token stops working; presenting it again later
// ends the session.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Session, error) {
	if refreshToken == "" {
		return Session{}, ErrUnauthorized
	}
	var (
		userID, family, refresh string
		ended                   bool // the session was ended; commit that, then refuse
	)
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		rt, err := q.GetRefreshTokenForUpdate(ctx, store.GetRefreshTokenForUpdateParams{
			TokenHash:    hashSecret(refreshToken),
			GraceSeconds: refreshGrace.Seconds(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnauthorized
		}
		if err != nil {
			return err
		}
		switch {
		case rt.Revoked || rt.Expired:
			return ErrUnauthorized
		case rt.Replaced && rt.RecentlyReplaced:
			return ErrRefreshSuperseded
		case rt.Replaced:
			// Only a copy would still hold a token that was already
			// exchanged: the session is compromised, so end it for
			// everyone holding any of its tokens.
			ended = true
			return s.endSession(ctx, tx, rt.UserID, rt.FamilyID, "refresh_token_reused")
		}

		u, err := s.users.ByID(ctx, rt.UserID)
		if errors.Is(err, user.ErrNotFound) || err == nil && !u.Active {
			ended = true
			return s.endSession(ctx, tx, rt.UserID, rt.FamilyID, "account_disabled")
		}
		if err != nil {
			return err
		}
		if _, open, err := outletOf(ctx, s.outlets, u); err != nil {
			return err
		} else if !open {
			ended = true
			return s.endSession(ctx, tx, rt.UserID, rt.FamilyID, "outlet_inactive")
		}

		var newID string
		if refresh, newID, err = s.issueRefresh(ctx, q, rt.UserID, rt.FamilyID); err != nil {
			return err
		}
		userID, family = rt.UserID, rt.FamilyID
		return q.MarkRefreshTokenReplaced(ctx, store.MarkRefreshTokenReplacedParams{
			RefreshTokenID: rt.RefreshTokenID,
			ReplacedBy:     &newID,
		})
	})
	if err != nil {
		return Session{}, err
	}
	if ended {
		return Session{}, ErrUnauthorized
	}
	return s.session(ctx, userID, family, refresh)
}

// endSession revokes every token of a session and records why.
func (s *Service) endSession(ctx context.Context, tx pgx.Tx, userID, family, reason string) error {
	if _, err := store.New(tx).RevokeFamily(ctx, family); err != nil {
		return err
	}
	return s.audit.Record(ctx, tx, audit.Entry{
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
func (s *Service) Logout(ctx context.Context, c Client, refreshToken string, access *authn.Session) error {
	var userID, family string
	if refreshToken != "" {
		row, err := store.New(s.pool).GetRefreshTokenFamily(ctx, hashSecret(refreshToken))
		switch {
		case err == nil:
			userID, family = row.UserID, row.FamilyID
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
	}
	if family == "" && access != nil {
		userID, family = access.UserID, access.SessionID
	}
	if family == "" {
		return nil
	}
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		n, err := store.New(tx).RevokeFamily(ctx, family)
		if err != nil || n == 0 {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "auth.logout", UserID: userID, TargetType: "session", TargetID: family, IP: c.IP,
		})
	})
}

// LoadPrincipal returns the user of a live session, for the Authenticate
// middleware. The session must still have a current refresh token and its
// user must be active; otherwise it is authn.ErrNoSession.
func (s *Service) LoadPrincipal(ctx context.Context, sess authn.Session) (authn.Principal, error) {
	alive, err := store.New(s.pool).SessionAlive(ctx, store.SessionAliveParams{
		UserID:   sess.UserID,
		FamilyID: sess.SessionID,
	})
	if err != nil {
		return authn.Principal{}, fmt.Errorf("load session %s: %w", sess.SessionID, err)
	}
	if !alive {
		return authn.Principal{}, authn.ErrNoSession
	}
	u, err := s.users.ByID(ctx, sess.UserID)
	if errors.Is(err, user.ErrNotFound) || err == nil && !u.Active {
		return authn.Principal{}, authn.ErrNoSession
	}
	if err != nil {
		return authn.Principal{}, fmt.Errorf("load session %s: %w", sess.SessionID, err)
	}
	o, open, err := outletOf(ctx, s.outlets, u)
	if err != nil {
		return authn.Principal{}, err
	}
	if !open {
		// Deactivating an outlet ends its users' access at once, not when
		// their access tokens expire.
		return authn.Principal{}, authn.ErrNoSession
	}
	permissions, err := s.users.Permissions(ctx, u)
	if err != nil {
		return authn.Principal{}, err
	}
	return authn.Principal{
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
func (s *Service) Me(ctx context.Context, userID string) (Account, error) {
	u, err := s.users.ByID(ctx, userID)
	if err != nil {
		return Account{}, fmt.Errorf("load account %s: %w", userID, err)
	}
	pending, err := store.New(s.pool).GetPendingEmail(ctx, userID)
	if err != nil {
		return Account{}, fmt.Errorf("load pending email of %s: %w", userID, err)
	}
	o, _, err := outletOf(ctx, s.outlets, u)
	if err != nil {
		return Account{}, err
	}
	permissions, err := s.users.Permissions(ctx, u)
	if err != nil {
		return Account{}, err
	}
	_, verified := u.VerifiedEmail()
	a := Account{
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
		a.Outlet = &OutletSummary{OutletID: o.OutletID, Code: o.Code, Name: o.Name, Timezone: o.Timezone, Locale: o.Locale}
	}
	return a, nil
}

// SetLocale sets the user's own language; nil follows their outlet's.
func (s *Service) SetLocale(ctx context.Context, p authn.Principal, locale *string) (Account, error) {
	if locale != nil && !i18n.Supported(*locale) {
		return Account{}, httpx.Invalid(map[string]string{"locale": "oneof"})
	}
	if err := s.users.SetLocale(ctx, p.UserID, locale); err != nil {
		return Account{}, fmt.Errorf("set locale: %w", err)
	}
	return s.Me(ctx, p.UserID)
}

// ChangePassword replaces the user's password after checking the current
// one. Every other session of the account ends; this one stays.
func (s *Service) ChangePassword(ctx context.Context, c Client, p authn.Principal, current, next string) error {
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
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.replacePassword(ctx, tx, u, hash, p.SessionID, "auth.password_changed", c)
	})
}

// checkPassword loads the user and checks their current password, counting
// wrong ones against the account.
func (s *Service) checkPassword(ctx context.Context, userID, current string) (user.User, error) {
	if err := blocked(s.limits.password, userID); err != nil {
		return user.User{}, err
	}
	u, err := s.users.ByID(ctx, userID)
	if err != nil {
		return user.User{}, fmt.Errorf("load user %s: %w", userID, err)
	}
	match, _, err := s.hasher.Verify(ctx, current, u.PasswordHash)
	if err != nil {
		return user.User{}, fmt.Errorf("verify password of %s: %w", userID, err)
	}
	if !match {
		s.limits.password.Hit(userID)
		return user.User{}, ErrWrongPassword
	}
	return u, nil
}

// replacePassword stores a new password hash, clears must_change_password,
// ends every session but keepFamily (empty ends them all), records action,
// and emails a notice to a verified address.
func (s *Service) replacePassword(ctx context.Context, tx pgx.Tx, u user.User, hash, keepFamily, action string, c Client) error {
	if err := s.users.ChangePassword(ctx, tx, u.UserID, hash); err != nil {
		return err
	}
	if _, err := store.New(tx).RevokeUserSessions(ctx, store.RevokeUserSessionsParams{UserID: u.UserID, KeepFamilyID: keepFamily}); err != nil {
		return err
	}
	if err := s.audit.Record(ctx, tx, audit.Entry{
		Action: action, UserID: u.UserID, OutletID: deref(u.OutletID), IP: c.IP,
	}); err != nil {
		return err
	}
	if _, verified := u.VerifiedEmail(); verified {
		if _, err := outbox.Enqueue(ctx, tx, JobSendPasswordChanged, jobUser{UserID: u.UserID}); err != nil {
			return err
		}
	}
	return nil
}

// ForgotPassword emails the Owner a reset link, if login is the Owner's
// username or verified email. It answers the same either way.
func (s *Service) ForgotPassword(ctx context.Context, c Client, login string) error {
	if err := blocked(s.limits.forgotIP, c.IP); err != nil {
		return err
	}
	s.limits.forgotIP.Hit(c.IP)

	owner, err := s.users.OwnerByLogin(ctx, strings.ToLower(strings.TrimSpace(login)))
	if errors.Is(err, user.ErrNotFound) {
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

	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := outbox.Enqueue(ctx, tx, JobSendPasswordReset, jobUser{UserID: owner.UserID}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{Action: "auth.password_reset_requested", UserID: owner.UserID, IP: c.IP})
	})
}

// ResetPassword sets a new password with the token from a reset link. Every
// session of the account ends.
func (s *Service) ResetPassword(ctx context.Context, c Client, token, next string) error {
	if err := blocked(s.limits.tokenIP, c.IP); err != nil {
		return err
	}
	params := store.GetAuthTokenForUpdateParams{TokenHash: hashSecret(token), Purpose: "password_reset"}

	// Find the account first, outside a transaction, so the new password is
	// checked and hashed before any row is locked.
	t, err := store.New(s.pool).GetAuthTokenForUpdate(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		s.limits.tokenIP.Hit(c.IP)
		return ErrTokenInvalid
	}
	if err != nil {
		return fmt.Errorf("find reset token: %w", err)
	}
	u, err := s.users.ByID(ctx, t.UserID)
	if errors.Is(err, user.ErrNotFound) || err == nil && !u.Active {
		return ErrTokenInvalid
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

	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		// Again, locked: the link may have been used in the meantime.
		if _, err := q.GetAuthTokenForUpdate(ctx, params); errors.Is(err, pgx.ErrNoRows) {
			return ErrTokenInvalid
		} else if err != nil {
			return err
		}
		if err := q.UseAuthTokens(ctx, store.UseAuthTokensParams{UserID: u.UserID, Purpose: "password_reset"}); err != nil {
			return err
		}
		return s.replacePassword(ctx, tx, u, hash, "", "auth.password_reset", c)
	})
}

// ChangeEmail starts changing the user's email address: a link goes to the
// new address, a notice to the verified current one. The current address
// stays until the new one is confirmed.
func (s *Service) ChangeEmail(ctx context.Context, c Client, p authn.Principal, email, current string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if !user.ValidEmail(email) {
		return httpx.Invalid(map[string]string{"email": "email"})
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
		return ErrEmailUnchanged
	}
	s.limits.email.Hit(p.UserID)

	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := outbox.Enqueue(ctx, tx, JobSendEmailVerification, jobEmail{UserID: u.UserID, Email: email}); err != nil {
			return err
		}
		if verified {
			if _, err := outbox.Enqueue(ctx, tx, JobSendEmailChangeNotice, jobEmailChange{
				UserID: u.UserID, OldEmail: old, NewEmail: email,
			}); err != nil {
				return err
			}
		}
		return s.audit.Record(ctx, tx, audit.Entry{
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
func (s *Service) VerifyEmail(ctx context.Context, c Client, token string) error {
	if err := blocked(s.limits.tokenIP, c.IP); err != nil {
		return err
	}
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		t, err := q.GetAuthTokenForUpdate(ctx, store.GetAuthTokenForUpdateParams{
			TokenHash: hashSecret(token), Purpose: "email_verify",
		})
		if errors.Is(err, pgx.ErrNoRows) {
			s.limits.tokenIP.Hit(c.IP)
			return ErrTokenInvalid
		}
		if err != nil {
			return err
		}
		if t.Email == nil {
			return fmt.Errorf("email_verify token %s has no address", t.AuthTokenID)
		}
		if err := s.users.SetVerifiedEmail(ctx, tx, t.UserID, *t.Email); err != nil {
			return err
		}
		if err := q.UseAuthTokens(ctx, store.UseAuthTokensParams{UserID: t.UserID, Purpose: "email_verify"}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "auth.email_verified", UserID: t.UserID, After: map[string]any{"email": t.Email}, IP: c.IP,
		})
	})
}

// issueRefresh stores a new refresh token of the family and returns it and
// its row's ID.
func (s *Service) issueRefresh(ctx context.Context, q *store.Queries, userID, family string) (token, tokenID string, err error) {
	token, hash := newSecret()
	tokenID = id.New()
	err = q.InsertRefreshToken(ctx, store.InsertRefreshTokenParams{
		RefreshTokenID: tokenID,
		UserID:         userID,
		FamilyID:       family,
		TokenHash:      hash,
		TtlSeconds:     RefreshTokenTTL.Seconds(),
	})
	return token, tokenID, err
}

// session completes a Session: a fresh access token and the account.
func (s *Service) session(ctx context.Context, userID, family, refresh string) (Session, error) {
	access, _, err := s.tokens.Issue(authn.Session{UserID: userID, SessionID: family})
	if err != nil {
		return Session{}, err
	}
	account, err := s.Me(ctx, userID)
	if err != nil {
		return Session{}, err
	}
	return Session{AccessToken: access, RefreshToken: refresh, User: account}, nil
}

// language is the language a user sees: their own choice, else their
// outlet's, else Indonesian.
func language(own *string, outletLocale string) string {
	for _, l := range []string{deref(own), outletLocale} {
		if l != "" && i18n.Supported(l) {
			return l
		}
	}
	return i18n.Default
}

// outletOf returns the outlet u belongs to, and whether they may use it.
// The Owner, who belongs to none, always may; everyone else only while
// their outlet is active. A deleted outlet counts as closed.
func outletOf(ctx context.Context, outlets Outlets, u user.User) (outlet.Outlet, bool, error) {
	if u.OutletID == nil {
		return outlet.Outlet{}, true, nil
	}
	o, err := outlets.Get(ctx, *u.OutletID)
	if errors.Is(err, outlet.ErrNotFound) {
		return outlet.Outlet{}, false, nil
	}
	if err != nil {
		return outlet.Outlet{}, false, fmt.Errorf("find outlet of %s: %w", u.UserID, err)
	}
	return o, o.Active, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
