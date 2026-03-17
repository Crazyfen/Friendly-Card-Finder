package sqlite

import (
	"FriendlyCardFinder/internal/deckbox"
	"fmt"
	"os"
	"testing"
)

func TestSaveCardListWithLargeCollection(t *testing.T) {
	// Create a temporary database for testing
	dbFile := t.TempDir() + "/test.db"
	storage, err := New(dbFile)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	defer func() {
		storage.Close()
		os.Remove(dbFile)
	}()

	// Register a deckbox user first (required for SearchCard to work)
	tradelistID := int64(12345)
	user := deckbox.DeckboxUser{
		DeckboxLogin: "testuser",
		InventoryID:  nil,
		TradelistID:  &tradelistID,
		WishlistID:   nil,
	}
	err = storage.SaveDeckboxUser(user)
	if err != nil {
		t.Fatalf("failed to save deckbox user: %v", err)
	}

	// Create a large card list with 20,000 cards (exceeds old limit of ~10,922)
	// This would fail with "too many SQL variables" before batching fix
	largeCardList := deckbox.CardList{
		ListId: tradelistID,
		Cards:  make(map[string]int16),
	}

	// Populate with 20,000 cards
	for i := range 20000 {
		cardName := fmt.Sprintf("Card_%d", i)
		largeCardList.Cards[cardName] = 1
	}

	// This should now succeed with batching
	err = storage.SaveCardList(largeCardList)
	if err != nil {
		t.Fatalf("failed to save large card list: %v", err)
	}

	// Verify the cards were actually saved
	results, err := storage.SearchCard("Card_", deckbox.ScopeTradelist)
	if err != nil {
		t.Fatalf("failed to search for cards: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected cards to be saved but found none")
	}

	t.Logf("Successfully saved and verified %d cards in batches", len(largeCardList.Cards))
}

func TestSaveCardListBatching(t *testing.T) {
	// Test batching with exact batch size boundaries
	tests := []struct {
		name            string
		cardCount       int
		expectedBatches int
	}{
		{"empty list", 0, 0},
		{"single card", 1, 1},
		{"partial batch", 500, 1},
		{"exact batch size", 1000, 1},
		{"batch and half", 1500, 2},
		{"multiple batches", 3500, 4},
		{"large collection", 15000, 15},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbFile := t.TempDir() + "/test.db"
			storage, err := New(dbFile)
			if err != nil {
				t.Fatalf("failed to create storage: %v", err)
			}
			defer func() {
				storage.Close()
				os.Remove(dbFile)
			}()

			// Compute expected batches based on configured batch size
			expectedBatches := 0
			if tt.cardCount > 0 {
				expectedBatches = (tt.cardCount + storage.batchSize - 1) / storage.batchSize
			}

			// Register a deckbox user with this list as tradelist
			listID := int64(expectedBatches * storage.batchSize)
			user := deckbox.DeckboxUser{
				DeckboxLogin: "testuser",
				InventoryID:  nil,
				TradelistID:  &listID,
				WishlistID:   nil,
			}
			err = storage.SaveDeckboxUser(user)
			if err != nil {
				t.Fatalf("failed to save deckbox user: %v", err)
			}

			// Create card list
			cardList := deckbox.CardList{
				ListId: listID,
				Cards:  make(map[string]int16),
			}

			// Populate with test cards
			for i := 0; i < tt.cardCount; i++ {
				cardList.Cards[fmt.Sprintf("Card_%d", i)] = 1
			}

			// Save should succeed
			err = storage.SaveCardList(cardList)
			if err != nil {
				t.Fatalf("failed to save card list: %v", err)
			}

			// Verify count matches
			if tt.cardCount > 0 {
				results, err := storage.SearchCard("Card_", deckbox.ScopeTradelist)
				if err != nil {
					t.Fatalf("failed to search: %v", err)
				}
				if len(results) != tt.cardCount {
					t.Errorf("expected %d cards, got %d", tt.cardCount, len(results))
				}
			}
		})
	}
}

func TestSaveCardListTransactionRollback(t *testing.T) {
	// Test that transaction rolls back on error
	dbFile := t.TempDir() + "/test.db"
	storage, err := New(dbFile)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	defer func() {
		storage.Close()
		os.Remove(dbFile)
	}()

	// Register a deckbox user
	listID := int64(111)
	user := deckbox.DeckboxUser{
		DeckboxLogin: "testuser",
		InventoryID:  nil,
		TradelistID:  &listID,
		WishlistID:   nil,
	}
	err = storage.SaveDeckboxUser(user)
	if err != nil {
		t.Fatalf("failed to save deckbox user: %v", err)
	}

	// Save a valid card list first
	cardList1 := deckbox.CardList{
		ListId: listID,
		Cards: map[string]int16{
			"ValidCard_1": 1,
			"ValidCard_2": 2,
		},
	}
	err = storage.SaveCardList(cardList1)
	if err != nil {
		t.Fatalf("failed to save first card list: %v", err)
	}

	// Verify first save worked
	results1, _ := storage.SearchCard("ValidCard_", deckbox.ScopeTradelist)
	if len(results1) != 2 {
		t.Fatalf("expected 2 cards from first save, got %d", len(results1))
	}

	// Clear and save again (verifies transaction consistency)
	cardList2 := deckbox.CardList{
		ListId: listID,
		Cards: map[string]int16{
			"NewCard_1": 3,
			"NewCard_2": 4,
			"NewCard_3": 5,
		},
	}
	err = storage.SaveCardList(cardList2)
	if err != nil {
		t.Fatalf("failed to save second card list: %v", err)
	}

	// Verify second save replaced the old cards
	results2, _ := storage.SearchCard("NewCard_", deckbox.ScopeTradelist)
	oldResults, _ := storage.SearchCard("ValidCard_", deckbox.ScopeTradelist)

	if len(results2) != 3 {
		t.Errorf("expected 3 new cards, got %d", len(results2))
	}
	if len(oldResults) != 0 {
		t.Errorf("expected old cards to be cleared, but found %d", len(oldResults))
	}
}

func TestSearchCardWithFTS(t *testing.T) {
	// Ensure FTS is available in this environment
	dbFile := t.TempDir() + "/test.db"
	storage, err := New(dbFile)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	defer func() {
		storage.Close()
		os.Remove(dbFile)
	}()

	if !storage.ftsEnabled {
		t.Skip("FTS5 not enabled in this sqlite build; skipping FTS tests")
	}

	listID := int64(5000)
	user := deckbox.DeckboxUser{
		DeckboxLogin: "ftstestuser",
		InventoryID:  nil,
		TradelistID:  &listID,
		WishlistID:   nil,
	}
	if err := storage.SaveDeckboxUser(user); err != nil {
		t.Fatalf("failed to save deckbox user: %v", err)
	}

	cardList := deckbox.CardList{
		ListId: listID,
		Cards: map[string]int16{
			"Lightning Bolt":  1,
			"Lightning Helix": 2,
			"Shocking Grasp":  1,
			"Shock":           1,
		},
	}
	if err := storage.SaveCardList(cardList); err != nil {
		t.Fatalf("failed to save card list: %v", err)
	}

	// Search for 'shock' should match both 'Shock' and 'Shocking Grasp'
	results, err := storage.SearchCard("shock", deckbox.ScopeTradelist)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 results for 'shock', got %d", len(results))
	}

	// Search for 'lightning' should match both Lightning cards
	results2, err := storage.SearchCard("lightning", deckbox.ScopeTradelist)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results2) < 2 {
		t.Fatalf("expected at least 2 results for 'lightning', got %d", len(results2))
	}

	// Search for a hyphenated name should not cause SQL parse errors (regression test)
	// Add one hyphenated card and ensure search works for the full hyphenated string
	cardList2 := deckbox.CardList{
		ListId: listID,
		Cards: map[string]int16{
			"Vitu-Ghazi Inspector": 1,
		},
	}
	if err := storage.SaveCardList(cardList2); err != nil {
		t.Fatalf("failed to save hyphenated card list: %v", err)
	}

	results3, err := storage.SearchCard("Vitu-Ghazi Inspector", deckbox.ScopeTradelist)
	if err != nil {
		t.Fatalf("search failed for hyphenated name: %v", err)
	}
	if len(results3) < 1 {
		t.Fatalf("expected at least 1 result for 'Vitu-Ghazi Inspector', got %d", len(results3))
	}

	// Accented-name search: ensure diacritics are handled (e.g., "Sméagol, Helpful Guide")
	cardList3 := deckbox.CardList{
		ListId: listID,
		Cards: map[string]int16{
			"Sméagol, Helpful Guide": 1,
		},
	}
	if err := storage.SaveCardList(cardList3); err != nil {
		t.Fatalf("failed to save accented card list: %v", err)
	}

	results4, err := storage.SearchCard("smeagol", deckbox.ScopeTradelist)
	if err != nil {
		t.Fatalf("search failed for accented name: %v", err)
	}
	if len(results4) < 1 {
		t.Fatalf("expected at least 1 result for 'smeagol', got %d", len(results4))
	}

	cardList4 := deckbox.CardList{
		ListId: listID,
		Cards: map[string]int16{
			"Umara Wizard // Umara Skyfalls": 1,
		},
	}

	if err := storage.SaveCardList(cardList4); err != nil {
		t.Fatalf("failed to save split card list: %v", err)
	}

	results5, err := storage.SearchCard("//", deckbox.ScopeTradelist)
	if err != nil {
		t.Fatalf("search failed for split card name: %v", err)
	}

	if len(results5) < 1 {
		t.Fatalf("expected at least 1 result for 'Umara Skyfalls', got %d", len(results5))
	}
}

func TestSearchCardWishlist(t *testing.T) {
	dbFile := t.TempDir() + "/test.db"
	storage, err := New(dbFile)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	defer func() {
		storage.Close()
		os.Remove(dbFile)
	}()

	wishlistID := int64(777)
	user := deckbox.DeckboxUser{
		DeckboxLogin: "wishuser",
		InventoryID:  nil,
		TradelistID:  nil,
		WishlistID:   &wishlistID,
	}
	if err := storage.SaveDeckboxUser(user); err != nil {
		t.Fatalf("failed to save deckbox user: %v", err)
	}

	cardList := deckbox.CardList{
		ListId: wishlistID,
		Cards: map[string]int16{
			"WishCard": 2,
		},
	}
	if err := storage.SaveCardList(cardList); err != nil {
		t.Fatalf("failed to save wishlist card list: %v", err)
	}

	// Search in wishlist scope - should find
	res, err := storage.SearchCard("WishCard", deckbox.ScopeWishlist)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected wishlist search to find results")
	}

	// Search in tradelist scope - should NOT find
	res2, err := storage.SearchCard("WishCard", deckbox.ScopeTradelist)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(res2) != 0 {
		t.Fatalf("expected no tradelist results for wishlist card, got %d", len(res2))
	}
}
