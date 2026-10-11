// Package i18n translates the text the server shows people — API error
// messages and emails — into Indonesian, the default, or English.
//
// Each language is one JSON catalog in locales/, named after its language
// tag and embedded into both binaries, so adding a language means adding a
// file. Keys are flat and dotted: "error.<code>" for the message of an API
// error code, "email.<name>.<part>" for emails. The tests fail until every
// catalog has every key.
//
// Logs never go through this package; they stay in English.
package i18n

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

// Default is the language used when nothing asked for has a catalog.
const Default = "id"

//go:embed locales/*.json
var catalogs embed.FS

// The catalogs are embedded and never change after loading, so the bundle is
// safe to share. A malformed catalog panics here, which every test of a
// package that imports this one reports.
var (
	bundle    = mustLoad()
	languages = bundle.LanguageTags() // Default first
	matcher   = language.NewMatcher(languages)
)

func mustLoad() *goi18n.Bundle {
	b := goi18n.NewBundle(language.MustParse(Default))
	files, err := fs.Glob(catalogs, "locales/*.json")
	if err != nil {
		panic(err)
	}
	for _, f := range files {
		if _, err := b.LoadMessageFileFS(catalogs, f); err != nil {
			panic(fmt.Sprintf("i18n: load %s: %v", f, err))
		}
	}
	return b
}

// Localizer translates into one language.
type Localizer struct {
	lang string
	l    *goi18n.Localizer
}

// For returns a Localizer for the first language in prefs that has a
// catalog. Each pref is a language tag ("en", "en-US") or a whole
// Accept-Language header ("en-US,en;q=0.9,id;q=0.8"). When none matches — or
// prefs is empty — it translates into Default.
func For(prefs ...string) Localizer {
	_, i := language.MatchStrings(matcher, prefs...)
	lang := languages[i].String()
	return Localizer{lang: lang, l: goi18n.NewLocalizer(bundle, lang)}
}

// Language is the language this Localizer translates into, such as "en".
func (l Localizer) Language() string { return l.lang }

// T returns the message with the given ID, its template filled from data
// (template fields are data's keys, e.g. {{.available}}). A message missing
// in this language falls back to Default; a message missing everywhere comes
// back as its ID, which the catalog tests keep from shipping.
func (l Localizer) T(id string, data map[string]any) string {
	msg, err := l.l.Localize(&goi18n.LocalizeConfig{MessageID: id, TemplateData: data})
	if msg == "" && err != nil {
		return id
	}
	return msg
}

// Error returns the message for an API error code: the "error.<code>"
// message, filled from the error's details.
func (l Localizer) Error(code string, details map[string]any) string {
	return l.T("error."+code, details)
}

// Languages lists the languages that have a catalog, Default first.
func Languages() []string {
	out := make([]string, len(languages))
	for i, tag := range languages {
		out[i] = tag.String()
	}
	return out
}

// Supported reports whether lang is exactly the tag of a language with a
// catalog: "en" is, "en-US" and "EN" are not. It validates a language a user
// picks, which is stored as given.
func Supported(lang string) bool {
	return slices.Contains(Languages(), lang)
}

// Has reports whether a message with this ID exists. Every catalog has the
// same keys (the tests check), so this is the same as asking whether id is
// translated at all.
func Has(id string) bool {
	_, err := goi18n.NewLocalizer(bundle, Default).Localize(&goi18n.LocalizeConfig{MessageID: id})
	_, missing := errors.AsType[*goi18n.MessageNotFoundErr](err)
	return !missing
}
