package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/auth/store"
	"github.com/kervzent/maki-be/internal/platform/i18n"
	"github.com/kervzent/maki-be/internal/platform/id"
	"github.com/kervzent/maki-be/internal/platform/mail"
	"github.com/kervzent/maki-be/internal/platform/outbox"
	"github.com/kervzent/maki-be/internal/user"
)

// The outbox jobs this module enqueues and handles.
//
//nolint:gosec // job kind names that mention passwords, not credentials
const (
	JobSendPasswordReset     = "auth.send_password_reset"
	JobSendEmailVerification = "auth.send_email_verification"
	JobSendPasswordChanged   = "auth.send_password_changed"
	JobSendEmailChangeNotice = "auth.send_email_change_notice"
)

type jobUser struct {
	UserID string `json:"user_id"`
}

type jobEmail struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

type jobEmailChange struct {
	UserID   string `json:"user_id"`
	OldEmail string `json:"old_email"`
	NewEmail string `json:"new_email"`
}

// Mailer sends the rendered emails: mail.SMTP, or mail.Log in development.
type Mailer interface {
	Send(ctx context.Context, msg mail.Message) error
}

// Accounts is the part of the user module the email jobs read: who an email
// goes to, and in which language.
type Accounts interface {
	ByID(ctx context.Context, userID string) (user.User, error)
}

// Jobs sends the auth module's emails from the worker, in each recipient's
// language.
//
// The tokens in reset and verification links are made here, as each email
// goes out, so a plain token only ever exists in the email itself — never in
// the database, the outbox or a production log. A retried job makes a new
// token; the unsent ones are never seen by anyone and simply expire.
type Jobs struct {
	pool    *pgxpool.Pool
	users   Accounts
	outlets Outlets
	mailer  Mailer
	webURL  string
}

// NewJobs returns the job handlers; webBaseURL is the web app, where the
// links in the emails lead.
func NewJobs(pool *pgxpool.Pool, users Accounts, outlets Outlets, mailer Mailer, webBaseURL string) *Jobs {
	return &Jobs{pool: pool, users: users, outlets: outlets, mailer: mailer, webURL: webBaseURL}
}

// Register adds the module's handlers to w.
func (j *Jobs) Register(w *outbox.Worker) {
	w.Handle(JobSendPasswordReset, j.sendPasswordReset)
	w.Handle(JobSendEmailVerification, j.sendEmailVerification)
	w.Handle(JobSendPasswordChanged, j.sendPasswordChanged)
	w.Handle(JobSendEmailChangeNotice, j.sendEmailChangeNotice)
}

func (j *Jobs) sendPasswordReset(ctx context.Context, job outbox.Job) error {
	var p jobUser
	u, ok, err := j.load(ctx, job, &p, func() string { return p.UserID })
	if !ok || err != nil {
		return err
	}
	// Only the Owner resets by email, and only to a verified address. If
	// that changed since the request, there is nothing to send.
	email, verified := u.VerifiedEmail()
	if !u.IsOwner || !verified {
		return nil
	}
	token, err := j.newToken(ctx, u.UserID, "password_reset", nil, ResetLinkTTL.Seconds())
	if err != nil {
		return err
	}
	return j.send(ctx, u, email, mail.Email{
		Name: "password_reset",
		Data: map[string]any{"minutes": int(math.Round(ResetLinkTTL.Minutes()))},
		Link: j.link("/reset-password", token),
	})
}

func (j *Jobs) sendEmailVerification(ctx context.Context, job outbox.Job) error {
	var p jobEmail
	u, ok, err := j.load(ctx, job, &p, func() string { return p.UserID })
	if !ok || err != nil {
		return err
	}
	// A new link replaces any older one still waiting, so only the latest
	// address asked for can be confirmed.
	if err := store.New(j.pool).UseAuthTokens(ctx, store.UseAuthTokensParams{UserID: u.UserID, Purpose: "email_verify"}); err != nil {
		return err
	}
	token, err := j.newToken(ctx, u.UserID, "email_verify", &p.Email, VerifyLinkTTL.Seconds())
	if err != nil {
		return err
	}
	return j.send(ctx, u, p.Email, mail.Email{
		Name: "email_verify",
		Data: map[string]any{"email": p.Email, "hours": int(math.Round(VerifyLinkTTL.Hours()))},
		Link: j.link("/verify-email", token),
	})
}

func (j *Jobs) sendPasswordChanged(ctx context.Context, job outbox.Job) error {
	var p jobUser
	u, ok, err := j.load(ctx, job, &p, func() string { return p.UserID })
	if !ok || err != nil {
		return err
	}
	email, verified := u.VerifiedEmail()
	if !verified {
		return nil
	}
	return j.send(ctx, u, email, mail.Email{Name: "password_changed"})
}

func (j *Jobs) sendEmailChangeNotice(ctx context.Context, job outbox.Job) error {
	var p jobEmailChange
	u, ok, err := j.load(ctx, job, &p, func() string { return p.UserID })
	if !ok || err != nil {
		return err
	}
	// To the old address, which is the one someone who took over the
	// session doesn't control.
	return j.send(ctx, u, p.OldEmail, mail.Email{
		Name: "email_changed",
		Data: map[string]any{"new_email": p.NewEmail},
	})
}

// load decodes the job's payload into dst and loads the user it names. ok
// is false, with no error, when the user is gone or can't sign in anymore:
// the job has nothing left to do and must not be retried.
func (j *Jobs) load(ctx context.Context, job outbox.Job, dst any, userID func() string) (user.User, bool, error) {
	if err := json.Unmarshal(job.Payload, dst); err != nil {
		return user.User{}, false, fmt.Errorf("decode %s payload: %w", job.Kind, err)
	}
	u, err := j.users.ByID(ctx, userID())
	if errors.Is(err, user.ErrNotFound) {
		return user.User{}, false, nil
	}
	if err != nil {
		return user.User{}, false, fmt.Errorf("load user %s: %w", userID(), err)
	}
	return u, u.Active, nil
}

// newToken stores a new single-use token and returns it.
func (j *Jobs) newToken(ctx context.Context, userID, purpose string, email *string, ttlSeconds float64) (string, error) {
	token, hash := newSecret()
	err := store.New(j.pool).InsertAuthToken(ctx, store.InsertAuthTokenParams{
		AuthTokenID: id.New(),
		UserID:      userID,
		Purpose:     purpose,
		Email:       email,
		TokenHash:   hash,
		TtlSeconds:  ttlSeconds,
	})
	if err != nil {
		return "", fmt.Errorf("store %s token: %w", purpose, err)
	}
	return token, nil
}

// link builds a link to the web app with the token in the URL fragment,
// which browsers never send to a server, so it stays out of every log.
func (j *Jobs) link(path, token string) string {
	return j.webURL + path + "#token=" + url.QueryEscape(token)
}

// send renders e for u, in their language, and sends it to the address to.
func (j *Jobs) send(ctx context.Context, u user.User, to string, e mail.Email) error {
	o, _, err := outletOf(ctx, j.outlets, u)
	if err != nil {
		return err
	}
	data := map[string]any{"name": u.Name, "username": u.Username}
	for k, v := range e.Data {
		data[k] = v
	}
	e.Data = data
	msg, err := mail.Render(i18n.For(language(u.Locale, o.Locale)), to, e)
	if err != nil {
		return err
	}
	return j.mailer.Send(ctx, msg)
}
