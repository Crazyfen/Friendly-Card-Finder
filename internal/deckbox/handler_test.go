package deckbox

import (
	"context"
	"FriendlyCardFinder/internal/dto"
	"errors"
	"log/slog"
	"testing"
)

// fakeStorage simulates a DeckboxSaver that fails on SaveCardList
type fakeStorage struct {
	UpdateTimestampCalled bool
}

func (f *fakeStorage) RegisterUser(user BotUser) error        { return nil }
func (f *fakeStorage) SaveDeckboxUser(user DeckboxUser) error { return nil }
func (f *fakeStorage) SaveCardList(list CardList) error       { return errors.New("save failed") }
func (f *fakeStorage) ClearCardList(listId int64) error       { return nil }
func (f *fakeStorage) SearchCard(cardName string, scope string) ([]dto.CardSearchDTO, error) {
	return nil, nil
}
func (f *fakeStorage) GetOwnerByListId(listId int64) (*CardListOwnerInfo, error) {
	return nil, nil
}
func (f *fakeStorage) GetDeckboxUser(deckboxLogin string) (*DeckboxUser, error) { return nil, nil }
func (f *fakeStorage) UpdateDeckboxUserTimestamp(deckboxLogin string, updatedAt int64) error {
	f.UpdateTimestampCalled = true
	return nil
}
func (f *fakeStorage) GetAllDeckboxUsersWithOldLists(thresholdSeconds int64) ([]string, error) {
	return nil, nil
}

// fakeScraper returns a profile with an inventory and a simple card list
type fakeScraper struct{}

func (f *fakeScraper) FetchDeckboxUserProfile(ctx context.Context, login string) (DeckboxUser, error) {
	id := int64(1)
	return DeckboxUser{InventoryID: &id, DeckboxLogin: login}, nil
}

func (f *fakeScraper) FetchCardList(ctx context.Context, listId int64) (CardList, error) {
	cl := CardList{ListId: listId, Cards: map[string]int16{"Card_1": 1}}
	return cl, nil
}

func TestRefreshWorkerDoesNotUpdateTimestampOnSaveError(t *testing.T) {
	log := slog.Default()
	storage := &fakeStorage{}
	scraper := &fakeScraper{}

	ctx := context.Background()
	cards, errStr := refreshUserListsWorker(ctx, log, storage, scraper, "testuser")
	if cards != 0 {
		t.Fatalf("expected 0 cards processed due to save error, got %d", cards)
	}
	if errStr == "" {
		t.Fatalf("expected non-empty error string when save fails")
	}
	if storage.UpdateTimestampCalled {
		t.Fatalf("expected timestamp not to be updated when save fails")
	}
}
