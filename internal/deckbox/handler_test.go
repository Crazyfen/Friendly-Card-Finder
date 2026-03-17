package deckbox

import (
	"FriendlyCardFinder/internal/dto"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

// fakeStorage is a configurable DeckboxSaver for tests.
type fakeStorage struct {
	UpdateTimestampCalled bool
	saveCardListErr       error
	getDeckboxUserFn      func(ctx context.Context, login string) (*DeckboxUser, error)
	searchCardResults     []dto.CardSearchDTO
}

func (f *fakeStorage) RegisterUser(ctx context.Context, user BotUser) error        { return nil }
func (f *fakeStorage) SaveDeckboxUser(ctx context.Context, user DeckboxUser) error { return nil }
func (f *fakeStorage) SaveCardList(ctx context.Context, list CardList) error {
	return f.saveCardListErr
}
func (f *fakeStorage) ClearCardList(ctx context.Context, listId int64) error { return nil }
func (f *fakeStorage) SearchCard(ctx context.Context, cardName string, scope string) ([]dto.CardSearchDTO, error) {
	return f.searchCardResults, nil
}
func (f *fakeStorage) GetOwnerByListId(ctx context.Context, listId int64) (*CardListOwnerInfo, error) {
	return nil, nil
}
func (f *fakeStorage) GetDeckboxUser(ctx context.Context, login string) (*DeckboxUser, error) {
	if f.getDeckboxUserFn != nil {
		return f.getDeckboxUserFn(ctx, login)
	}
	return nil, nil
}
func (f *fakeStorage) UpdateDeckboxUserTimestamp(ctx context.Context, login string, updatedAt int64) error {
	f.UpdateTimestampCalled = true
	return nil
}
func (f *fakeStorage) GetAllDeckboxUsersWithOldLists(ctx context.Context, thresholdSeconds int64) ([]string, error) {
	return nil, nil
}

// fakeScraper is a configurable profileFetcher for tests.
type fakeScraper struct {
	fetchErr error
}

func (f *fakeScraper) FetchDeckboxUserProfile(ctx context.Context, login string) (DeckboxUser, error) {
	if f.fetchErr != nil {
		return DeckboxUser{}, f.fetchErr
	}
	id := int64(1)
	return DeckboxUser{InventoryID: &id, DeckboxLogin: login}, nil
}

func (f *fakeScraper) FetchCardList(ctx context.Context, listId int64) (CardList, error) {
	if f.fetchErr != nil {
		return CardList{}, f.fetchErr
	}
	return CardList{ListId: listId, Cards: map[string]int16{"Card_1": 1}}, nil
}

// --- refreshUserListsWorker ---

func TestRefreshWorkerDoesNotUpdateTimestampOnSaveError(t *testing.T) {
	storage := &fakeStorage{saveCardListErr: errors.New("save failed")}
	cards, errStr := refreshUserListsWorker(context.Background(), slog.Default(), storage, &fakeScraper{}, "testuser")
	if cards != 0 {
		t.Fatalf("expected 0 cards, got %d", cards)
	}
	if errStr == "" {
		t.Fatal("expected non-empty error string when save fails")
	}
	if storage.UpdateTimestampCalled {
		t.Fatal("timestamp must not be updated when save fails")
	}
}

func TestRefreshWorkerUpdatesTimestampOnSuccess(t *testing.T) {
	storage := &fakeStorage{}
	cards, errStr := refreshUserListsWorker(context.Background(), slog.Default(), storage, &fakeScraper{}, "testuser")
	if errStr != "" {
		t.Fatalf("unexpected error: %s", errStr)
	}
	if cards == 0 {
		t.Fatal("expected non-zero card count on success")
	}
	if !storage.UpdateTimestampCalled {
		t.Fatal("timestamp must be updated on success")
	}
}

func TestRefreshWorkerContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	storage := &fakeStorage{}
	cards, errStr := refreshUserListsWorker(ctx, slog.Default(), storage, &fakeScraper{}, "testuser")
	if cards != 0 {
		t.Fatalf("expected 0 cards when context cancelled, got %d", cards)
	}
	if errStr == "" {
		t.Fatal("expected error string when context cancelled")
	}
	if storage.UpdateTimestampCalled {
		t.Fatal("timestamp must not be updated when context is cancelled")
	}
}

func TestRefreshWorkerProfileFetchError(t *testing.T) {
	storage := &fakeStorage{}
	scraper := &fakeScraper{fetchErr: errors.New("network error")}
	cards, errStr := refreshUserListsWorker(context.Background(), slog.Default(), storage, scraper, "testuser")
	if cards != 0 {
		t.Fatalf("expected 0 cards on scraper error, got %d", cards)
	}
	if errStr == "" {
		t.Fatal("expected error string when profile fetch fails")
	}
	if storage.UpdateTimestampCalled {
		t.Fatal("timestamp must not be updated when scraper fails")
	}
}

// --- SearchCard ---

func TestSearchCardGroupsByListId(t *testing.T) {
	// Two cards from list 10, one card from list 20 — results arrive pre-sorted by listId.
	searchResults := []dto.CardSearchDTO{
		{ListId: 10, CardName: "Bolt", Quantity: 2},
		{ListId: 10, CardName: "Shock", Quantity: 1},
		{ListId: 20, CardName: "Bolt", Quantity: 4},
	}
	storage := &fakeStorage{searchCardResults: searchResults}
	result, err := SearchCard(context.Background(), slog.Default(), storage, "Bolt", ScopeTradelist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.SearchResults) != 2 {
		t.Fatalf("expected 2 groups (one per list), got %d", len(result.SearchResults))
	}
	if result.SearchQuery != "Bolt" {
		t.Fatalf("expected query 'Bolt', got %q", result.SearchQuery)
	}
	if result.SearchResults[0].ListId != 10 || len(result.SearchResults[0].Cards) != 2 {
		t.Errorf("first group: want listId=10 with 2 cards, got %+v", result.SearchResults[0])
	}
	if result.SearchResults[1].ListId != 20 || len(result.SearchResults[1].Cards) != 1 {
		t.Errorf("second group: want listId=20 with 1 card, got %+v", result.SearchResults[1])
	}
}

func TestSearchCardSameCardAcrossLists(t *testing.T) {
	// Same card name in two different lists should produce two separate groups.
	searchResults := []dto.CardSearchDTO{
		{ListId: 1, CardName: "Lightning Bolt", Quantity: 4},
		{ListId: 2, CardName: "Lightning Bolt", Quantity: 2},
	}
	storage := &fakeStorage{searchCardResults: searchResults}
	result, err := SearchCard(context.Background(), slog.Default(), storage, "Lightning Bolt", ScopeTradelist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.SearchResults) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(result.SearchResults))
	}
	qty, _ := result.SearchResults[0].GetCardQuantity("Lightning Bolt")
	if qty != 4 {
		t.Errorf("list 1: expected qty=4, got %d", qty)
	}
	qty, _ = result.SearchResults[1].GetCardQuantity("Lightning Bolt")
	if qty != 2 {
		t.Errorf("list 2: expected qty=2, got %d", qty)
	}
}

func TestSearchCardEmpty(t *testing.T) {
	storage := &fakeStorage{}
	result, err := SearchCard(context.Background(), slog.Default(), storage, "Nonexistent", ScopeTradelist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.SearchResults) != 0 {
		t.Fatalf("expected empty results, got %d", len(result.SearchResults))
	}
	if result.SearchQuery != "Nonexistent" {
		t.Errorf("expected query preserved, got %q", result.SearchQuery)
	}
}

// --- SuggestDeckbox freshness filter ---

func TestSuggestDeckboxSkipsFreshUsers(t *testing.T) {
	recentTS := time.Now().Add(-1 * time.Hour).Unix() // 1 h ago — fresh within 24 h limit
	storage := &fakeStorage{
		getDeckboxUserFn: func(_ context.Context, login string) (*DeckboxUser, error) {
			return &DeckboxUser{DeckboxLogin: login, UpdatedAt: &recentTS}, nil
		},
	}
	result := SuggestDeckbox(context.Background(), slog.Default(), storage, &fakeScraper{}, []string{"user1", "user2"}, 24)
	if result.SkippedCount != 2 {
		t.Errorf("expected 2 skipped (fresh), got %d", result.SkippedCount)
	}
	if result.ProcessedCount != 0 {
		t.Errorf("expected 0 processed, got %d", result.ProcessedCount)
	}
}

func TestSuggestDeckboxNilTimestampNotSkipped(t *testing.T) {
	// A user with no prior timestamp must always be processed.
	storage := &fakeStorage{
		getDeckboxUserFn: func(_ context.Context, login string) (*DeckboxUser, error) {
			return &DeckboxUser{DeckboxLogin: login, UpdatedAt: nil}, nil
		},
	}
	result := SuggestDeckbox(context.Background(), slog.Default(), storage, &fakeScraper{}, []string{"user1"}, 24)
	if result.SkippedCount != 0 {
		t.Errorf("nil timestamp should not be skipped, got SkippedCount=%d", result.SkippedCount)
	}
	if result.ProcessedCount != 1 {
		t.Errorf("expected 1 processed, got %d", result.ProcessedCount)
	}
}

func TestSuggestDeckboxStaleUserIsProcessed(t *testing.T) {
	// A user whose last update is older than the freshness limit must be processed.
	oldTS := time.Now().Add(-48 * time.Hour).Unix() // 48 h ago — stale against 24 h limit
	storage := &fakeStorage{
		getDeckboxUserFn: func(_ context.Context, login string) (*DeckboxUser, error) {
			return &DeckboxUser{DeckboxLogin: login, UpdatedAt: &oldTS}, nil
		},
	}
	result := SuggestDeckbox(context.Background(), slog.Default(), storage, &fakeScraper{}, []string{"user1"}, 24)
	if result.SkippedCount != 0 {
		t.Errorf("stale user must not be skipped, got SkippedCount=%d", result.SkippedCount)
	}
	if result.ProcessedCount != 1 {
		t.Errorf("expected 1 processed, got %d", result.ProcessedCount)
	}
}

func TestSuggestDeckboxEmptyLogins(t *testing.T) {
	storage := &fakeStorage{}
	result := SuggestDeckbox(context.Background(), slog.Default(), storage, &fakeScraper{}, []string{}, 24)
	if result.ProcessedCount != 0 || result.SkippedCount != 0 || len(result.Errors) != 0 {
		t.Errorf("expected zero result for empty login list, got %+v", result)
	}
}
