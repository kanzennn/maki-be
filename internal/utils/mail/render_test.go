package mail

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/utils/i18n"
)

var resetEmail = Email{
	Name: "password_reset",
	Data: map[string]any{"name": "Faiz", "username": "owner", "minutes": 30},
	Link: "https://admin.maki.test/reset-password#token=abc123",
}

func TestRenderInTheRecipientsLanguage(t *testing.T) {
	for _, lang := range []string{"id", "en"} {
		loc := i18n.For(lang)
		msg, err := Render(loc, "owner@maki.test", resetEmail)
		if err != nil {
			t.Fatalf("%s: Render: %v", lang, err)
		}

		if want := loc.T("email.password_reset.subject", nil); msg.Subject != want {
			t.Errorf("%s: subject = %q, want %q", lang, msg.Subject, want)
		}
		for _, want := range []string{
			loc.T("email.greeting", resetEmail.Data),
			"owner", "30", // filled template fields
			loc.T("email.password_reset.action", nil),
			resetEmail.Link,
			strings.Split(loc.T("email.signature", nil), "\n")[0],
		} {
			if !strings.Contains(msg.Text, want) {
				t.Errorf("%s: text doesn't contain %q:\n%s", lang, want, msg.Text)
			}
		}
		for _, want := range []string{`lang="` + lang + `"`, `href="` + resetEmail.Link + `"`, loc.T("email.password_reset.action", nil)} {
			if !strings.Contains(msg.HTML, want) {
				t.Errorf("%s: HTML doesn't contain %q", lang, want)
			}
		}
		if msg.To != "owner@maki.test" {
			t.Errorf("%s: To = %q", lang, msg.To)
		}
	}
}

func TestRenderEveryEmail(t *testing.T) {
	data := map[string]any{"name": "Faiz", "username": "owner", "minutes": 30, "hours": 24, "email": "new@maki.test", "new_email": "new@maki.test"}
	for _, e := range []Email{
		{Name: "password_reset", Link: "https://maki.test/r"},
		{Name: "email_verify", Link: "https://maki.test/v"},
		{Name: "password_changed"},
		{Name: "email_changed"},
	} {
		e.Data = data
		for _, lang := range i18n.Languages() {
			if _, err := Render(i18n.For(lang), "owner@maki.test", e); err != nil {
				t.Errorf("%s in %s: %v", e.Name, lang, err)
			}
		}
	}
}

func TestRenderEscapesDataInHTML(t *testing.T) {
	e := resetEmail
	e.Data = map[string]any{"name": `<script>alert(1)</script>`, "username": "owner", "minutes": 30}

	msg, err := Render(i18n.For("en"), "owner@maki.test", e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg.HTML, "<script>") || !strings.Contains(msg.HTML, "&lt;script&gt;") {
		t.Errorf("the name reached the HTML unescaped:\n%s", msg.HTML)
	}
}

func TestRenderRejectsMistakes(t *testing.T) {
	for name, e := range map[string]Email{
		"unknown email":       {Name: "no_such_email", Data: resetEmail.Data},
		"missing field":       {Name: "password_reset", Data: map[string]any{"name": "Faiz"}, Link: resetEmail.Link},
		"link without button": {Name: "password_changed", Data: resetEmail.Data, Link: "https://maki.test/x"},
	} {
		if _, err := Render(i18n.For("id"), "owner@maki.test", e); err == nil {
			t.Errorf("%s: Render succeeded, want an error", name)
		}
	}
}

func TestLogMailerWritesTheEmailToTheLog(t *testing.T) {
	var logs bytes.Buffer
	m := NewLog(slog.New(slog.NewJSONHandler(&logs, nil)))

	if err := m.Send(t.Context(), Message{To: "owner@maki.test", Subject: "Reset", Text: "open https://maki.test/r#token=abc"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"owner@maki.test", "Reset", "https://maki.test/r#token=abc"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log doesn't contain %q: %s", want, logs.String())
		}
	}
	if err := m.Send(t.Context(), Message{To: "not an address"}); err == nil {
		t.Error("Send accepted a malformed recipient")
	}
}

func TestNewPicksTheMailer(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	if _, err := New(config.SMTP{}, false, logger); err == nil {
		t.Error("New without SMTP_HOST outside development succeeded, want an error")
	}
	if m, err := New(config.SMTP{}, true, logger); err != nil {
		t.Errorf("New in development: %v", err)
	} else if _, ok := m.(*Log); !ok {
		t.Errorf("New in development without SMTP_HOST = %T, want *Log", m)
	}
	cfg := config.SMTP{Host: "smtp.maki.test", Port: 587, TLS: config.SMTPStartTLS, From: "no-reply@maki.test"}
	if m, err := New(cfg, false, logger); err != nil {
		t.Errorf("New with SMTP_HOST: %v", err)
	} else if _, ok := m.(*SMTP); !ok {
		t.Errorf("New with SMTP_HOST = %T, want *SMTP", m)
	}
}
