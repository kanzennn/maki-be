// Package jobs holds the worker's job handlers: each decodes an outbox job's
// payload and does its work through the services. cmd/worker registers them
// on the outbox worker.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/i18n"
	"github.com/kervzent/maki-be/internal/utils/mail"
	"github.com/kervzent/maki-be/internal/utils/outbox"
)

// Mailer sends the rendered emails: mail.SMTP, or mail.Log in development.
type Mailer interface {
	Send(ctx context.Context, msg mail.Message) error
}

// AuthJobs sends the auth emails from the worker, in each recipient's
// language: password reset and email verification links, and the notices
// of a changed password or email.
type AuthJobs struct {
	users   *services.UserService
	outlets *services.OutletService
	links   *services.AuthTokenService
	mailer  Mailer
	webURL  string
}

// NewAuthJobs returns the auth emails' handlers; webBaseURL is the web app,
// where the links in the emails lead.
func NewAuthJobs(users *services.UserService, outlets *services.OutletService, links *services.AuthTokenService,
	mailer Mailer, webBaseURL string,
) *AuthJobs {
	return &AuthJobs{users: users, outlets: outlets, links: links, mailer: mailer, webURL: webBaseURL}
}

// Register adds the handlers to w.
func (j *AuthJobs) Register(w *outbox.Worker) {
	w.Handle(models.JobSendPasswordReset, j.sendPasswordReset)
	w.Handle(models.JobSendEmailVerification, j.sendEmailVerification)
	w.Handle(models.JobSendPasswordChanged, j.sendPasswordChanged)
	w.Handle(models.JobSendEmailChangeNotice, j.sendEmailChangeNotice)
}

func (j *AuthJobs) sendPasswordReset(ctx context.Context, job outbox.Job) error {
	var p models.JobUser
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
	token, err := j.links.NewPasswordReset(ctx, u.UserID)
	if err != nil {
		return err
	}
	return j.send(ctx, u, email, mail.Email{
		Name: "password_reset",
		Data: map[string]any{"minutes": int(math.Round(services.ResetLinkTTL.Minutes()))},
		Link: j.link("/reset-password", token),
	})
}

func (j *AuthJobs) sendEmailVerification(ctx context.Context, job outbox.Job) error {
	var p models.JobEmail
	u, ok, err := j.load(ctx, job, &p, func() string { return p.UserID })
	if !ok || err != nil {
		return err
	}
	token, err := j.links.NewEmailVerification(ctx, u.UserID, p.Email)
	if err != nil {
		return err
	}
	return j.send(ctx, u, p.Email, mail.Email{
		Name: "email_verify",
		Data: map[string]any{"email": p.Email, "hours": int(math.Round(services.VerifyLinkTTL.Hours()))},
		Link: j.link("/verify-email", token),
	})
}

func (j *AuthJobs) sendPasswordChanged(ctx context.Context, job outbox.Job) error {
	var p models.JobUser
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

func (j *AuthJobs) sendEmailChangeNotice(ctx context.Context, job outbox.Job) error {
	var p models.JobEmailChange
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
func (j *AuthJobs) load(ctx context.Context, job outbox.Job, dst any, userID func() string) (models.User, bool, error) {
	if err := json.Unmarshal(job.Payload, dst); err != nil {
		return models.User{}, false, fmt.Errorf("decode %s payload: %w", job.Kind, err)
	}
	u, err := j.users.ByID(ctx, userID())
	if errors.Is(err, apperror.ErrNoAccount) {
		return models.User{}, false, nil
	}
	if err != nil {
		return models.User{}, false, fmt.Errorf("load user %s: %w", userID(), err)
	}
	return u, u.Active, nil
}

// link builds a link to the web app with the token in the URL fragment,
// which browsers never send to a server, so it stays out of every log.
func (j *AuthJobs) link(path, token string) string {
	return j.webURL + path + "#token=" + url.QueryEscape(token)
}

// send renders e for u, in their language, and sends it to the address to.
func (j *AuthJobs) send(ctx context.Context, u models.User, to string, e mail.Email) error {
	lang, err := j.outlets.LanguageOf(ctx, u)
	if err != nil {
		return err
	}
	data := map[string]any{"name": u.Name, "username": u.Username}
	for k, v := range e.Data {
		data[k] = v
	}
	e.Data = data
	msg, err := mail.Render(i18n.For(lang), to, e)
	if err != nil {
		return err
	}
	return j.mailer.Send(ctx, msg)
}
