// Package discord holds the Discord side of the chat platform: wording is
// shared with Telegram through message.Message, this package only renders it.
package discord

import (
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/message"
	"fmt"
	"strings"
)

// escape backslash-escapes every character Discord's Markdown could read as
// formatting. One replacer scans once, so the backslash it adds is never
// escaped again.
var escape = strings.NewReplacer(
	`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "`", "\\`", "|", `\|`,
	">", `\>`, "[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`, "#", `\#`,
).Replace

// Format renders a Message as Discord Markdown. Text is escaped so card names
// and logins can never be read as formatting; code spans and URLs are not.
func Format(m message.Message) string {
	var b strings.Builder
	for _, s := range m {
		switch s.Kind {
		case message.KindBold:
			b.WriteString("**" + escape(s.Text) + "**")
		case message.KindCode:
			if strings.Contains(s.Text, "`") {
				b.WriteString("`` " + s.Text + " ``")
			} else {
				b.WriteString("`" + s.Text + "`")
			}
		case message.KindLink:
			b.WriteString("[" + escape(s.Text) + "](" + s.URL + ")")
		case message.KindContacts:
			b.WriteString(formatContacts(s.Contacts))
		default:
			b.WriteString(escape(s.Text))
		}
	}
	return b.String()
}

// formatContacts lists Discord Contacts first, as mentions, then Telegram
// Contacts as t.me links. A Contact without a Handle is left out.
func formatContacts(cs []deckbox.Contact) string {
	var parts []string
	for _, c := range message.ReaderFirst(cs, deckbox.PlatformDiscord) {
		if c.Handle == nil {
			continue
		}
		switch c.Platform {
		case deckbox.PlatformDiscord:
			parts = append(parts, fmt.Sprintf("<@%d> (%s)", c.UserID, escape(*c.Handle)))
		case deckbox.PlatformTelegram:
			parts = append(parts, fmt.Sprintf("[@%s](https://t.me/%s)", escape(*c.Handle), *c.Handle))
		}
	}
	return strings.Join(parts, ", ")
}
