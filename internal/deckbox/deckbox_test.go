package deckbox_test

import (
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/dto"
	"context"
	"strings"
	"testing"
)

// Ensure context is used via mockOwnerStorage method signatures.

// --- CardList ---

func TestCardListAddCard(t *testing.T) {
	cl := deckbox.CardList{ListId: 1, Cards: make(map[string]int16)}

	cl.AddCard("Bolt", 2)
	qty, err := cl.GetCardQuantity("Bolt")
	if err != nil || qty != 2 {
		t.Fatalf("after first add: want qty=2 err=nil, got qty=%d err=%v", qty, err)
	}

	// Adding again should accumulate.
	cl.AddCard("Bolt", 3)
	qty, err = cl.GetCardQuantity("Bolt")
	if err != nil || qty != 5 {
		t.Fatalf("after second add: want qty=5 err=nil, got qty=%d err=%v", qty, err)
	}
}

func TestCardListGetCardQuantity(t *testing.T) {
	cl := deckbox.CardList{
		ListId: 1,
		Cards:  map[string]int16{"Shock": 1},
	}

	t.Run("found", func(t *testing.T) {
		qty, err := cl.GetCardQuantity("Shock")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if qty != 1 {
			t.Errorf("expected qty=1, got %d", qty)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := cl.GetCardQuantity("Counterspell")
		if err == nil {
			t.Fatal("expected error for missing card, got nil")
		}
	})
}

// --- FormatForTelegram ---

// mockOwnerStorage is a minimal DeckboxSaver for FormatForTelegram tests.
type mockOwnerStorage struct {
	ownerFn func(ctx context.Context, listId int64) (*deckbox.CardListOwnerInfo, error)
}

// Compile-time check so interface changes can't silently orphan the mock.
var _ deckbox.DeckboxSaver = (*mockOwnerStorage)(nil)

func (m *mockOwnerStorage) RegisterUser(ctx context.Context, user deckbox.BotUser) error {
	return nil
}
func (m *mockOwnerStorage) SaveDeckboxUser(ctx context.Context, user deckbox.DeckboxUser) error {
	return nil
}
func (m *mockOwnerStorage) SaveCardList(ctx context.Context, list deckbox.CardList) error {
	return nil
}
func (m *mockOwnerStorage) ClearCardList(ctx context.Context, listId int64) error { return nil }
func (m *mockOwnerStorage) SearchCard(ctx context.Context, cardName string, scope string, exact bool) ([]dto.CardSearchDTO, error) {
	return nil, nil
}
func (m *mockOwnerStorage) GetOwnerByListId(ctx context.Context, listId int64) (*deckbox.CardListOwnerInfo, error) {
	if m.ownerFn != nil {
		return m.ownerFn(ctx, listId)
	}
	return nil, nil
}
func (m *mockOwnerStorage) GetDeckboxUser(ctx context.Context, login string) (*deckbox.DeckboxUser, error) {
	return nil, nil
}
func (m *mockOwnerStorage) UpdateDeckboxUserTimestamp(ctx context.Context, login string, ts int64) error {
	return nil
}
func (m *mockOwnerStorage) GetAllDeckboxUsersWithOldLists(ctx context.Context, threshold int64) ([]string, error) {
	return nil, nil
}

func TestMessageFormatter(t *testing.T) {
	t.Run("no results contains query", func(t *testing.T) {
		result := deckbox.SearchCardResult{
			SearchQuery:   "Counterspell",
			SearchResults: nil,
		}
		msg := result.FormatForTelegram("en", deckbox.ScopeTradelist)
		if !strings.Contains(msg, "Counterspell") {
			t.Errorf("expected query in no-results message, got: %q", msg)
		}
	})

	t.Run("sell scope no results contains query", func(t *testing.T) {
		result := deckbox.SearchCardResult{
			SearchQuery:   "Black Lotus",
			SearchResults: nil,
		}
		msg := result.FormatForTelegram("en", deckbox.ScopeWishlist)
		if !strings.Contains(msg, "Black Lotus") {
			t.Errorf("expected query in no-results message, got: %q", msg)
		}
	})

	t.Run("with results contains card name", func(t *testing.T) {
		result := deckbox.SearchCardResult{
			SearchQuery: "Bolt",
			SearchResults: []deckbox.CardListWithOwner{
				{
					CardList:     deckbox.CardList{ListId: 10, Cards: map[string]int16{"Lightning Bolt": 4}},
					DeckboxLogin: "trader1",
				},
			},
		}
		msg := result.FormatForTelegram("en", deckbox.ScopeTradelist)
		if !strings.Contains(msg, "Lightning Bolt") {
			t.Errorf("expected card name in result message, got: %q", msg)
		}
		if !strings.Contains(msg, "trader1") {
			t.Errorf("expected owner login in result message, got: %q", msg)
		}
	})

	t.Run("multi-card grouped format", func(t *testing.T) {
		tgID := int64(42)
		tgUser := "alice"
		result := deckbox.MultiCardSearchResult{
			SearchQueries: []string{"Bolt", "Shock", "Lava Spike"},
			Aggregates: []deckbox.UserSearchAggregate{
				{
					ListId:           100,
					DeckboxLogin:     "alice_db",
					TelegramID:       &tgID,
					TelegramUsername: &tgUser,
					FoundCards:       map[string]int16{"Bolt": 4, "Shock": 2, "Lava Spike": 1},
					UniqueCount:      3,
					TotalQuantity:    7,
				},
				{
					ListId:        200,
					DeckboxLogin:  "bob",
					FoundCards:    map[string]int16{"Bolt": 2},
					UniqueCount:   1,
					TotalQuantity: 2,
				},
			},
			NotFound: []string{"Force of Will"},
		}
		main, notFound := result.FormatForTelegram("en", deckbox.ScopeTradelist)

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

	t.Run("multi-card all found returns empty notFound", func(t *testing.T) {
		result := deckbox.MultiCardSearchResult{
			SearchQueries: []string{"Bolt"},
			Aggregates: []deckbox.UserSearchAggregate{
				{
					ListId:        1,
					DeckboxLogin:  "a",
					FoundCards:    map[string]int16{"Bolt": 1},
					UniqueCount:   1,
					TotalQuantity: 1,
				},
			},
		}
		_, notFound := result.FormatForTelegram("en", deckbox.ScopeTradelist)
		if notFound != "" {
			t.Errorf("expected empty notFound, got: %q", notFound)
		}
	})

	t.Run("multi-card all not found returns no-results main", func(t *testing.T) {
		result := deckbox.MultiCardSearchResult{
			SearchQueries: []string{"A", "B"},
			Aggregates:    nil,
			NotFound:      []string{"A", "B"},
		}
		main, notFound := result.FormatForTelegram("en", deckbox.ScopeTradelist)
		if !strings.Contains(main, "No results") && !strings.Contains(main, "2") {
			t.Errorf("expected no-results header referencing count, got: %q", main)
		}
		if notFound != "" {
			t.Errorf("when everything not found, main carries the message; notFound should stay empty, got: %q", notFound)
		}
	})

	t.Run("multi-card wishlist scope uses sell keys", func(t *testing.T) {
		result := deckbox.MultiCardSearchResult{
			SearchQueries: []string{"A", "B"},
			NotFound:      []string{"A"},
			Aggregates: []deckbox.UserSearchAggregate{
				{
					ListId:        1,
					DeckboxLogin:  "x",
					FoundCards:    map[string]int16{"B": 1},
					UniqueCount:   1,
					TotalQuantity: 1,
				},
			},
		}
		main, notFound := result.FormatForTelegram("en", deckbox.ScopeWishlist)
		if !strings.Contains(main, "Wishlist") {
			t.Errorf("wishlist scope should use sell header, got: %q", main)
		}
		if !strings.Contains(notFound, "wishlist") {
			t.Errorf("wishlist not-found should mention wishlist, got: %q", notFound)
		}
	})

	t.Run("with telegram user includes tg link", func(t *testing.T) {
		tgID := int64(12345)
		tgUser := "alice"
		result := deckbox.SearchCardResult{
			SearchQuery: "Shock",
			SearchResults: []deckbox.CardListWithOwner{
				{
					CardList:         deckbox.CardList{ListId: 20, Cards: map[string]int16{"Shock": 1}},
					DeckboxLogin:     "alice_db",
					TelegramID:       &tgID,
					TelegramUsername: &tgUser,
				},
			},
		}
		msg := result.FormatForTelegram("en", deckbox.ScopeTradelist)
		if !strings.Contains(msg, "alice") {
			t.Errorf("expected telegram username in result, got: %q", msg)
		}
	})
}
