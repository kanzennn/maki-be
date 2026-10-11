package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/kervzent/maki-be/internal/platform/config"
	"github.com/kervzent/maki-be/internal/platform/id"
)

// sendTimeout bounds sending one message when ctx has no deadline.
const sendTimeout = 30 * time.Second

// SMTP sends through an SMTP server. It opens one connection per message:
// the worker sends a handful of emails, not bulk mail.
type SMTP struct {
	addr      string
	host      string
	from      netmail.Address
	mode      string    // config.SMTPStartTLS, config.SMTPTLS or config.SMTPNoTLS
	auth      smtp.Auth // nil when the server needs no authentication
	tlsConfig *tls.Config
}

// NewSMTP returns a mailer for the server in cfg.
func NewSMTP(cfg config.SMTP) (*SMTP, error) {
	from, err := netmail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("mail: SMTP_FROM: %w", err)
	}
	s := &SMTP{
		addr:      net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		host:      cfg.Host,
		from:      *from,
		mode:      cfg.TLS,
		tlsConfig: &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12},
	}
	if cfg.Username != "" {
		// PlainAuth refuses to send the password over a connection that
		// isn't encrypted, except to localhost.
		s.auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}
	return s, nil
}

// Send delivers msg to the server. With STARTTLS it refuses to go on if the
// server doesn't offer it, rather than send in plain text.
func (s *SMTP) Send(ctx context.Context, msg Message) error {
	to, err := netmail.ParseAddress(msg.To)
	if err != nil {
		return fmt.Errorf("mail: recipient %q: %w", msg.To, err)
	}
	data, err := msg.encode(s.from, *to, time.Now())
	if err != nil {
		return err
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(sendTimeout)
	}
	conn, err := s.dial(ctx, deadline)
	if err != nil {
		return fmt.Errorf("mail: connect to %s: %w", s.addr, err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("mail: %w", err)
	}

	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return fmt.Errorf("mail: greet %s: %w", s.addr, err)
	}
	defer func() { _ = c.Close() }()

	if s.mode == config.SMTPStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("mail: %s doesn't offer STARTTLS; refusing to send in plain text", s.addr)
		}
		if err := c.StartTLS(s.tlsConfig); err != nil {
			return fmt.Errorf("mail: start TLS: %w", err)
		}
	}
	if s.auth != nil {
		if err := c.Auth(s.auth); err != nil {
			return fmt.Errorf("mail: authenticate: %w", err)
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return fmt.Errorf("mail: sender: %w", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("mail: recipient: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: start data: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("mail: write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: server rejected the message: %w", err)
	}
	// The server has accepted the message. A failed QUIT doesn't unsend it,
	// and reporting it would make the job send the email twice.
	_ = c.Quit()
	return nil
}

func (s *SMTP) dial(ctx context.Context, deadline time.Time) (net.Conn, error) {
	d := &net.Dialer{Deadline: deadline}
	if s.mode == config.SMTPTLS {
		return (&tls.Dialer{NetDialer: d, Config: s.tlsConfig}).DialContext(ctx, "tcp", s.addr)
	}
	return d.DialContext(ctx, "tcp", s.addr)
}

// encode renders msg as an RFC 5322 message with CRLF line endings: a
// plain-text part, plus an HTML alternative when msg has one. Bodies are
// quoted-printable, so any UTF-8 text and line length survive transport.
func (m Message) encode(from, to netmail.Address, now time.Time) ([]byte, error) {
	if strings.ContainsAny(m.Subject, "\r\n") {
		return nil, errors.New("mail: the subject contains a line break")
	}
	domain := from.Address[strings.LastIndexByte(from.Address, '@')+1:]

	var buf bytes.Buffer
	header := func(key, value string) {
		buf.WriteString(key + ": " + value + "\r\n")
	}
	header("From", from.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", "<"+id.New()+"@"+domain+">")
	header("MIME-Version", "1.0")

	if m.HTML == "" {
		header("Content-Type", "text/plain; charset=utf-8")
		header("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		if err := writeQuotedPrintable(&buf, m.Text); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	mw := multipart.NewWriter(&buf)
	header("Content-Type", `multipart/alternative; boundary="`+mw.Boundary()+`"`)
	buf.WriteString("\r\n")
	for _, part := range []struct{ contentType, body string }{
		{"text/plain; charset=utf-8", m.Text},
		{"text/html; charset=utf-8", m.HTML},
	} {
		w, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.contentType},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		if err := writeQuotedPrintable(w, part.body); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeQuotedPrintable(w io.Writer, s string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := io.WriteString(qp, s); err != nil {
		return err
	}
	return qp.Close()
}
