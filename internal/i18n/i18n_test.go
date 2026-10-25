package i18n

import (
	"FriendlyCardFinder/internal/message"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The translation table's real interface is not T's signature: it is that every
// key exists in every language, carrying the same format verbs in the same
// order. Callers rely on both — T falls back to returning the key, and callers
// Format the result with a fixed number of spans — so a table that breaks
// either rule fails in a chat rather than in a build. These tests are where that
// interface is stated.

// referenceLang is the language every other is compared against. English is the
// fallback T serves for an unknown language code, so it is the one that must be
// complete.
const referenceLang = "en"

// verbPattern matches one fmt verb with its flags and width, or an escaped
// percent. A space is deliberately not a flag character here: a bare "%" in
// prose ("100% done") is likelier in a translation than fmt's space flag.
var verbPattern = regexp.MustCompile(`%%|%[-+#0-9.]*[a-zA-Z]`)

// formatVerbs returns the fmt verbs in s, in order ("%d %s" -> ["%d","%s"]),
// with flags and widths dropped so "%-5s" and "%s" compare equal.
func formatVerbs(s string) []string {
	var verbs []string
	for _, m := range verbPattern.FindAllString(s, -1) {
		if m != "%%" {
			verbs = append(verbs, "%"+m[len(m)-1:])
		}
	}
	return verbs
}

func TestEveryLanguageHasEveryKey(t *testing.T) {
	reference, ok := translations[referenceLang]
	if !ok {
		t.Fatalf("the reference language %q is missing from the table", referenceLang)
	}

	for lang, m := range translations {
		for key := range reference {
			if _, ok := m[key]; !ok {
				// T returns the key itself, so this reaches a Bot User as the
				// literal string "search.multi_not_found".
				t.Errorf("%s is missing key %q", lang, key)
			}
		}
		for key := range m {
			if _, ok := reference[key]; !ok {
				t.Errorf("%s has key %q that %s does not — every key must exist everywhere", lang, key, referenceLang)
			}
		}
	}
}

func TestFormatVerbsMatchAcrossLanguages(t *testing.T) {
	// Callers Format these with a fixed span list, so a translation with the
	// wrong placeholders prints a literal %s — and only for the users reading in
	// that language, which is the half nobody testing in English would see.
	reference := translations[referenceLang]

	for lang, m := range translations {
		if lang == referenceLang {
			continue
		}
		for key, want := range reference {
			got, ok := m[key]
			if !ok {
				continue // reported by TestEveryLanguageHasEveryKey
			}
			wantVerbs, gotVerbs := formatVerbs(want), formatVerbs(got)
			if !reflect.DeepEqual(wantVerbs, gotVerbs) {
				t.Errorf("%s %q has verbs %v, but %s has %v", lang, key, gotVerbs, referenceLang, wantVerbs)
			}
		}
	}
}

func TestFormatVerbs(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"no verbs here", nil},
		{"Processed: %d\nSkipped: %d", []string{"%d", "%d"}},
		{"Saved your nick, %s %s", []string{"%s", "%s"}},
		{` у <a href="tg://user?id=%d">@%s</a>`, []string{"%d", "%s"}},
		{"100%% done", nil},
		{"100%% done, %d left", []string{"%d"}},
		{"width and flags are ignored: %-5s %+d", []string{"%s", "%d"}},
		{"a trailing percent is not a verb %", nil},
		{"a bare percent in prose is not a verb: 100% done", nil},
	}

	for _, tt := range tests {
		if got := formatVerbs(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("formatVerbs(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestDetectLang(t *testing.T) {
	tests := map[string]string{
		"":      "en",
		"ru":    "ru",
		"ru-RU": "ru",
		"RU":    "ru",
		"en":    "en",
		"en-GB": "en",
		"de":    "en", // unsupported codes fall back rather than failing
	}

	for code, want := range tests {
		if got := DetectLang(code); got != want {
			t.Errorf("DetectLang(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestTFallsBackRatherThanFailing(t *testing.T) {
	// Both fallbacks are deliberate: a wrong string beats a crash mid-search.
	// They are only wrong as the *only* check, which is what the tests above are.
	if got := T("de", "start.title"); got != translations["en"]["start.title"] {
		t.Error("an unsupported language must fall back to English")
	}
	if got := T("en", "no.such.key"); got != "no.such.key" {
		t.Errorf("an unknown key must return itself, got %q", got)
	}
	if got := T("", "start.title"); got != translations["en"]["start.title"] {
		t.Error("an empty language must fall back to English")
	}
}

func TestFormatFillsPlaceholders(t *testing.T) {
	got := Format("en", "search.results_header", message.Bold("Shock"))
	want := message.Message{message.Text("Results for: "), message.Bold("Shock"), message.Text(":\n")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestFormatMissingArgKeepsPlaceholder(t *testing.T) {
	got := Format("en", "deckbox.register_success", message.Text("Petya")).PlainText()
	if want := "Saved your Deckbox nick, Petya %s"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNoMarkupInTranslations(t *testing.T) {
	// Each Platform writes its own markup, so a tag in a translation would reach
	// Discord as literal text. Keys ending in ".code" are the inside of a code
	// span: their "<" is the visible placeholder in "/deckbox <your_login>",
	// which the formatter escapes, not markup.
	for lang, m := range translations {
		for key, val := range m {
			if strings.HasSuffix(key, ".code") {
				continue
			}
			if strings.ContainsAny(val, "<*`") {
				t.Errorf("%s %q holds markup: %q", lang, key, val)
			}
		}
	}
}

func TestPlaceholdersAreStringsOnly(t *testing.T) {
	// Format fills %s and nothing else; any other verb would print literally.
	for lang, m := range translations {
		for key, val := range m {
			for _, v := range formatVerbs(val) {
				if v != "%s" {
					t.Errorf("%s %q uses %s; placeholders are %%s only", lang, key, v)
				}
			}
		}
	}
}
