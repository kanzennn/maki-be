package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http/httptest"
	netmail "net/mail"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/config"
)

// fakeSMTP is just enough of an SMTP server to test the client: EHLO,
// optional STARTTLS, AUTH PLAIN, MAIL, RCPT, DATA and QUIT.
type fakeSMTP struct {
	t        *testing.T
	ln       net.Listener
	tls      *tls.Config // certificate for STARTTLS and implicit TLS
	startTLS bool        // offer STARTTLS in the EHLO reply
	user     string      // required AUTH PLAIN credentials; empty: none
	password string

	mu   sync.Mutex
	got  []received
	wg   sync.WaitGroup
	fail string // reply to DATA's end with this error instead of 250
}

type received struct {
	from, to string
	data     []byte
	tls      bool
	authed   bool
}

// testCert borrows httptest's certificate for 127.0.0.1 and a pool that
// trusts it.
func testCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	srv := httptest.NewTLSServer(nil)
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv.TLS.Certificates[0], pool
}

func startFakeSMTP(t *testing.T, implicitTLS bool, configure func(*fakeSMTP)) (*fakeSMTP, *x509.CertPool) {
	t.Helper()
	cert, pool := testCert(t)
	s := &fakeSMTP{t: t, tls: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	if configure != nil {
		configure(s)
	}
	s.ln = listen(t)
	if implicitTLS {
		s.ln = tls.NewListener(s.ln, s.tls)
	}
	s.wg.Go(func() {
		for {
			conn, err := s.ln.Accept()
			if err != nil {
				return
			}
			s.wg.Go(func() { s.serve(conn, implicitTLS) })
		}
	})
	t.Cleanup(func() {
		_ = s.ln.Close()
		s.wg.Wait()
	})
	return s, pool
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func (s *fakeSMTP) port() int {
	return s.ln.Addr().(*net.TCPAddr).Port
}

func (s *fakeSMTP) messages() []received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]received(nil), s.got...)
}

func (s *fakeSMTP) serve(conn net.Conn, secure bool) {
	defer func() { _ = conn.Close() }()
	tp := textproto.NewConn(conn)
	var msg received
	msg.tls = secure
	reply := func(format string, args ...any) bool { return tp.PrintfLine(format, args...) == nil }

	if !reply("220 fake ESMTP") {
		return
	}
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO":
			lines := []string{"fake"}
			if s.startTLS && !msg.tls {
				lines = append(lines, "STARTTLS")
			}
			if s.user != "" {
				lines = append(lines, "AUTH PLAIN")
			}
			for i, l := range lines {
				sep := "-"
				if i == len(lines)-1 {
					sep = " "
				}
				reply("250%s%s", sep, l)
			}
		case "STARTTLS":
			reply("220 go ahead")
			tlsConn := tls.Server(conn, s.tls)
			if err := tlsConn.HandshakeContext(s.t.Context()); err != nil {
				return
			}
			conn, tp, msg.tls = tlsConn, textproto.NewConn(tlsConn), true
		case "AUTH":
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(arg, "PLAIN "))
			parts := strings.Split(string(raw), "\x00")
			if len(parts) == 3 && parts[1] == s.user && parts[2] == s.password {
				msg.authed = true
				reply("235 ok")
			} else {
				reply("535 bad credentials")
			}
		case "MAIL":
			if s.user != "" && !msg.authed {
				reply("530 authenticate first")
				continue
			}
			msg.from = between(arg, "<", ">")
			reply("250 ok")
		case "RCPT":
			msg.to = between(arg, "<", ">")
			reply("250 ok")
		case "DATA":
			reply("354 go on")
			data, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			if s.fail != "" {
				reply("%s", s.fail)
				continue
			}
			msg.data = data
			s.mu.Lock()
			s.got = append(s.got, msg)
			s.mu.Unlock()
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 unknown command")
		}
	}
}

func between(s, open, closing string) string {
	_, rest, _ := strings.Cut(s, open)
	v, _, _ := strings.Cut(rest, closing)
	return v
}

func newTestSMTP(t *testing.T, srv *fakeSMTP, pool *x509.CertPool, cfg config.SMTP) *SMTP {
	t.Helper()
	cfg.Host = "127.0.0.1"
	cfg.Port = srv.port()
	if cfg.From == "" {
		cfg.From = "Maki <no-reply@maki.test>"
	}
	m, err := NewSMTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.tlsConfig.RootCAs = pool
	return m
}

var testMessage = Message{
	To:      "owner@maki.test",
	Subject: "Buat password baru — akun Maki",
	Text:    "Halo Faiz,\n\nBuka tautan ini: https://maki.test/reset#token=abc\n",
	HTML:    "<p>Halo Faiz,</p><p><a href=\"https://maki.test/reset#token=abc\">Buat password baru</a></p>",
}

func TestSMTPSendsOverSTARTTLSWithAuth(t *testing.T) {
	srv, pool := startFakeSMTP(t, false, func(s *fakeSMTP) {
		s.startTLS, s.user, s.password = true, "maki", "s3cret"
	})
	m := newTestSMTP(t, srv, pool, config.SMTP{TLS: config.SMTPStartTLS, Username: "maki", Password: "s3cret"})

	if err := m.Send(t.Context(), testMessage); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := srv.messages()
	if len(got) != 1 {
		t.Fatalf("server received %d messages, want 1", len(got))
	}
	if !got[0].tls || !got[0].authed {
		t.Errorf("tls = %v, authed = %v, want both", got[0].tls, got[0].authed)
	}
	if got[0].from != "no-reply@maki.test" || got[0].to != "owner@maki.test" {
		t.Errorf("envelope = %s → %s", got[0].from, got[0].to)
	}
	assertMessage(t, got[0].data, testMessage)
}

func TestSMTPSendsOverImplicitTLS(t *testing.T) {
	srv, pool := startFakeSMTP(t, true, nil)
	m := newTestSMTP(t, srv, pool, config.SMTP{TLS: config.SMTPTLS})

	if err := m.Send(t.Context(), testMessage); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := srv.messages(); len(got) != 1 || !got[0].tls {
		t.Fatalf("received %+v, want one message over TLS", got)
	}
}

func TestSMTPRefusesPlainTextWhenSTARTTLSIsMissing(t *testing.T) {
	srv, pool := startFakeSMTP(t, false, nil) // doesn't offer STARTTLS
	m := newTestSMTP(t, srv, pool, config.SMTP{TLS: config.SMTPStartTLS})

	err := m.Send(t.Context(), testMessage)
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("Send error = %v, want a refusal naming STARTTLS", err)
	}
	if got := srv.messages(); len(got) != 0 {
		t.Errorf("server received %d messages in plain text, want 0", len(got))
	}
}

func TestSMTPPlainTextForLocalCatcher(t *testing.T) {
	srv, pool := startFakeSMTP(t, false, nil)
	m := newTestSMTP(t, srv, pool, config.SMTP{TLS: config.SMTPNoTLS})

	if err := m.Send(t.Context(), Message{To: "a@maki.test", Subject: "Hi", Text: "Only text"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := srv.messages()
	if len(got) != 1 {
		t.Fatalf("received %d messages, want 1", len(got))
	}
	assertMessage(t, got[0].data, Message{To: "a@maki.test", Subject: "Hi", Text: "Only text"})
}

func TestSMTPReportsRejectedMessage(t *testing.T) {
	srv, pool := startFakeSMTP(t, false, func(s *fakeSMTP) { s.fail = "554 rejected as spam" })
	m := newTestSMTP(t, srv, pool, config.SMTP{TLS: config.SMTPNoTLS})

	err := m.Send(t.Context(), testMessage)
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("Send error = %v, want the server's rejection", err)
	}
}

func TestSMTPRejectsBadInput(t *testing.T) {
	srv, pool := startFakeSMTP(t, false, nil)
	m := newTestSMTP(t, srv, pool, config.SMTP{TLS: config.SMTPNoTLS})

	for name, msg := range map[string]Message{
		"recipient isn't an address":   {To: "not an address", Subject: "Hi", Text: "x"},
		"header injection via subject": {To: "a@maki.test", Subject: "Hi\r\nBcc: victim@maki.test", Text: "x"},
	} {
		if err := m.Send(t.Context(), msg); err == nil {
			t.Errorf("%s: Send succeeded, want an error", name)
		}
	}
	if got := srv.messages(); len(got) != 0 {
		t.Errorf("server received %d messages, want 0", len(got))
	}
}

func TestSMTPGivesUpWhenContextEnds(t *testing.T) {
	// A server that accepts the connection and never greets.
	ln := listen(t)
	defer func() { _ = ln.Close() }()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_, _ = io.Copy(io.Discard, conn)
		}
	}()
	m, err := NewSMTP(config.SMTP{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, TLS: config.SMTPNoTLS, From: "no-reply@maki.test"})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	err = m.Send(ctx, testMessage)
	if netErr, ok := errors.AsType[net.Error](err); !ok || !netErr.Timeout() {
		t.Fatalf("Send error = %v, want a timeout", err)
	}
}

// assertMessage parses raw as an email and checks it carries want.
func assertMessage(t *testing.T, raw []byte, want Message) {
	t.Helper()
	msg, err := netmail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("not a valid message: %v\n%s", err, raw)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != want.Subject {
		t.Errorf("Subject = %q (%v), want %q", subject, err, want.Subject)
	}
	if to, err := msg.Header.AddressList("To"); err != nil || len(to) != 1 || to[0].Address != want.To {
		t.Errorf("To = %v (%v), want %s", to, err, want.To)
	}
	for _, h := range []string{"From", "Date", "Message-ID", "MIME-Version"} {
		if msg.Header.Get(h) == "" {
			t.Errorf("missing %s header", h)
		}
	}
	if msg.Header.Get("Bcc") != "" {
		t.Error("message has a Bcc header")
	}

	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("Content-Type: %v", err)
	}
	bodies := map[string]string{}
	if mediaType == "multipart/alternative" {
		r := multipart.NewReader(msg.Body, params["boundary"])
		for {
			p, err := r.NextRawPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("read part: %v", err)
			}
			typ, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
			bodies[typ] = decodeQP(t, p)
		}
	} else {
		bodies[mediaType] = decodeQP(t, msg.Body)
	}

	// Transport may end a body with a line break; the text before it counts.
	if got := trimBody(bodies["text/plain"]); got != trimBody(want.Text) {
		t.Errorf("text part = %q, want %q", got, want.Text)
	}
	if got := trimBody(bodies["text/html"]); got != trimBody(want.HTML) {
		t.Errorf("html part = %q, want %q", got, want.HTML)
	}
}

func trimBody(s string) string {
	return strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// The server side of these tests reads DATA with textproto, which turns CRLF
// into LF, so line endings are checked on encode's own output.
func TestEncodeUsesCRLFThroughout(t *testing.T) {
	from, _ := netmail.ParseAddress("Maki <no-reply@maki.test>")
	to, _ := netmail.ParseAddress("owner@maki.test")
	for name, msg := range map[string]Message{
		"text only":        {Subject: "Hi", Text: "line one\nline two\n"},
		"text and html":    {Subject: "Hi", Text: "line one\nline two\n", HTML: "<p>one</p>\n<p>two</p>\n"},
		"non-ASCII header": {Subject: "Kata sandi — diganti", Text: "é\n"},
		"very long line":   {Subject: "Hi", Text: strings.Repeat("x", 5000)},
	} {
		raw, err := msg.encode(*from, *to, time.Now())
		if err != nil {
			t.Fatalf("%s: encode: %v", name, err)
		}
		if strings.Contains(strings.ReplaceAll(string(raw), "\r\n", ""), "\n") {
			t.Errorf("%s: bare LF in the encoded message:\n%q", name, raw)
		}
		if _, err := netmail.ReadMessage(strings.NewReader(string(raw))); err != nil {
			t.Errorf("%s: not a valid message: %v", name, err)
		}
		for line := range strings.SplitSeq(string(raw), "\r\n") {
			if len(line) > 998 {
				t.Errorf("%s: a line of %d characters, over SMTP's limit of 998", name, len(line))
			}
		}
	}
}

func decodeQP(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(quotedprintable.NewReader(bufio.NewReader(r)))
	if err != nil {
		t.Fatalf("decode quoted-printable: %v", err)
	}
	return string(b)
}
