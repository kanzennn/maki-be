package i18n

import (
	"encoding/json"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
)

// readCatalogs returns every embedded catalog as language → key → message.
func readCatalogs(t *testing.T) map[string]map[string]string {
	t.Helper()
	files, err := fs.Glob(catalogs, "locales/*.json")
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]map[string]string)
	for _, f := range files {
		data, err := catalogs.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var messages map[string]string
		if err := json.Unmarshal(data, &messages); err != nil {
			t.Fatalf("%s must be a flat object of key → message: %v", f, err)
		}
		out[strings.TrimSuffix(path.Base(f), ".json")] = messages
	}
	return out
}

var placeholder = regexp.MustCompile(`\{\{\s*\.(\w+)\s*\}\}`)

func placeholders(msg string) []string {
	var names []string
	for _, m := range placeholder.FindAllStringSubmatch(msg, -1) {
		names = append(names, m[1])
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// Every language must have every key, with the same template fields, and
// every message must render — the check the spec asks CI to make.
func TestCatalogsAgree(t *testing.T) {
	all := readCatalogs(t)
	base, ok := all[Default]
	if !ok {
		t.Fatalf("no catalog for the default language %q", Default)
	}
	if len(all) < 2 {
		t.Fatalf("found catalogs for %v, want at least id and en", slices.Collect(maps.Keys(all)))
	}

	for lang, messages := range all {
		for key := range base {
			if _, ok := messages[key]; !ok {
				t.Errorf("%s.json is missing %q", lang, key)
			}
		}
		for key, msg := range messages {
			if _, ok := base[key]; !ok {
				t.Errorf("%s.json has %q, which %s.json doesn't", lang, key, Default)
				continue
			}
			if strings.TrimSpace(msg) == "" {
				t.Errorf("%s.json: %q is empty", lang, key)
			}
			if got, want := placeholders(msg), placeholders(base[key]); !slices.Equal(got, want) {
				t.Errorf("%s.json: %q uses fields %v, %s.json uses %v", lang, key, got, Default, want)
			}

			data := make(map[string]any)
			for _, name := range placeholders(msg) {
				data[name] = "x"
			}
			out, err := goi18n.NewLocalizer(bundle, lang).Localize(&goi18n.LocalizeConfig{MessageID: key, TemplateData: data})
			if err != nil || out == "" || strings.Contains(out, "<no value>") {
				t.Errorf("%s.json: %q doesn't render: %q, %v", lang, key, out, err)
			}
		}
	}
}

func TestForPicksTheLanguage(t *testing.T) {
	tests := []struct {
		prefs []string
		want  string
	}{
		{nil, "id"},
		{[]string{""}, "id"},
		{[]string{"en"}, "en"},
		{[]string{"en-US,en;q=0.9"}, "en"},
		{[]string{"id-ID"}, "id"},
		{[]string{"fr-FR,fr;q=0.9"}, "id"},     // no catalog: the default
		{[]string{"fr,en;q=0.5"}, "en"},        // the first one that has a catalog
		{[]string{"de", "en"}, "en"},           // several preferences, in order
		{[]string{"not a language tag"}, "id"}, // garbage is ignored
	}
	for _, tt := range tests {
		if got := For(tt.prefs...).Language(); got != tt.want {
			t.Errorf("For(%q).Language() = %q, want %q", tt.prefs, got, tt.want)
		}
	}
}

func TestErrorTranslates(t *testing.T) {
	all := readCatalogs(t)
	for _, lang := range []string{"id", "en"} {
		if got, want := For(lang).Error("not_found", nil), all[lang]["error.not_found"]; got != want {
			t.Errorf("For(%q).Error(not_found) = %q, want %q", lang, got, want)
		}
	}
	if got := For("en").Error("no_such_code", nil); got != "error.no_such_code" {
		t.Errorf("an unknown code gives %q, want its message ID", got)
	}
}

func TestLanguagesAndSupported(t *testing.T) {
	langs := Languages()
	if len(langs) == 0 || langs[0] != Default {
		t.Fatalf("Languages() = %v, want %q first", langs, Default)
	}
	if !slices.Contains(langs, "en") {
		t.Errorf("Languages() = %v, want en in it", langs)
	}
	for lang, want := range map[string]bool{"id": true, "en": true, "en-US": false, "EN": false, "fr": false, "": false} {
		if got := Supported(lang); got != want {
			t.Errorf("Supported(%q) = %v, want %v", lang, got, want)
		}
	}
}

func TestHas(t *testing.T) {
	if !Has("error.internal_error") {
		t.Error("Has(error.internal_error) = false, want true")
	}
	if Has("error.no_such_code") {
		t.Error("Has(error.no_such_code) = true, want false")
	}
}
