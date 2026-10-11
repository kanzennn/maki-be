package mail

import (
	"context"
	"fmt"
	"log/slog"
	netmail "net/mail"
)

// Log is the development mailer: it writes each message to the log instead
// of sending it, so flows such as password reset work without mail
// credentials — the link to open is in the worker's log.
type Log struct {
	logger *slog.Logger
}

// NewLog returns a mailer that only logs.
func NewLog(logger *slog.Logger) *Log {
	return &Log{logger: logger}
}

// Send logs msg. It still rejects a malformed recipient, as SMTP would.
func (l *Log) Send(ctx context.Context, msg Message) error {
	if _, err := netmail.ParseAddress(msg.To); err != nil {
		return fmt.Errorf("mail: recipient %q: %w", msg.To, err)
	}
	l.logger.InfoContext(ctx, "email not sent: development writes it to the log instead",
		slog.String("to", msg.To),
		slog.String("subject", msg.Subject),
		slog.String("text", msg.Text),
	)
	return nil
}
