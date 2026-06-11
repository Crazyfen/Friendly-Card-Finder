package sqlite

import (
	"FriendlyCardFinder/internal/deckbox"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

// newTestDB creates a SQLiteStorage backed by a temp file and registers cleanup.
func newTestDB(t *testing.T) *SQLiteStorage {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := New(t.TempDir()+"/test.db", log)
	if err != nil {
		t.Fatalf("failed to create test storage: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Benchmarking notes:
//
// 1) Run benchmarks (baseline):
//    go test -run ^$ -bench . -benchmem ./internal/storage/sqlite
//
// 2) Run again with FTS5 enabled (if your sqlite build supports it):
//    go test -tags "fts5" -run ^$ -bench . -benchmem ./internal/storage/sqlite
//
// 3) Profile CPU usage (sample):
//    go test -run ^$ -bench BenchmarkSaveCardList -cpuprofile cpu.prof ./internal/storage/sqlite
//    go tool pprof -http=:8080 cpu.prof
//
// 4) Compare results using benchstat:
//    benchstat before.txt after.txt
//
// Example benchstat output:
//    name                         old time/op    new time/op    delta
//    BenchmarkSearchCard_FTS...    12.0ms ± 3%    7.5ms ± 2%     -37.5%
//
// (Requires `benchstat`, e.g. `go install golang.org/x/perf/cmd/benchstat@latest`)

// TestFtsSchemaUpgrade simulates a database created before quantity was stored
// in the FTS table: the old 3-column card_lists_fts must be dropped, recreated
// with the new schema, and repopulated from card_lists on startup.
func TestFtsSchemaUpgrade(t *testing.T) {
	ctx := context.Background()
	dbFile := t.TempDir() + "/test.db"
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	s, err := New(dbFile, log)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	if !s.ftsEnabled {
		s.Close()
		t.Skip("skipping: FTS5 not available in this build")
	}

	tradelistID := int64(777)
	if err := s.SaveDeckboxUser(ctx, deckbox.DeckboxUser{DeckboxLogin: "upgradeuser", TradelistID: &tradelistID}); err != nil {
		t.Fatalf("failed to save deckbox user: %v", err)
	}
	if err := s.SaveCardList(ctx, deckbox.CardList{
		ListId: tradelistID,
		Cards:  map[string]int16{"Lightning Bolt": 4, "Sméagol, Helpful Guide": 2},
	}); err != nil {
		t.Fatalf("failed to save card list: %v", err)
	}

	// Downgrade the FTS table to the old 3-column schema (without quantity).
	if _, err := s.db.writeDB.Exec(`DROP TABLE card_lists_fts`); err != nil {
		t.Fatalf("failed to drop fts table: %v", err)
	}
	if _, err := s.db.writeDB.Exec(`CREATE VIRTUAL TABLE card_lists_fts USING fts5(listId UNINDEXED, cardName, cardName_normalized);`); err != nil {
		t.Fatalf("failed to create old-schema fts table: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("failed to close storage: %v", err)
	}

	// Reopen: ensureFtsTable must detect the old schema and rebuild.
	s2, err := New(dbFile, log)
	if err != nil {
		t.Fatalf("failed to reopen storage: %v", err)
	}
	defer s2.Close()
	if !s2.ftsEnabled {
		t.Fatal("expected FTS to be enabled after schema upgrade")
	}

	for name, wantQty := range map[string]int16{"Lightning Bolt": 4, "Smeagol": 2} {
		results, err := s2.SearchCard(ctx, name, deckbox.ScopeTradelist)
		if err != nil {
			t.Fatalf("search %q failed: %v", name, err)
		}
		if len(results) != 1 {
			t.Fatalf("search %q: got %d results, want 1", name, len(results))
		}
		if results[0].Quantity != wantQty {
			t.Errorf("search %q: got quantity %d, want %d", name, results[0].Quantity, wantQty)
		}
	}
}

func TestSaveCardListWithLargeCollection(t *testing.T) {
	// Create a temporary database for testing
	dbFile := t.TempDir() + "/test.db"
	storage, err := New(dbFile, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	defer func() {
		storage.Close()
		os.Remove(dbFile)
	}()

	ctx := context.Background()

	// Register a deckbox user first (required for SearchCard to work)
	tradelistID := int64(12345)
	user := deckbox.DeckboxUser{
		DeckboxLogin: "testuser",
		InventoryID:  nil,
		TradelistID:  &tradelistID,
		WishlistID:   nil,
	}
	err = storage.SaveDeckboxUser(ctx, user)
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
	err = storage.SaveCardList(ctx, largeCardList)
	if err != nil {
		t.Fatalf("failed to save large card list: %v", err)
	}

	// Verify the cards were actually saved
	results, err := storage.SearchCard(ctx, "Card_", deckbox.ScopeTradelist)
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
		name      string
		cardCount int
	}{
		{"empty list", 0},
		{"single card", 1},
		{"partial batch", 500},
		{"exact batch size", 1000},
		{"batch and half", 1500},
		{"multiple batches", 3500},
		{"large collection", 15000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbFile := t.TempDir() + "/test.db"
			storage, err := New(dbFile, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatalf("failed to create storage: %v", err)
			}
			ctx := context.Background()
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
			err = storage.SaveDeckboxUser(ctx, user)
			if err != nil {
				t.Fatalf("failed to save deckbox user: %v", err)
			}

			// Populate with test cards
			cardList := deckbox.CardList{
				ListId: listID,
				Cards:  make(map[string]int16),
			}
			for i := 0; i < tt.cardCount; i++ {
				cardList.Cards[fmt.Sprintf("Card_%d", i)] = 1
			}

			// Save should succeed
			if err := storage.SaveCardList(ctx, cardList); err != nil {
				t.Fatalf("failed to save card list: %v", err)
			}

			// Verify count matches
			if tt.cardCount > 0 {
				results, err := storage.SearchCard(ctx, "Card_", deckbox.ScopeTradelist)
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
	storage, err := New(dbFile, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	ctx := context.Background()
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
	err = storage.SaveDeckboxUser(ctx, user)
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
	err = storage.SaveCardList(ctx, cardList1)
	if err != nil {
		t.Fatalf("failed to save first card list: %v", err)
	}

	// Verify first save worked
	results1, _ := storage.SearchCard(ctx, "ValidCard_", deckbox.ScopeTradelist)
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
	err = storage.SaveCardList(ctx, cardList2)
	if err != nil {
		t.Fatalf("failed to save second card list: %v", err)
	}

	// Verify second save replaced the old cards
	results2, _ := storage.SearchCard(ctx, "NewCard_", deckbox.ScopeTradelist)
	oldResults, _ := storage.SearchCard(ctx, "ValidCard_", deckbox.ScopeTradelist)

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
	storage, err := New(dbFile, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	ctx := context.Background()
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
	if err := storage.SaveDeckboxUser(ctx, user); err != nil {
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
	if err := storage.SaveCardList(ctx, cardList); err != nil {
		t.Fatalf("failed to save card list: %v", err)
	}

	// Search for 'shock' should match both 'Shock' and 'Shocking Grasp'
	results, err := storage.SearchCard(ctx, "shock", deckbox.ScopeTradelist)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 results for 'shock', got %d", len(results))
	}

	// Search for 'lightning' should match both Lightning cards
	results2, err := storage.SearchCard(ctx, "lightning", deckbox.ScopeTradelist)
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
	if err := storage.SaveCardList(ctx, cardList2); err != nil {
		t.Fatalf("failed to save hyphenated card list: %v", err)
	}

	results3, err := storage.SearchCard(ctx, "Vitu-Ghazi Inspector", deckbox.ScopeTradelist)
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
	if err := storage.SaveCardList(ctx, cardList3); err != nil {
		t.Fatalf("failed to save accented card list: %v", err)
	}

	results4, err := storage.SearchCard(ctx, "smeagol", deckbox.ScopeTradelist)
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

	if err := storage.SaveCardList(ctx, cardList4); err != nil {
		t.Fatalf("failed to save split card list: %v", err)
	}

	results5, err := storage.SearchCard(ctx, "//", deckbox.ScopeTradelist)
	if err != nil {
		t.Fatalf("search failed for split card name: %v", err)
	}

	if len(results5) < 1 {
		t.Fatalf("expected at least 1 result for 'Umara Skyfalls', got %d", len(results5))
	}
}

func TestSearchCardWishlist(t *testing.T) {
	dbFile := t.TempDir() + "/test.db"
	storage, err := New(dbFile, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	ctx := context.Background()
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
	if err := storage.SaveDeckboxUser(ctx, user); err != nil {
		t.Fatalf("failed to save deckbox user: %v", err)
	}

	cardList := deckbox.CardList{
		ListId: wishlistID,
		Cards: map[string]int16{
			"WishCard": 2,
		},
	}
	if err := storage.SaveCardList(ctx, cardList); err != nil {
		t.Fatalf("failed to save wishlist card list: %v", err)
	}

	// Search in wishlist scope - should find
	res, err := storage.SearchCard(ctx, "WishCard", deckbox.ScopeWishlist)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected wishlist search to find results")
	}

	// Search in tradelist scope - should NOT find
	res2, err := storage.SearchCard(ctx, "WishCard", deckbox.ScopeTradelist)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(res2) != 0 {
		t.Fatalf("expected no tradelist results for wishlist card, got %d", len(res2))
	}
}

// --- RegisterUser ---

func TestRegisterUser(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		s := newTestDB(t)
		err := s.RegisterUser(ctx, deckbox.BotUser{
			TelegramID:       1001,
			TelegramUsername: "alice",
			DeckboxLogin:     "alice_deckbox",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate returns error", func(t *testing.T) {
		s := newTestDB(t)
		user := deckbox.BotUser{TelegramID: 1002, TelegramUsername: "bob", DeckboxLogin: "bob_deckbox"}
		if err := s.RegisterUser(ctx, user); err != nil {
			t.Fatalf("first insert failed: %v", err)
		}
		if err := s.RegisterUser(ctx, user); err == nil {
			t.Fatal("expected error on duplicate insert, got nil")
		}
	})
}

// --- GetDeckboxUser ---

func TestGetDeckboxUser(t *testing.T) {
	ctx := context.Background()

	t.Run("not found returns nil", func(t *testing.T) {
		s := newTestDB(t)
		got, err := s.GetDeckboxUser(ctx, "nobody")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil for unknown user, got %+v", got)
		}
	})

	t.Run("found returns correct data", func(t *testing.T) {
		s := newTestDB(t)
		invID := int64(10)
		tlID := int64(20)
		user := deckbox.DeckboxUser{
			DeckboxLogin: "carol",
			InventoryID:  &invID,
			TradelistID:  &tlID,
			WishlistID:   nil,
		}
		if err := s.SaveDeckboxUser(ctx, user); err != nil {
			t.Fatalf("save failed: %v", err)
		}
		got, err := s.GetDeckboxUser(ctx, "carol")
		if err != nil {
			t.Fatalf("get failed: %v", err)
		}
		if got == nil {
			t.Fatal("expected user, got nil")
		}
		if got.DeckboxLogin != "carol" || *got.InventoryID != invID || *got.TradelistID != tlID {
			t.Errorf("returned user mismatch: %+v", got)
		}
		if got.WishlistID != nil {
			t.Errorf("expected nil wishlistId, got %v", got.WishlistID)
		}
	})
}

// --- UpdateDeckboxUserTimestamp ---

func TestUpdateDeckboxUserTimestamp(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)

	user := deckbox.DeckboxUser{DeckboxLogin: "dave"}
	if err := s.SaveDeckboxUser(ctx, user); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Timestamp should be nil before update.
	got, _ := s.GetDeckboxUser(ctx, "dave")
	if got.UpdatedAt != nil {
		t.Fatalf("expected nil timestamp before update, got %v", got.UpdatedAt)
	}

	ts := time.Now().Unix()
	if err := s.UpdateDeckboxUserTimestamp(ctx, "dave", ts); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	got, _ = s.GetDeckboxUser(ctx, "dave")
	if got.UpdatedAt == nil {
		t.Fatal("expected timestamp after update, got nil")
	}
	if *got.UpdatedAt != ts {
		t.Errorf("expected ts=%d, got %d", ts, *got.UpdatedAt)
	}
}

// --- GetAllDeckboxUsersWithOldLists ---

func TestGetAllDeckboxUsersWithOldLists(t *testing.T) {
	ctx := context.Background()

	save := func(s *SQLiteStorage, login string, updatedAt *int64) {
		t.Helper()
		if err := s.SaveDeckboxUser(ctx, deckbox.DeckboxUser{DeckboxLogin: login}); err != nil {
			t.Fatalf("save %s: %v", login, err)
		}
		if updatedAt != nil {
			if err := s.UpdateDeckboxUserTimestamp(ctx, login, *updatedAt); err != nil {
				t.Fatalf("timestamp %s: %v", login, err)
			}
		}
	}

	ptr := func(i int64) *int64 { return &i }

	now := time.Now().Unix()
	threshold := now - 3600 // 1 hour ago

	tests := []struct {
		name       string
		setup      func(*SQLiteStorage)
		wantLogins []string
	}{
		{
			name: "nil timestamp always included",
			setup: func(s *SQLiteStorage) {
				save(s, "no_ts", nil)
			},
			wantLogins: []string{"no_ts"},
		},
		{
			name: "old timestamp included",
			setup: func(s *SQLiteStorage) {
				save(s, "old", ptr(now-7200)) // 2 hours ago
			},
			wantLogins: []string{"old"},
		},
		{
			name: "fresh timestamp excluded",
			setup: func(s *SQLiteStorage) {
				save(s, "fresh", ptr(now-60)) // 1 minute ago
			},
			wantLogins: []string{},
		},
		{
			name: "mixed: only stale and nil returned",
			setup: func(s *SQLiteStorage) {
				save(s, "nil_ts", nil)
				save(s, "stale", ptr(now-7200))
				save(s, "current", ptr(now-60))
			},
			wantLogins: []string{"nil_ts", "stale"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestDB(t)
			tt.setup(s)
			logins, err := s.GetAllDeckboxUsersWithOldLists(ctx, threshold)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := make(map[string]bool, len(logins))
			for _, l := range logins {
				got[l] = true
			}
			for _, want := range tt.wantLogins {
				if !got[want] {
					t.Errorf("expected %q in results, got %v", want, logins)
				}
			}
			if len(logins) != len(tt.wantLogins) {
				t.Errorf("expected %d results, got %d: %v", len(tt.wantLogins), len(logins), logins)
			}
		})
	}
}

// --- ClearCardList ---

func TestClearCardList(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)

	listID := int64(55)
	tl := listID
	if err := s.SaveDeckboxUser(ctx, deckbox.DeckboxUser{DeckboxLogin: "eve", TradelistID: &tl}); err != nil {
		t.Fatalf("save user: %v", err)
	}
	if err := s.SaveCardList(ctx, deckbox.CardList{ListId: listID, Cards: map[string]int16{"Bolt": 4}}); err != nil {
		t.Fatalf("save list: %v", err)
	}

	results, _ := s.SearchCard(ctx, "Bolt", deckbox.ScopeTradelist)
	if len(results) == 0 {
		t.Fatal("expected card before clear")
	}

	if err := s.ClearCardList(ctx, listID); err != nil {
		t.Fatalf("clear failed: %v", err)
	}

	results, _ = s.SearchCard(ctx, "Bolt", deckbox.ScopeTradelist)
	if len(results) != 0 {
		t.Fatalf("expected 0 results after clear, got %d", len(results))
	}
}

// --- GetOwnerByListId ---

func TestGetOwnerByListId(t *testing.T) {
	ctx := context.Background()

	t.Run("deckbox user only (no telegram user)", func(t *testing.T) {
		s := newTestDB(t)
		tl := int64(100)
		if err := s.SaveDeckboxUser(ctx, deckbox.DeckboxUser{DeckboxLogin: "frank", TradelistID: &tl}); err != nil {
			t.Fatalf("save failed: %v", err)
		}
		info, err := s.GetOwnerByListId(ctx, tl)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info == nil {
			t.Fatal("expected owner info, got nil")
		}
		if info.DeckboxLogin != "frank" {
			t.Errorf("expected 'frank', got %q", info.DeckboxLogin)
		}
		// No telegram user registered → both fields nil
		if info.TelegramID != nil || info.TelegramUsername != nil {
			t.Errorf("expected nil telegram fields, got id=%v username=%v", info.TelegramID, info.TelegramUsername)
		}
	})

	t.Run("with registered telegram user", func(t *testing.T) {
		s := newTestDB(t)
		tl := int64(200)
		if err := s.SaveDeckboxUser(ctx, deckbox.DeckboxUser{DeckboxLogin: "grace", TradelistID: &tl}); err != nil {
			t.Fatalf("save deckbox user: %v", err)
		}
		if err := s.RegisterUser(ctx, deckbox.BotUser{TelegramID: 9001, TelegramUsername: "grace_tg", DeckboxLogin: "grace"}); err != nil {
			t.Fatalf("register telegram user: %v", err)
		}
		info, err := s.GetOwnerByListId(ctx, tl)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.TelegramID == nil || *info.TelegramID != 9001 {
			t.Errorf("expected TelegramID=9001, got %v", info.TelegramID)
		}
		if info.TelegramUsername == nil || *info.TelegramUsername != "grace_tg" {
			t.Errorf("expected TelegramUsername='grace_tg', got %v", info.TelegramUsername)
		}
	})

	t.Run("unknown list id returns error", func(t *testing.T) {
		s := newTestDB(t)
		info, err := s.GetOwnerByListId(ctx, 99999)
		if err == nil {
			t.Fatal("expected error for unknown list id, got nil")
		}
		if info != nil {
			t.Fatalf("expected nil info on error, got %+v", info)
		}
	})
}

func BenchmarkSaveCardList(b *testing.B) {
	// Silence slog output to keep benchmark output clean.
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(oldLogger)

	// Setup once; benchmark SaveCardList performance.
	dbFile := b.TempDir() + "/bench.db"
	storage, err := New(dbFile, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		b.Fatalf("failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()
	tradelistID := int64(12345)
	user := deckbox.DeckboxUser{
		DeckboxLogin: "benchuser",
		InventoryID:  nil,
		TradelistID:  &tradelistID,
		WishlistID:   nil,
	}
	if err := storage.SaveDeckboxUser(ctx, user); err != nil {
		b.Fatalf("failed to save deckbox user: %v", err)
	}

	cardList := deckbox.CardList{
		ListId: tradelistID,
		Cards:  make(map[string]int16, 10000),
	}
	for i := 0; i < 10000; i++ {
		cardList.Cards[fmt.Sprintf("Card_%d", i)] = 1
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := storage.SaveCardList(ctx, cardList); err != nil {
			b.Fatalf("failed to save card list: %v", err)
		}
	}
}

func benchmarkSearchCard(b *testing.B, wantFTS bool) {
	// Silence slog output to keep benchmark output clean.
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(oldLogger)

	// Setup once; benchmark SearchCard performance.
	dbFile := b.TempDir() + "/bench.db"
	storage, err := New(dbFile, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		b.Fatalf("failed to create storage: %v", err)
	}
	defer storage.Close()

	if storage.ftsEnabled != wantFTS {
		b.Skipf("skipping: ftsEnabled=%v (want %v)", storage.ftsEnabled, wantFTS)
	}

	ctx := context.Background()
	tradelistID := int64(12345)
	user := deckbox.DeckboxUser{
		DeckboxLogin: "benchuser",
		InventoryID:  nil,
		TradelistID:  &tradelistID,
		WishlistID:   nil,
	}
	if err := storage.SaveDeckboxUser(ctx, user); err != nil {
		b.Fatalf("failed to save deckbox user: %v", err)
	}

	cardList := deckbox.CardList{
		ListId: tradelistID,
		Cards:  make(map[string]int16, 10000),
	}
	for i := 0; i < 10000; i++ {
		cardList.Cards[fmt.Sprintf("Card_%d", i)] = 1
	}
	if err := storage.SaveCardList(ctx, cardList); err != nil {
		b.Fatalf("failed to save card list: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := storage.SearchCard(ctx, "Card_", deckbox.ScopeTradelist); err != nil {
			b.Fatalf("search failed: %v", err)
		}
	}
}

func BenchmarkSearchCard_FTSDisabled(b *testing.B) {
	benchmarkSearchCard(b, false)
}

func BenchmarkSearchCard_FTSEnabled(b *testing.B) {
	benchmarkSearchCard(b, true)
}

// BenchmarkSaveCardList_PopulatedDB replaces a single list while many other
// lists already exist. This exercises the per-save DELETE against a large
// card_lists / card_lists_fts table — the realistic refresh-cycle shape, where
// a full-table scan in the FTS delete would dominate.
func BenchmarkSaveCardList_PopulatedDB(b *testing.B) {
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(oldLogger)

	storage, err := New(b.TempDir()+"/bench.db", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		b.Fatalf("failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()

	// Background data: 40 other lists x 5,000 cards = 200,000 rows.
	for l := int64(1); l <= 40; l++ {
		cards := make(map[string]int16, 5000)
		for i := 0; i < 5000; i++ {
			cards[fmt.Sprintf("Card_%d_%d", l, i)] = 1
		}
		if err := storage.SaveCardList(ctx, deckbox.CardList{ListId: l, Cards: cards}); err != nil {
			b.Fatalf("failed to seed list %d: %v", l, err)
		}
	}

	// The list being refreshed.
	tradelistID := int64(99999)
	if err := storage.SaveDeckboxUser(ctx, deckbox.DeckboxUser{DeckboxLogin: "benchuser", TradelistID: &tradelistID}); err != nil {
		b.Fatalf("failed to save deckbox user: %v", err)
	}
	cardList := deckbox.CardList{ListId: tradelistID, Cards: make(map[string]int16, 5000)}
	for i := 0; i < 5000; i++ {
		cardList.Cards[fmt.Sprintf("Card_%d", i)] = 1
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := storage.SaveCardList(ctx, cardList); err != nil {
			b.Fatalf("failed to save card list: %v", err)
		}
	}
}

// BenchmarkSaveCardList_BatchSizes sweeps the configured batch size at a fixed
// collection size to surface how CARD_LIST_BATCH_SIZE affects write throughput
// (fewer/larger statements vs. more/smaller ones).
func BenchmarkSaveCardList_BatchSizes(b *testing.B) {
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(oldLogger)

	const cardCount = 10000
	ctx := context.Background()

	for _, batchSize := range []int{100, 500, 1000, 5000} {
		b.Run(fmt.Sprintf("batch=%d", batchSize), func(b *testing.B) {
			storage, err := New(b.TempDir()+"/bench.db", slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				b.Fatalf("failed to create storage: %v", err)
			}
			defer storage.Close()
			storage.batchSize = batchSize

			tradelistID := int64(12345)
			if err := storage.SaveDeckboxUser(ctx, deckbox.DeckboxUser{
				DeckboxLogin: "benchuser",
				TradelistID:  &tradelistID,
			}); err != nil {
				b.Fatalf("failed to save deckbox user: %v", err)
			}

			cardList := deckbox.CardList{ListId: tradelistID, Cards: make(map[string]int16, cardCount)}
			for i := 0; i < cardCount; i++ {
				cardList.Cards[fmt.Sprintf("Card_%d", i)] = 1
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := storage.SaveCardList(ctx, cardList); err != nil {
					b.Fatalf("failed to save card list: %v", err)
				}
			}
		})
	}
}

// BenchmarkNormalizeASCII covers the per-card normalization that runs for every
// FTS row on save and for every query token on search.
func BenchmarkNormalizeASCII(b *testing.B) {
	inputs := map[string]string{
		"ascii":    "Lightning Bolt",
		"accented": "Sméagol, Helpful Guide",
	}
	for name, in := range inputs {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = normalizeASCII(in)
			}
		})
	}
}

// BenchmarkBuildFtsQueryTerm covers the query-string -> FTS MATCH conversion that
// runs once per search.
func BenchmarkBuildFtsQueryTerm(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = buildFtsQueryTerm("Vitu-Ghazi Inspector")
	}
}
