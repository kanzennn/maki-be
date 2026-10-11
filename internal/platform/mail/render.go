package mail

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"

	"github.com/kervzent/maki-be/internal/platform/i18n"
)

//go:embed templates/layout.html
var templates embed.FS

// layout is the HTML every email shares. html/template escapes the
// translated text and data, so a user's name can't inject markup.
var layout = template.Must(template.ParseFS(templates, "templates/layout.html"))

// Email is an email to render from the i18n catalogs, in the recipient's
// language.
type Email struct {
	// Name picks the catalog keys: email.<Name>.subject, email.<Name>.body
	// (paragraphs separated by a blank line) and, when Link is set,
	// email.<Name>.action, the button's label.
	Name string
	// Data fills the template fields. Every email greets the recipient by
	// "name".
	Data map[string]any
	// Link is the URL behind the email's button, if it has one.
	Link string
}

type layoutData struct {
	Lang, Subject, Greeting string
	Paragraphs              []string
	Link, Action, LinkHint  string
	Signature               []string
	Footer                  string
}

// Render builds the message to send to the address to, in loc's language.
// It fails when the catalogs have no email by that name or a template field
// has no data, so a mistake fails the job instead of emailing someone
// "<no value>".
func Render(loc i18n.Localizer, to string, e Email) (Message, error) {
	key := "email." + e.Name + "."
	if !i18n.Has(key+"subject") || !i18n.Has(key+"body") {
		return Message{}, fmt.Errorf("mail: the catalogs have no email %q", e.Name)
	}
	t := func(id string) string { return loc.T(id, e.Data) }

	d := layoutData{
		Lang:       loc.Language(),
		Subject:    t(key + "subject"),
		Greeting:   t("email.greeting"),
		Paragraphs: strings.Split(t(key+"body"), "\n\n"),
		Signature:  strings.Split(t("email.signature"), "\n"),
		Footer:     t("email.footer"),
	}
	if e.Link != "" {
		if !i18n.Has(key + "action") {
			return Message{}, fmt.Errorf("mail: email %q has a link but no %saction label", e.Name, key)
		}
		d.Link = e.Link
		d.Action = t(key + "action")
		d.LinkHint = t("email.link_hint")
	}

	var text strings.Builder
	text.WriteString(d.Greeting + "\n\n")
	for _, p := range d.Paragraphs {
		text.WriteString(p + "\n\n")
	}
	if d.Link != "" {
		text.WriteString(d.Action + ":\n" + d.Link + "\n\n")
	}
	text.WriteString(strings.Join(d.Signature, "\n") + "\n\n-- \n" + d.Footer + "\n")

	var html bytes.Buffer
	if err := layout.Execute(&html, d); err != nil {
		return Message{}, fmt.Errorf("mail: render %q: %w", e.Name, err)
	}

	msg := Message{To: to, Subject: d.Subject, Text: text.String(), HTML: html.String()}
	if strings.Contains(msg.Text, "<no value>") {
		return Message{}, fmt.Errorf("mail: email %q is missing template data: %v", e.Name, e.Data)
	}
	return msg, nil
}
