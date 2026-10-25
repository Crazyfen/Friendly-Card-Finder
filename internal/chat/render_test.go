package chat_test

import (
	"FriendlyCardFinder/internal/chat"
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/message"
	"reflect"
	"strings"
	"testing"
)

func agg(listId int64, login string, cards map[string]int16, unique, total int) deckbox.UserSearchAggregate {
	return deckbox.UserSearchAggregate{
		ListId:        listId,
		DeckboxLogin:  login,
		FoundCards:    cards,
		UniqueCount:   unique,
		TotalQuantity: total,
	}
}

func ptr(s string) *string { return &s }

// contactsSpan returns the message's Contacts span, if it has one.
func contactsSpan(m message.Message) (message.Span, bool) {
	for _, s := range m {
		if s.Kind == message.KindContacts {
			return s, true
		}
	}
	return message.Span{}, false
}

// --- single-card rendering (the n=1 case) ---

func TestRenderSingle(t *testing.T) {
	t.Run("no results contains query", func(t *testing.T) {
		result := deckbox.SearchResult{
			SearchQueries: []string{"Counterspell"},
			NotFound:      []string{"Counterspell"},
		}
		msg, notFound := chat.RenderSearch(result, "en", deckbox.ScopeTradelist)
		if !strings.Contains(msg.PlainText(), "Counterspell") {
			t.Errorf("expected query in no-results message, got: %q", msg.PlainText())
		}
		if notFound != nil {
			t.Errorf("single-card search carries everything in the main message, got notFound: %q", notFound.PlainText())
		}
	})

	t.Run("sell scope no results contains query", func(t *testing.T) {
		result := deckbox.SearchResult{
			SearchQueries: []string{"Black Lotus"},
			NotFound:      []string{"Black Lotus"},
		}
		msg, _ := chat.RenderSearch(result, "en", deckbox.ScopeWishlist)
		if !strings.Contains(msg.PlainText(), "Black Lotus") {
			t.Errorf("expected query in no-results message, got: %q", msg.PlainText())
		}
	})

	t.Run("with results contains card name and owner", func(t *testing.T) {
		result := deckbox.SearchResult{
			SearchQueries: []string{"Bolt"},
			Aggregates: []deckbox.UserSearchAggregate{
				agg(10, "trader1", map[string]int16{"Lightning Bolt": 4}, 1, 4),
			},
		}
		msg, _ := chat.RenderSearch(result, "en", deckbox.ScopeTradelist)
		if !strings.Contains(msg.PlainText(), "Lightning Bolt") {
			t.Errorf("expected card name in result message, got: %q", msg.PlainText())
		}
		// The link carries the base64-encoded query as Deckbox's filter param.
		want := message.Link("trader1", "https://deckbox.org/sets/10?f=17Qm9sdA==")
		found := false
		for _, s := range msg {
			found = found || reflect.DeepEqual(s, want)
		}
		if !found {
			t.Errorf("expected a pre-filtered deckbox link %+v, got: %+v", want, msg)
		}
	})

	t.Run("contacts reach the message as data", func(t *testing.T) {
		a := agg(20, "alice_db", map[string]int16{"Shock": 1}, 1, 1)
		a.Contacts = []deckbox.Contact{
			{Platform: deckbox.PlatformTelegram, UserID: 12345, Handle: ptr("alice")},
			{Platform: deckbox.PlatformDiscord, UserID: 900000000000000001, Handle: ptr("alice_mtg")},
		}
		result := deckbox.SearchResult{SearchQueries: []string{"Shock"}, Aggregates: []deckbox.UserSearchAggregate{a}}

		msg, _ := chat.RenderSearch(result, "en", deckbox.ScopeTradelist)
		span, ok := contactsSpan(msg)
		if !ok || !reflect.DeepEqual(span.Contacts, a.Contacts) {
			t.Errorf("expected a Contacts span carrying every Contact, got: %+v", msg)
		}
		if !strings.Contains(msg.PlainText(), "alice_db from alice, alice_mtg:\n") {
			t.Errorf("expected the owner clause, got: %q", msg.PlainText())
		}
	})

	t.Run("no reachable contact, no owner clause", func(t *testing.T) {
		// A Contact without a Handle renders as nothing on every Platform, so a
		// dangling " from " would be all that is left of it.
		a := agg(21, "alice_db", map[string]int16{"Shock": 1}, 1, 1)
		a.Contacts = []deckbox.Contact{{Platform: deckbox.PlatformTelegram, UserID: 7}}
		result := deckbox.SearchResult{SearchQueries: []string{"Shock"}, Aggregates: []deckbox.UserSearchAggregate{a}}

		msg, _ := chat.RenderSearch(result, "en", deckbox.ScopeTradelist)
		if _, ok := contactsSpan(msg); ok || strings.Contains(msg.PlainText(), " from ") {
			t.Errorf("expected no owner clause, got: %q", msg.PlainText())
		}
	})

	t.Run("owner mention is translated", func(t *testing.T) {
		a := agg(1, "bob_db", map[string]int16{"Opt": 1}, 1, 1)
		a.Contacts = []deckbox.Contact{{Platform: deckbox.PlatformTelegram, UserID: 7, Handle: ptr("bob")}}
		result := deckbox.SearchResult{
			SearchQueries: []string{"Opt"},
			Aggregates:    []deckbox.UserSearchAggregate{a},
		}

		en, _ := chat.RenderSearch(result, "en", deckbox.ScopeTradelist)
		ru, _ := chat.RenderSearch(result, "ru", deckbox.ScopeTradelist)
		if !strings.Contains(en.PlainText(), " from bob") {
			t.Errorf("expected English owner mention, got: %q", en.PlainText())
		}
		if !strings.Contains(ru.PlainText(), " у bob") {
			t.Errorf("expected Russian owner mention, got: %q", ru.PlainText())
		}
	})

	t.Run("card names are bold text, not markup", func(t *testing.T) {
		result := deckbox.SearchResult{SearchQueries: []string{"<Unknown>"}}
		msg, _ := chat.RenderSearch(result, "en", deckbox.ScopeTradelist)
		want := message.Message{message.Text("No results for: "), message.Bold("<Unknown>")}
		if !reflect.DeepEqual(msg, want) {
			t.Errorf("got %+v, want %+v", msg, want)
		}
	})
}

// --- multi-card rendering ---

func TestRenderMulti(t *testing.T) {
	t.Run("grouped format", func(t *testing.T) {
		first := agg(100, "alice_db", map[string]int16{"Bolt": 4, "Shock": 2, "Lava Spike": 1}, 3, 7)
		first.Contacts = []deckbox.Contact{{Platform: deckbox.PlatformTelegram, UserID: 42, Handle: ptr("alice")}}

		result := deckbox.SearchResult{
			SearchQueries: []string{"Bolt", "Shock", "Lava Spike"},
			Aggregates: []deckbox.UserSearchAggregate{
				first,
				agg(200, "bob", map[string]int16{"Bolt": 2}, 1, 2),
			},
			NotFound: []string{"Force of Will"},
		}
		mainMsg, notFound := chat.RenderSearch(result, "en", deckbox.ScopeTradelist)
		main := mainMsg.PlainText()

		if !strings.Contains(main, "alice_db") || !strings.Contains(main, "bob") {
			t.Errorf("main must mention both owners, got: %q", main)
		}
		if !strings.Contains(main, "3/3") || !strings.Contains(main, "1/3") {
			t.Errorf("main must contain uniqueCount/totalQueried headers, got: %q", main)
		}
		if strings.Index(main, "alice_db") > strings.Index(main, "bob") {
			t.Errorf("alice (higher UniqueCount) must come before bob, got: %q", main)
		}
		if !strings.Contains(main, "alice_db from alice — 3/3") {
			t.Errorf("expected owner mention, got: %q", main)
		}
		if strings.HasSuffix(main, "\n") {
			t.Errorf("main must not end in a newline, got: %q", main)
		}
		if notFound == nil || !strings.Contains(notFound.PlainText(), "Force of Will") {
			t.Errorf("expected not-found message, got: %q", notFound.PlainText())
		}
	})

	t.Run("all found returns nil notFound", func(t *testing.T) {
		result := deckbox.SearchResult{
			SearchQueries: []string{"Bolt", "Shock"},
			Aggregates: []deckbox.UserSearchAggregate{
				agg(1, "a", map[string]int16{"Bolt": 1, "Shock": 1}, 2, 2),
			},
		}
		_, notFound := chat.RenderSearch(result, "en", deckbox.ScopeTradelist)
		if notFound != nil {
			t.Errorf("expected nil notFound, got: %q", notFound.PlainText())
		}
	})

	t.Run("all not found returns no-results main", func(t *testing.T) {
		result := deckbox.SearchResult{
			SearchQueries: []string{"A", "B"},
			NotFound:      []string{"A", "B"},
		}
		main, notFound := chat.RenderSearch(result, "en", deckbox.ScopeTradelist)
		if got := main.PlainText(); got != "No results for any of the 2 cards." {
			t.Errorf("expected no-results header referencing count, got: %q", got)
		}
		if notFound != nil {
			t.Errorf("when everything not found, main carries the message; notFound should stay nil, got: %q", notFound.PlainText())
		}
	})

	t.Run("wishlist scope uses sell keys", func(t *testing.T) {
		result := deckbox.SearchResult{
			SearchQueries: []string{"A", "B"},
			NotFound:      []string{"A"},
			Aggregates: []deckbox.UserSearchAggregate{
				agg(1, "x", map[string]int16{"B": 1}, 1, 1),
			},
		}
		main, notFound := chat.RenderSearch(result, "en", deckbox.ScopeWishlist)
		if !strings.Contains(main.PlainText(), "Wishlist") {
			t.Errorf("wishlist scope should use sell header, got: %q", main.PlainText())
		}
		if !strings.Contains(notFound.PlainText(), "wishlist") {
			t.Errorf("wishlist not-found should mention wishlist, got: %q", notFound.PlainText())
		}
	})

	t.Run("card names are sorted", func(t *testing.T) {
		result := deckbox.SearchResult{
			SearchQueries: []string{"Zed", "Alpha", "Mid"},
			Aggregates: []deckbox.UserSearchAggregate{
				agg(1, "a", map[string]int16{"Zed": 1, "Alpha": 1, "Mid": 1}, 3, 3),
			},
		}
		mainMsg, _ := chat.RenderSearch(result, "en", deckbox.ScopeTradelist)
		main := mainMsg.PlainText()
		if strings.Index(main, "Alpha") > strings.Index(main, "Mid") || strings.Index(main, "Mid") > strings.Index(main, "Zed") {
			t.Errorf("expected card names in sorted order, got: %q", main)
		}
	})
}
