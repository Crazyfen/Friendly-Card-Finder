package telegram_test

import (
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/telegram"
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

// --- single-card rendering (the n=1 case) ---

func TestRenderSingle(t *testing.T) {
	t.Run("no results contains query", func(t *testing.T) {
		result := deckbox.SearchResult{
			SearchQueries: []string{"Counterspell"},
			NotFound:      []string{"Counterspell"},
		}
		msg, notFound := telegram.RenderSearch(result, "en", deckbox.ScopeTradelist)
		if !strings.Contains(msg, "Counterspell") {
			t.Errorf("expected query in no-results message, got: %q", msg)
		}
		if notFound != "" {
			t.Errorf("single-card search carries everything in the main message, got notFound: %q", notFound)
		}
	})

	t.Run("sell scope no results contains query", func(t *testing.T) {
		result := deckbox.SearchResult{
			SearchQueries: []string{"Black Lotus"},
			NotFound:      []string{"Black Lotus"},
		}
		msg, _ := telegram.RenderSearch(result, "en", deckbox.ScopeWishlist)
		if !strings.Contains(msg, "Black Lotus") {
			t.Errorf("expected query in no-results message, got: %q", msg)
		}
	})

	t.Run("with results contains card name and owner", func(t *testing.T) {
		result := deckbox.SearchResult{
			SearchQueries: []string{"Bolt"},
			Aggregates: []deckbox.UserSearchAggregate{
				agg(10, "trader1", map[string]int16{"Lightning Bolt": 4}, 1, 4),
			},
		}
		msg, _ := telegram.RenderSearch(result, "en", deckbox.ScopeTradelist)
		if !strings.Contains(msg, "Lightning Bolt") {
			t.Errorf("expected card name in result message, got: %q", msg)
		}
		if !strings.Contains(msg, "trader1") {
			t.Errorf("expected owner login in result message, got: %q", msg)
		}
		// The link carries the base64-encoded query as Deckbox's filter param.
		if !strings.Contains(msg, "?f=17") {
			t.Errorf("expected pre-filtered deckbox link, got: %q", msg)
		}
	})

	t.Run("with telegram user includes tg link", func(t *testing.T) {
		tgID := int64(12345)
		tgUser := "alice"
		a := agg(20, "alice_db", map[string]int16{"Shock": 1}, 1, 1)
		a.TelegramID = &tgID
		a.TelegramUsername = &tgUser

		result := deckbox.SearchResult{
			SearchQueries: []string{"Shock"},
			Aggregates:    []deckbox.UserSearchAggregate{a},
		}
		msg, _ := telegram.RenderSearch(result, "en", deckbox.ScopeTradelist)
		if !strings.Contains(msg, "tg://user?id=12345") {
			t.Errorf("expected telegram deep link, got: %q", msg)
		}
		if !strings.Contains(msg, "@alice") {
			t.Errorf("expected telegram username in result, got: %q", msg)
		}
	})

	t.Run("owner mention is translated", func(t *testing.T) {
		tgID := int64(7)
		tgUser := "bob"
		a := agg(1, "bob_db", map[string]int16{"Opt": 1}, 1, 1)
		a.TelegramID = &tgID
		a.TelegramUsername = &tgUser
		result := deckbox.SearchResult{
			SearchQueries: []string{"Opt"},
			Aggregates:    []deckbox.UserSearchAggregate{a},
		}

		en, _ := telegram.RenderSearch(result, "en", deckbox.ScopeTradelist)
		ru, _ := telegram.RenderSearch(result, "ru", deckbox.ScopeTradelist)
		if en == ru {
			t.Errorf("owner mention must follow the user's language, got identical output: %q", en)
		}
		if !strings.Contains(en, " from ") {
			t.Errorf("expected English owner mention, got: %q", en)
		}
		if !strings.Contains(ru, " у ") {
			t.Errorf("expected Russian owner mention, got: %q", ru)
		}
	})
}

// --- multi-card rendering ---

func TestRenderMulti(t *testing.T) {
	t.Run("grouped format", func(t *testing.T) {
		tgID := int64(42)
		tgUser := "alice"
		first := agg(100, "alice_db", map[string]int16{"Bolt": 4, "Shock": 2, "Lava Spike": 1}, 3, 7)
		first.TelegramID = &tgID
		first.TelegramUsername = &tgUser

		result := deckbox.SearchResult{
			SearchQueries: []string{"Bolt", "Shock", "Lava Spike"},
			Aggregates: []deckbox.UserSearchAggregate{
				first,
				agg(200, "bob", map[string]int16{"Bolt": 2}, 1, 2),
			},
			NotFound: []string{"Force of Will"},
		}
		main, notFound := telegram.RenderSearch(result, "en", deckbox.ScopeTradelist)

		if !strings.Contains(main, "alice_db") || !strings.Contains(main, "bob") {
			t.Errorf("main must mention both owners, got: %q", main)
		}
		if !strings.Contains(main, "3/3") || !strings.Contains(main, "1/3") {
			t.Errorf("main must contain uniqueCount/totalQueried headers, got: %q", main)
		}
		if strings.Index(main, "alice_db") > strings.Index(main, "bob") {
			t.Errorf("alice (higher UniqueCount) must come before bob, got: %q", main)
		}
		if !strings.Contains(main, "@alice") {
			t.Errorf("expected telegram mention, got: %q", main)
		}
		if notFound == "" || !strings.Contains(notFound, "Force of Will") {
			t.Errorf("expected not-found message, got: %q", notFound)
		}
	})

	t.Run("all found returns empty notFound", func(t *testing.T) {
		result := deckbox.SearchResult{
			SearchQueries: []string{"Bolt", "Shock"},
			Aggregates: []deckbox.UserSearchAggregate{
				agg(1, "a", map[string]int16{"Bolt": 1, "Shock": 1}, 2, 2),
			},
		}
		_, notFound := telegram.RenderSearch(result, "en", deckbox.ScopeTradelist)
		if notFound != "" {
			t.Errorf("expected empty notFound, got: %q", notFound)
		}
	})

	t.Run("all not found returns no-results main", func(t *testing.T) {
		result := deckbox.SearchResult{
			SearchQueries: []string{"A", "B"},
			NotFound:      []string{"A", "B"},
		}
		main, notFound := telegram.RenderSearch(result, "en", deckbox.ScopeTradelist)
		if !strings.Contains(main, "No results") && !strings.Contains(main, "2") {
			t.Errorf("expected no-results header referencing count, got: %q", main)
		}
		if notFound != "" {
			t.Errorf("when everything not found, main carries the message; notFound should stay empty, got: %q", notFound)
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
		main, notFound := telegram.RenderSearch(result, "en", deckbox.ScopeWishlist)
		if !strings.Contains(main, "Wishlist") {
			t.Errorf("wishlist scope should use sell header, got: %q", main)
		}
		if !strings.Contains(notFound, "wishlist") {
			t.Errorf("wishlist not-found should mention wishlist, got: %q", notFound)
		}
	})

	t.Run("card names are sorted", func(t *testing.T) {
		result := deckbox.SearchResult{
			SearchQueries: []string{"Zed", "Alpha", "Mid"},
			Aggregates: []deckbox.UserSearchAggregate{
				agg(1, "a", map[string]int16{"Zed": 1, "Alpha": 1, "Mid": 1}, 3, 3),
			},
		}
		main, _ := telegram.RenderSearch(result, "en", deckbox.ScopeTradelist)
		if strings.Index(main, "Alpha") > strings.Index(main, "Mid") || strings.Index(main, "Mid") > strings.Index(main, "Zed") {
			t.Errorf("expected card names in sorted order, got: %q", main)
		}
	})
}
