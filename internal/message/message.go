// Package message is the platform-neutral form of a bot reply. A Message is a
// sequence of Spans that a platform renderer turns into Telegram HTML or Discord
// Markdown, so command code describes what to say without knowing where it goes.
package message

import (
	"sort"
	"strings"

	"FriendlyCardFinder/internal/deckbox"
)

// Kind says how a Span is rendered.
type Kind uint8

const (
	KindText Kind = iota
	KindBold
	KindCode
	KindLink
	KindContacts
)

// Span is one piece of a Message. Text carries the visible words for every
// Kind except KindContacts, URL is set only for KindLink, and Contacts is set
// only for KindContacts.
type Span struct {
	Kind     Kind
	Text     string
	URL      string
	Contacts []deckbox.Contact
}

// Message is a reply as an ordered list of Spans.
type Message []Span

// Text returns a plain span.
func Text(s string) Span { return Span{Kind: KindText, Text: s} }

// Bold returns a span rendered emphasised.
func Bold(s string) Span { return Span{Kind: KindBold, Text: s} }

// Code returns a span rendered as inline code.
func Code(s string) Span { return Span{Kind: KindCode, Text: s} }

// Link returns a span whose visible text links to url.
func Link(text, url string) Span { return Span{Kind: KindLink, Text: text, URL: url} }

// Contacts returns a span listing the people to reach, rendered per Platform by
// the renderer.
func Contacts(cs []deckbox.Contact) Span { return Span{Kind: KindContacts, Contacts: cs} }

// PlainText concatenates the text of every span with no separator. A Contacts
// span contributes its non-nil Handles joined by ", ".
func (m Message) PlainText() string {
	var b strings.Builder
	for _, s := range m {
		if s.Kind != KindContacts {
			b.WriteString(s.Text)
			continue
		}
		var handles []string
		for _, c := range s.Contacts {
			if c.Handle != nil {
				handles = append(handles, *c.Handle)
			}
		}
		b.WriteString(strings.Join(handles, ", "))
	}
	return b.String()
}

// ReaderFirst returns a copy of cs with the contacts on the reader's Platform
// moved to the front. Order is otherwise preserved; cs itself is not modified.
func ReaderFirst(cs []deckbox.Contact, reader deckbox.Platform) []deckbox.Contact {
	out := append([]deckbox.Contact(nil), cs...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Platform == reader && out[j].Platform != reader
	})
	return out
}
