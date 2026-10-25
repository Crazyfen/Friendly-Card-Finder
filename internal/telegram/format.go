package telegram

import (
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/message"
	"fmt"
	"strings"
)

// escape covers the three characters Telegram's HTML parse mode requires
// escaped in text. html.EscapeString would also rewrite ' and ", which Telegram
// renders the same but which would change the bytes of every existing reply.
var escape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace

// escapeAttr also escapes the quote that would close an href.
var escapeAttr = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace

// Format renders a Message as Telegram HTML. Every piece of text is escaped, so
// card names and logins can never be read as markup.
func Format(m message.Message) string {
	var b strings.Builder
	for _, s := range m {
		switch s.Kind {
		case message.KindBold:
			b.WriteString("<b>" + escape(s.Text) + "</b>")
		case message.KindCode:
			b.WriteString("<code>" + escape(s.Text) + "</code>")
		case message.KindLink:
			b.WriteString(`<a href="` + escapeAttr(s.URL) + `">` + escape(s.Text) + "</a>")
		case message.KindContacts:
			b.WriteString(formatContacts(s.Contacts))
		default:
			b.WriteString(escape(s.Text))
		}
	}
	return b.String()
}

// formatContacts lists Telegram Contacts first, as links, then Discord Contacts
// by name, since a Telegram reader cannot follow a Discord mention. A Contact
// without a Handle has no form that works from here and is left out.
func formatContacts(cs []deckbox.Contact) string {
	var parts []string
	for _, c := range message.ReaderFirst(cs, deckbox.PlatformTelegram) {
		if c.Handle == nil {
			continue
		}
		switch c.Platform {
		case deckbox.PlatformTelegram:
			parts = append(parts, fmt.Sprintf(`<a href="tg://user?id=%d">@%s</a>`, c.UserID, escape(*c.Handle)))
		case deckbox.PlatformDiscord:
			parts = append(parts, "Discord: "+escape(*c.Handle))
		}
	}
	return strings.Join(parts, ", ")
}
