package message

import (
	"testing"

	"FriendlyCardFinder/internal/deckbox"
)

func handle(s string) *string { return &s }

func TestReaderFirst(t *testing.T) {
	in := []deckbox.Contact{
		{Platform: deckbox.PlatformTelegram, UserID: 7},
		{Platform: deckbox.PlatformDiscord, UserID: 9},
	}

	got := ReaderFirst(in, deckbox.PlatformDiscord)
	if len(got) != 2 || got[0].UserID != 9 || got[1].UserID != 7 {
		t.Fatalf("discord reader: got %+v, want [dc 9, tg 7]", got)
	}
	if in[0].UserID != 7 || in[1].UserID != 9 {
		t.Fatalf("input slice was modified: %+v", in)
	}

	got = ReaderFirst(in, deckbox.PlatformTelegram)
	if got[0].UserID != 7 || got[1].UserID != 9 {
		t.Fatalf("telegram reader: got %+v, want order unchanged", got)
	}
}

func TestPlainText(t *testing.T) {
	m := Message{
		Text("On deckbox "),
		Link("petya", "https://x"),
		Contacts([]deckbox.Contact{
			{Platform: deckbox.PlatformTelegram, UserID: 1, Handle: handle("petya")},
			{Platform: deckbox.PlatformDiscord, UserID: 2, Handle: handle("alice")},
		}),
	}
	want := "On deckbox petyapetya, alice"
	if got := m.PlainText(); got != want {
		t.Fatalf("PlainText() = %q, want %q", got, want)
	}
}

func TestPlainTextSkipsNilHandle(t *testing.T) {
	m := Message{Contacts([]deckbox.Contact{
		{Platform: deckbox.PlatformTelegram, UserID: 1, Handle: nil},
		{Platform: deckbox.PlatformDiscord, UserID: 2, Handle: handle("alice")},
	})}
	if got := m.PlainText(); got != "alice" {
		t.Fatalf("PlainText() = %q, want %q", got, "alice")
	}
}
