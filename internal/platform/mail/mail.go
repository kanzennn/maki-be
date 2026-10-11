// Package mail sends email. Only the worker sends any, always from an outbox
// job, and it picks the adapter once at startup with New: SMTP when SMTP_HOST
// is set, or — in development only — a mailer that writes each email to the
// log instead of sending it.
package mail

import (
	"context"
	"errors"
	"log/slog"

	"github.com/kervzent/maki-be/internal/platform/config"
)

// Message is one rendered email.
type Message struct {
	To      string // the recipient's address, e.g. owner@example.com
	Subject string
	Text    string // plain-text body
	HTML    string // HTML alternative; optional
}

// Mailer sends messages. Implementations are safe for concurrent use.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// New returns the SMTP mailer for cfg or, in development without SMTP_HOST,
// the log mailer. Outside development a missing SMTP_HOST is an error: the
// log mailer would quietly swallow every password reset.
func New(cfg config.SMTP, development bool, logger *slog.Logger) (Mailer, error) {
	if cfg.Host == "" {
		if !development {
			return nil, errors.New("SMTP_HOST is required outside development: without it no email is sent")
		}
		return NewLog(logger), nil
	}
	return NewSMTP(cfg)
}
