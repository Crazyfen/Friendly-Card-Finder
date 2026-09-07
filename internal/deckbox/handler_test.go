package deckbox

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// fakeStorage is a configurable DeckboxSaver for tests.
type fakeStorage struct {
	saveCardListErr  error
	getDeckboxUserFn func(ctx context.Context, login string) (*DeckboxUser, error)
	searchResults    []CardListWithOwner
	searchFn         func(cardName string) []CardListWithOwner

	// timestampUpdated, when non-nil, is closed the first time the timestamp is
	// written — so the background refresh Register kicks off can be awaited.
	timestampUpdated chan struct{}

	// searchRecorded, when non-nil, receives once per RecordSearch call — the
	// Demand write is fire-and-forget, so tests need something to wait on.
	searchRecorded chan struct{}

	mu                    sync.Mutex // Search runs its queries concurrently
	saveCardListCalls     int
	outcomeCalls          int
	UpdateTimestampCalled bool
	lastSearchName        string
	lastSearchExact       bool
	lastSearchScope       Scope
	lastOutcome           RefreshOutcome
	recordedTerms         []TermStat
}

func (f *fakeStorage) RegisterUser(ctx context.Context, user BotUser) error        { return nil }
func (f *fakeStorage) SaveDeckboxUser(ctx context.Context, user DeckboxUser) error { return nil }
func (f *fakeStorage) SaveCardList(ctx context.Context, list CardList) error {
	f.mu.Lock()
	f.saveCardListCalls++
	f.mu.Unlock()
	return f.saveCardListErr
}
func (f *fakeStorage) SearchCard(ctx context.Context, q Query) ([]CardListWithOwner, error) {
	f.mu.Lock()
	f.lastSearchName = q.Name
	f.lastSearchExact = q.Exact
	f.lastSearchScope = q.Scope
	f.mu.Unlock()
	if f.searchFn != nil {
		return f.searchFn(q.Name), nil
	}
	return f.searchResults, nil
}
func (f *fakeStorage) GetDeckboxUser(ctx context.Context, login string) (*DeckboxUser, error) {
	if f.getDeckboxUserFn != nil {
		return f.getDeckboxUserFn(ctx, login)
	}
	return nil, nil
}
func (f *fakeStorage) SaveRefreshOutcome(ctx context.Context, o RefreshOutcome) error {
	f.mu.Lock()
	f.lastOutcome = o
	f.outcomeCalls++
	// Only a successful refresh stamps updated_at; a failure records its error
	// and leaves the timestamp alone.
	first := o.UpdatedAt != nil && !f.UpdateTimestampCalled
	if o.UpdatedAt != nil {
		f.UpdateTimestampCalled = true
	}
	f.mu.Unlock()
	if first && f.timestampUpdated != nil {
		close(f.timestampUpdated)
	}
	return nil
}
func (f *fakeStorage) GetAllDeckboxUsersWithOldLists(ctx context.Context, thresholdSeconds int64) ([]string, error) {
	return nil, nil
}

func (f *fakeStorage) RecordSearch(ctx context.Context, terms []TermStat) error {
	f.mu.Lock()
	f.recordedTerms = append(f.recordedTerms, terms...)
	f.mu.Unlock()
	if f.searchRecorded != nil {
		select {
		case f.searchRecorded <- struct{}{}:
		default:
		}
	}
	return nil
}

// fakeAdmin is a configurable AdminStore. It is separate from fakeStorage
// because the two interfaces are: a test of search or Refresh never has to
// answer the Admin Panel's queries.
type fakeAdmin struct {
	purgeResult PurgeResult
	purgeErr    error

	mu            sync.Mutex
	purgedLogin   string
	unlinkedLogin string
}

func (f *fakeAdmin) PurgeDeckboxUser(ctx context.Context, login string) (PurgeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purgedLogin = login
	return f.purgeResult, f.purgeErr
}

func (f *fakeAdmin) UnlinkBotUser(ctx context.Context, login string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unlinkedLogin = login
	return nil
}

func (f *fakeAdmin) AdminOverview(ctx context.Context, staleThreshold int64) (Overview, error) {
	return Overview{}, nil
}
func (f *fakeAdmin) AdminUsers(ctx context.Context) ([]AdminUser, error) { return nil, nil }
func (f *fakeAdmin) AdminInsights(ctx context.Context) (Insights, error) { return Insights{}, nil }

func (f *fakeStorage) saveCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saveCardListCalls
}

func (f *fakeStorage) timestampCalled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.UpdateTimestampCalled
}

func (f *fakeStorage) outcomeRecorded() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.outcomeCalls > 0
}

// fakeScraper is a configurable Fetcher for tests.
type fakeScraper struct {
	fetchErr error
	// emptyLists makes every Card List come back with no cards, which is what a
	// Deckbox User who has not filled in their collection actually looks like.
	emptyLists bool
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
	if f.emptyLists {
		return CardList{ListId: listId, Cards: map[string]int16{}}, nil
	}
	return CardList{ListId: listId, Cards: map[string]int16{"Card_1": 1}}, nil
}

func newTestDeckbox(storage DeckboxSaver, scraper Fetcher) *Deckbox {
	return New(slog.Default(), storage, &fakeAdmin{}, scraper, 3, 24)
}

// newTestDeckboxAdmin builds one for the Admin Panel operations, where the
// AdminStore is what the test configures and asserts on.
func newTestDeckboxAdmin(admin *fakeAdmin) *Deckbox {
	return New(slog.Default(), &fakeStorage{}, admin, &fakeScraper{}, 3, 24)
}

// listMatch builds one grouped storage result.
func listMatch(listId int64, login string, cards map[string]int16) CardListWithOwner {
	return CardListWithOwner{
		CardList:     CardList{ListId: listId, Cards: cards},
		DeckboxLogin: login,
	}
}

// --- Register ---

func TestRegisterRefreshesAndStampsTimestamp(t *testing.T) {
	// Registration goes through the same refresh as the scheduler, so it must
	// stamp updated_at — otherwise the new user reads as stale immediately.
	storage := &fakeStorage{timestampUpdated: make(chan struct{})}
	d := newTestDeckbox(storage, &fakeScraper{})

	if err := d.Register(context.Background(), Registration{
		TelegramID:       1,
		TelegramUsername: "alice",
		DeckboxLogin:     "alice_db",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case <-storage.timestampUpdated:
	case <-time.After(5 * time.Second):
		t.Fatal("registration did not refresh the user's lists")
	}
}

func TestRegisterReturnsStorageError(t *testing.T) {
	d := newTestDeckbox(&errorStorage{}, &fakeScraper{})
	err := d.Register(context.Background(), Registration{DeckboxLogin: "x"})
	if err == nil {
		t.Fatal("expected error when RegisterUser fails")
	}
}

// errorStorage fails registration and nothing else.
type errorStorage struct{ fakeStorage }

func (e *errorStorage) RegisterUser(ctx context.Context, user BotUser) error {
	return errors.New("duplicate user")
}

// --- refreshUser: reports what happened, decides nothing ---
//
// The unchanged-list skip lives in storage — see
// TestSaveCardListSkipsUnchangedBody in internal/storage/sqlite.

func TestRefreshUserReportsWhatHappened(t *testing.T) {
	tests := []struct {
		name      string
		storage   *fakeStorage
		scraper   *fakeScraper
		wantSaved bool
		wantErr   bool
		wantCards int
	}{
		{
			name:    "profile fetch fails",
			storage: &fakeStorage{}, scraper: &fakeScraper{fetchErr: errors.New("network error")},
			wantSaved: false, wantErr: true, wantCards: 0,
		},
		{
			name:    "card list cannot be saved",
			storage: &fakeStorage{saveCardListErr: errors.New("disk full")}, scraper: &fakeScraper{},
			wantSaved: true, wantErr: true, wantCards: 0,
		},
		{
			name:    "collection is empty",
			storage: &fakeStorage{}, scraper: &fakeScraper{emptyLists: true},
			wantSaved: true, wantErr: false, wantCards: 0,
		},
		{
			name:    "everything works",
			storage: &fakeStorage{}, scraper: &fakeScraper{},
			wantSaved: true, wantErr: false, wantCards: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := newTestDeckbox(tt.storage, tt.scraper).refreshUser(context.Background(), slog.Default(), "petya")

			if out.ProfileSaved != tt.wantSaved {
				t.Errorf("ProfileSaved = %v, want %v", out.ProfileSaved, tt.wantSaved)
			}
			if (out.Err != "") != tt.wantErr {
				t.Errorf("Err = %q, wanted an error: %v", out.Err, tt.wantErr)
			}
			if out.CardCount != tt.wantCards {
				t.Errorf("CardCount = %d, want %d", out.CardCount, tt.wantCards)
			}
			// refreshUser reports; refreshUsers stamps and stores.
			if out.UpdatedAt != nil {
				t.Error("refreshUser must not stamp updated_at")
			}
			if tt.storage.outcomeRecorded() {
				t.Error("refreshUser must not record the outcome")
			}
		})
	}
}

func TestRefreshUserContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := newTestDeckbox(&fakeStorage{}, &fakeScraper{}).refreshUser(ctx, slog.Default(), "petya")
	if out.ProfileSaved || out.Err == "" {
		t.Fatalf("a cancelled refresh must report failure, got %+v", out)
	}
}

// --- refreshUsers: the one place an outcome is judged ---

func TestRefreshUsersStampsEverythingItCouldRead(t *testing.T) {
	// An empty collection is a legitimate answer, not a failure. Refusing to
	// stamp updated_at for one leaves that Deckbox User permanently stale, and
	// the ticker re-scrapes them every 30 seconds forever.
	tests := []struct {
		name      string
		storage   *fakeStorage
		scraper   *fakeScraper
		wantStamp bool
	}{
		{"full collection", &fakeStorage{}, &fakeScraper{}, true},
		{"empty collection", &fakeStorage{}, &fakeScraper{emptyLists: true}, true},
		{"lists could not be saved", &fakeStorage{saveCardListErr: errors.New("disk full")}, &fakeScraper{}, true},
		{"profile unreachable", &fakeStorage{}, &fakeScraper{fetchErr: errors.New("network error")}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := newTestDeckbox(tt.storage, tt.scraper).refreshUsers(context.Background(), []string{"petya"}, 1)
			if len(out) != 1 {
				t.Fatalf("expected one outcome, got %d", len(out))
			}
			if stamped := out[0].UpdatedAt != nil; stamped != tt.wantStamp {
				t.Errorf("stamped = %v, want %v (outcome %+v)", stamped, tt.wantStamp, out[0])
			}
			if !tt.storage.outcomeRecorded() {
				t.Error("every outcome must be recorded, successful or not")
			}
		})
	}
}

// --- ParseExactQuery ---

func TestParseExactQuery(t *testing.T) {
	tests := []struct {
		in        string
		wantName  string
		wantExact bool
	}{
		{`"Lightning Bolt"`, "Lightning Bolt", true},
		{"«Shock»", "Shock", true},
		{"“Opt”", "Opt", true},
		{"„Opt“", "Opt", true},
		{"„Opt”", "Opt", true},
		{"'Opt'", "Opt", true},
		{"‘Opt’", "Opt", true},
		{` "Shock" `, "Shock", true},
		{`" Shock "`, "Shock", true},
		{"Lightning Bolt", "Lightning Bolt", false},
		{`"unbalanced`, `"unbalanced`, false},
		{`unbalanced"`, `unbalanced"`, false},
		{`""`, `""`, false},
		{`" "`, `" "`, false},
		{"Urza's Saga", "Urza's Saga", false},
		{"'Til Death Do Us Part", "'Til Death Do Us Part", false},
	}
	for _, tt := range tests {
		gotName, gotExact := ParseExactQuery(tt.in)
		if gotName != tt.wantName || gotExact != tt.wantExact {
			t.Errorf("ParseExactQuery(%q) = (%q, %v), want (%q, %v)", tt.in, gotName, gotExact, tt.wantName, tt.wantExact)
		}
	}
}

// --- Search: single card (the n=1 case) ---

func TestSearchSingleCardKeepsOneAggregatePerList(t *testing.T) {
	storage := &fakeStorage{searchResults: []CardListWithOwner{
		listMatch(10, "alice", map[string]int16{"Bolt": 2, "Shock": 1}),
		listMatch(20, "bob", map[string]int16{"Bolt": 4}),
	}}
	d := newTestDeckbox(storage, &fakeScraper{})

	result, err := d.Search(context.Background(), []string{"Bolt"}, ScopeTradelist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.SearchQueries) != 1 || result.SearchQueries[0] != "Bolt" {
		t.Fatalf("expected query 'Bolt', got %v", result.SearchQueries)
	}
	if len(result.Aggregates) != 2 {
		t.Fatalf("expected 2 aggregates (one per list), got %d", len(result.Aggregates))
	}
	// alice holds 2 distinct matching names, so she ranks first.
	if result.Aggregates[0].DeckboxLogin != "alice" || result.Aggregates[0].UniqueCount != 2 {
		t.Errorf("first aggregate: want alice with 2 cards, got %+v", result.Aggregates[0])
	}
	if result.Aggregates[1].ListId != 20 || result.Aggregates[1].FoundCards["Bolt"] != 4 {
		t.Errorf("second aggregate: want list 20 with Bolt=4, got %+v", result.Aggregates[1])
	}
}

func TestSearchSingleCardEmpty(t *testing.T) {
	d := newTestDeckbox(&fakeStorage{}, &fakeScraper{})
	result, err := d.Search(context.Background(), []string{"Nonexistent"}, ScopeTradelist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Aggregates) != 0 {
		t.Fatalf("expected empty results, got %d", len(result.Aggregates))
	}
	if result.SearchQueries[0] != "Nonexistent" {
		t.Errorf("expected query preserved, got %q", result.SearchQueries[0])
	}
	if len(result.NotFound) != 1 || result.NotFound[0] != "Nonexistent" {
		t.Errorf("expected the query in NotFound, got %v", result.NotFound)
	}
}

func TestSearchQuotedQueryIsExact(t *testing.T) {
	storage := &fakeStorage{}
	d := newTestDeckbox(storage, &fakeScraper{})
	result, err := d.Search(context.Background(), []string{`"Lightning Bolt"`}, ScopeTradelist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !storage.lastSearchExact {
		t.Error("expected exact search for quoted query")
	}
	if storage.lastSearchName != "Lightning Bolt" {
		t.Errorf("expected quotes stripped from storage query, got %q", storage.lastSearchName)
	}
	if result.SearchQueries[0] != "Lightning Bolt" {
		t.Errorf("expected quotes stripped from SearchQueries, got %q", result.SearchQueries[0])
	}
}

func TestSearchUnquotedQueryIsNotExact(t *testing.T) {
	storage := &fakeStorage{}
	d := newTestDeckbox(storage, &fakeScraper{})
	_, err := d.Search(context.Background(), []string{"Lightning Bolt"}, ScopeTradelist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if storage.lastSearchExact {
		t.Error("expected non-exact search for unquoted query")
	}
	if storage.lastSearchName != "Lightning Bolt" {
		t.Errorf("expected query passed through unchanged, got %q", storage.lastSearchName)
	}
}

func TestSearchPassesScopeThrough(t *testing.T) {
	storage := &fakeStorage{}
	d := newTestDeckbox(storage, &fakeScraper{})

	if _, err := d.Search(context.Background(), []string{"Bolt"}, ScopeWishlist); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if storage.lastSearchScope != ScopeWishlist {
		t.Errorf("expected scope %q to reach storage, got %q", ScopeWishlist, storage.lastSearchScope)
	}
}

// --- Search: multiple cards ---

func TestSearchAggregatesByListId(t *testing.T) {
	// list 1 has both cards; list 2 has only Bolt.
	storage := &fakeStorage{
		searchFn: func(cardName string) []CardListWithOwner {
			switch cardName {
			case "Bolt":
				return []CardListWithOwner{
					listMatch(1, "alice", map[string]int16{"Bolt": 4}),
					listMatch(2, "bob", map[string]int16{"Bolt": 2}),
				}
			case "Shock":
				return []CardListWithOwner{
					listMatch(1, "alice", map[string]int16{"Shock": 3}),
				}
			}
			return nil
		},
	}
	d := newTestDeckbox(storage, &fakeScraper{})

	result, err := d.Search(context.Background(), []string{"Bolt", "Shock"}, ScopeTradelist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Aggregates) != 2 {
		t.Fatalf("expected 2 aggregates, got %d", len(result.Aggregates))
	}
	// Alice (unique=2, total=7) first; Bob (unique=1, total=2) second.
	if result.Aggregates[0].DeckboxLogin != "alice" {
		t.Errorf("expected alice first, got %q", result.Aggregates[0].DeckboxLogin)
	}
	if result.Aggregates[0].UniqueCount != 2 || result.Aggregates[0].TotalQuantity != 7 {
		t.Errorf("alice: want unique=2 total=7, got unique=%d total=%d",
			result.Aggregates[0].UniqueCount, result.Aggregates[0].TotalQuantity)
	}
	if result.Aggregates[1].DeckboxLogin != "bob" {
		t.Errorf("expected bob second, got %q", result.Aggregates[1].DeckboxLogin)
	}
	if result.Aggregates[1].UniqueCount != 1 || result.Aggregates[1].TotalQuantity != 2 {
		t.Errorf("bob: want unique=1 total=2, got unique=%d total=%d",
			result.Aggregates[1].UniqueCount, result.Aggregates[1].TotalQuantity)
	}
}

func TestSearchTracksNotFound(t *testing.T) {
	storage := &fakeStorage{
		searchFn: func(cardName string) []CardListWithOwner {
			if cardName == "Bolt" {
				return []CardListWithOwner{listMatch(1, "alice", map[string]int16{"Bolt": 1})}
			}
			return nil
		},
	}
	d := newTestDeckbox(storage, &fakeScraper{})
	result, err := d.Search(context.Background(), []string{"Bolt", "Force of Will", "Black Lotus"}, ScopeTradelist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.NotFound) != 2 {
		t.Fatalf("expected 2 not-found entries, got %d", len(result.NotFound))
	}
	if result.NotFound[0] != "Force of Will" || result.NotFound[1] != "Black Lotus" {
		t.Errorf("expected order preserved, got %v", result.NotFound)
	}
}

func TestSearchSortOrder(t *testing.T) {
	// Three lists: A (unique=1, total=10), B (unique=2, total=3), C (unique=2, total=5).
	// Expected: C, B, A — unique DESC then total DESC.
	storage := &fakeStorage{
		searchFn: func(cardName string) []CardListWithOwner {
			switch cardName {
			case "X":
				return []CardListWithOwner{
					listMatch(1, "a", map[string]int16{"X": 10}),
					listMatch(2, "b", map[string]int16{"X": 1}),
					listMatch(3, "c", map[string]int16{"X": 1}),
				}
			case "Y":
				return []CardListWithOwner{
					listMatch(2, "b", map[string]int16{"Y": 2}),
					listMatch(3, "c", map[string]int16{"Y": 4}),
				}
			}
			return nil
		},
	}
	d := newTestDeckbox(storage, &fakeScraper{})
	result, err := d.Search(context.Background(), []string{"X", "Y"}, ScopeTradelist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := []string{result.Aggregates[0].DeckboxLogin, result.Aggregates[1].DeckboxLogin, result.Aggregates[2].DeckboxLogin}
	want := []string{"c", "b", "a"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("order[%d]: want %q, got %q (full: %v)", i, want[i], got[i], got)
		}
	}
}

func TestSearchStripsQuotes(t *testing.T) {
	storage := &fakeStorage{
		searchFn: func(cardName string) []CardListWithOwner {
			if cardName == "Bolt" {
				return []CardListWithOwner{listMatch(1, "alice", map[string]int16{"Bolt": 1})}
			}
			return nil
		},
	}
	d := newTestDeckbox(storage, &fakeScraper{})
	result, err := d.Search(context.Background(), []string{`"Bolt"`, `"Shock"`}, ScopeTradelist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Quoted lines are searched exactly, and both SearchQueries and NotFound
	// echo the stripped names.
	if !storage.lastSearchExact {
		t.Error("expected quoted lines to run as exact searches")
	}
	if len(result.SearchQueries) != 2 || result.SearchQueries[0] != "Bolt" || result.SearchQueries[1] != "Shock" {
		t.Errorf("expected stripped SearchQueries [Bolt Shock], got %v", result.SearchQueries)
	}
	if len(result.NotFound) != 1 || result.NotFound[0] != "Shock" {
		t.Errorf("expected stripped NotFound [Shock], got %v", result.NotFound)
	}
}

func TestSearchAllNotFound(t *testing.T) {
	d := newTestDeckbox(&fakeStorage{}, &fakeScraper{}) // returns nil for any card
	result, err := d.Search(context.Background(), []string{"A", "B"}, ScopeTradelist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Aggregates) != 0 {
		t.Errorf("expected no aggregates, got %d", len(result.Aggregates))
	}
	if len(result.NotFound) != 2 {
		t.Errorf("expected 2 not-found, got %d", len(result.NotFound))
	}
}

// --- Suggest freshness filter ---

func TestSuggestSkipsFreshUsers(t *testing.T) {
	recentTS := time.Now().Add(-1 * time.Hour).Unix() // 1 h ago — fresh within 24 h limit
	storage := &fakeStorage{
		getDeckboxUserFn: func(_ context.Context, login string) (*DeckboxUser, error) {
			return &DeckboxUser{DeckboxLogin: login, UpdatedAt: &recentTS}, nil
		},
	}
	d := newTestDeckbox(storage, &fakeScraper{})
	result := d.Suggest(context.Background(), []string{"user1", "user2"})
	if result.SkippedCount != 2 {
		t.Errorf("expected 2 skipped (fresh), got %d", result.SkippedCount)
	}
	if result.ProcessedCount != 0 {
		t.Errorf("expected 0 processed, got %d", result.ProcessedCount)
	}
}

func TestSuggestNilTimestampNotSkipped(t *testing.T) {
	// A user with no prior timestamp must always be processed.
	storage := &fakeStorage{
		getDeckboxUserFn: func(_ context.Context, login string) (*DeckboxUser, error) {
			return &DeckboxUser{DeckboxLogin: login, UpdatedAt: nil}, nil
		},
	}
	d := newTestDeckbox(storage, &fakeScraper{})
	result := d.Suggest(context.Background(), []string{"user1"})
	if result.SkippedCount != 0 {
		t.Errorf("nil timestamp should not be skipped, got SkippedCount=%d", result.SkippedCount)
	}
	if result.ProcessedCount != 1 {
		t.Errorf("expected 1 processed, got %d", result.ProcessedCount)
	}
}

func TestSuggestStaleUserIsProcessed(t *testing.T) {
	// A user whose last update is older than the freshness limit must be processed.
	oldTS := time.Now().Add(-48 * time.Hour).Unix() // 48 h ago — stale against 24 h limit
	storage := &fakeStorage{
		getDeckboxUserFn: func(_ context.Context, login string) (*DeckboxUser, error) {
			return &DeckboxUser{DeckboxLogin: login, UpdatedAt: &oldTS}, nil
		},
	}
	d := newTestDeckbox(storage, &fakeScraper{})
	result := d.Suggest(context.Background(), []string{"user1"})
	if result.SkippedCount != 0 {
		t.Errorf("stale user must not be skipped, got SkippedCount=%d", result.SkippedCount)
	}
	if result.ProcessedCount != 1 {
		t.Errorf("expected 1 processed, got %d", result.ProcessedCount)
	}
}

func TestSuggestEmptyLogins(t *testing.T) {
	d := newTestDeckbox(&fakeStorage{}, &fakeScraper{})
	result := d.Suggest(context.Background(), []string{})
	if result.ProcessedCount != 0 || result.SkippedCount != 0 || len(result.Errors) != 0 {
		t.Errorf("expected zero result for empty login list, got %+v", result)
	}
}
