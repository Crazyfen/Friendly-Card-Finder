package discord

import (
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/message"
	"strings"
	"testing"
)

func TestFormatEscapesMarkdown(t *testing.T) {
	got := Format(message.Message{message.Text("some_user *x* ~y~ `z` | > [a](b) # \\")})
	want := "some\\_user \\*x\\* \\~y\\~ \\`z\\` \\| \\> \\[a\\]\\(b\\) \\# \\\\"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatSpans(t *testing.T) {
	url := "https://deckbox.org/sets/42?f=17U2hvY2s="
	cases := []struct {
		name string
		m    message.Message
		want string
	}{
		{"bold", message.Message{message.Bold("Shock")}, "**Shock**"},
		{"bold escapes", message.Message{message.Bold("a_b")}, `**a\_b**`},
		{"code", message.Message{message.Code("/search_x")}, "`/search_x`"},
		{"code with backtick", message.Message{message.Code("a`b")}, "`` a`b ``"},
		{"link", message.Message{message.Link("petya", url)}, "[petya](" + url + ")"},
		{"link text escaped", message.Message{message.Link("a_b", url)}, `[a\_b](` + url + `)`},
	}
	for _, c := range cases {
		if got := Format(c.m); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFormatContactsDiscordFirst(t *testing.T) {
	petya, alice := "petya", "alice_mtg"
	cs := []deckbox.Contact{
		{Platform: deckbox.PlatformTelegram, UserID: 7, Handle: &petya},
		{Platform: deckbox.PlatformDiscord, UserID: 9, Handle: &alice},
		{Platform: deckbox.PlatformTelegram, UserID: 8},
	}
	got := Format(message.Message{message.Contacts(cs)})
	want := `<@9> (alice\_mtg), [@petya](https://t.me/petya)`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if strings.Contains(got, "8") {
		t.Errorf("handle-less contact leaked: %q", got)
	}
}
