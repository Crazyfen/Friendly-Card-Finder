package sqlite

import (
	"FriendlyCardFinder/internal/deckbox"
	"context"
	"errors"
	"testing"
	"time"
)

// seedUser creates a Deckbox User with all three lists populated, plus an
// optional Bot User registration claiming the login.
func seedUser(t *testing.T, s *SQLiteStorage, login string, base int64, registered bool) {
	t.Helper()
	ctx := context.Background()

	inv, trade, wish := base+1, base+2, base+3
	if err := s.SaveDeckboxUser(ctx, deckbox.DeckboxUser{
		DeckboxLogin: login,
		InventoryID:  &inv,
		TradelistID:  &trade,
		WishlistID:   &wish,
	}); err != nil {
		t.Fatalf("save deckbox user %s: %v", login, err)
	}

	for _, id := range []int64{inv, trade, wish} {
		if err := s.SaveCardList(ctx, deckbox.CardList{
			ListId: id,
			Cards:  map[string]int16{"Lightning Bolt": 2, login + " Signature Card": 1},
		}); err != nil {
			t.Fatalf("save card list %d: %v", id, err)
		}
	}

	if registered {
		name := login + "_tg"
		if err := s.RegisterUser(ctx, deckbox.BotUser{
			TelegramID:       base,
			TelegramUsername: name,
			DeckboxLogin:     login,
		}); err != nil {
			t.Fatalf("register %s: %v", login, err)
		}
	}
}

func TestPurgeDropsStoredBodyHash(t *testing.T) {
	// The stored hash dies with the rows it describes. If it survived, a
	// re-registration of the same login would fetch the same unchanged export,
	// be skipped as "unchanged", and stay permanently empty.
	ctx := context.Background()
	s := newTestDB(t)

	const listID = int64(300)
	list := deckbox.CardList{
		ListId:   listID,
		Cards:    map[string]int16{"Lightning Bolt": 2},
		BodyHash: 42,
	}

	seedTradelist(t, s, "petya", listID)
	if err := s.SaveCardList(ctx, list); err != nil {
		t.Fatalf("save card list: %v", err)
	}

	if _, err := s.PurgeDeckboxUser(ctx, "petya"); err != nil {
		t.Fatalf("purge failed: %v", err)
	}

	// Re-registration: same login, same list, byte-identical export.
	seedTradelist(t, s, "petya", listID)
	if err := s.SaveCardList(ctx, list); err != nil {
		t.Fatalf("re-save card list: %v", err)
	}

	if found := searchCards(t, s, ctx, "Lightning Bolt", deckbox.ScopeTradelist, false); len(found) != 1 {
		t.Errorf("re-registration after a purge stayed empty: %v", found)
	}
}

func TestPurgeDeckboxUser(t *testing.T) {
	// A Purge has to clear every table the user appears in. Missing one leaves
	// rows that searches still find or that the panel still counts.
	ctx := context.Background()
	s := newTestDB(t)
	seedUser(t, s, "petya", 100, true)

	res, err := s.PurgeDeckboxUser(ctx, "petya")
	if err != nil {
		t.Fatalf("purge failed: %v", err)
	}

	if len(res.ListIDs) != 3 {
		t.Errorf("expected 3 list ids reported, got %v", res.ListIDs)
	}
	if res.CardRows != 6 {
		t.Errorf("expected 6 card rows deleted, got %d", res.CardRows)
	}
	if !res.Registration {
		t.Error("expected the registration to be reported as removed")
	}

	if got, _ := s.GetDeckboxUser(ctx, "petya"); got != nil {
		t.Error("deckbox_users row survived the purge")
	}
	if found := searchCards(t, s, ctx, "Lightning Bolt", deckbox.ScopeTradelist, false); len(found) != 0 {
		t.Errorf("purged cards are still searchable: %v", found)
	}

	overview, err := s.AdminOverview(ctx, time.Now().Unix())
	if err != nil {
		t.Fatalf("overview failed: %v", err)
	}
	if overview.BotUsers != 0 || overview.CardRows != 0 {
		t.Errorf("expected an empty database after purge, got %+v", overview)
	}
	if users, _ := s.AdminUsers(ctx); len(users) != 0 {
		t.Errorf("expected no collections after purge, got %+v", users)
	}
}

func TestPurgeLeavesOthersIntact(t *testing.T) {
	// The delete is by listId, so a bug in the id lookup would take out lists
	// belonging to whoever happens to be adjacent.
	ctx := context.Background()
	s := newTestDB(t)
	seedUser(t, s, "petya", 100, true)
	seedUser(t, s, "masha", 200, true)

	if _, err := s.PurgeDeckboxUser(ctx, "petya"); err != nil {
		t.Fatalf("purge failed: %v", err)
	}

	if got, _ := s.GetDeckboxUser(ctx, "masha"); got == nil {
		t.Fatal("masha was removed by petya's purge")
	}

	found := searchCards(t, s, ctx, "masha Signature Card", deckbox.ScopeTradelist, false)
	if len(found) == 0 {
		t.Error("masha's cards vanished with petya's purge")
	}

	users, err := s.AdminUsers(ctx)
	if err != nil {
		t.Fatalf("admin users failed: %v", err)
	}
	if len(users) != 1 || users[0].DeckboxLogin != "masha" {
		t.Fatalf("expected only masha to remain, got %+v", users)
	}
	if users[0].TradelistCount != 2 {
		t.Errorf("expected masha's tradelist count to survive, got %d", users[0].TradelistCount)
	}
}

func TestPurgeUnknownLogin(t *testing.T) {
	s := newTestDB(t)
	if _, err := s.PurgeDeckboxUser(context.Background(), "nobody"); !errors.Is(err, deckbox.ErrUnknownLogin) {
		t.Fatalf("expected ErrUnknownLogin, got %v", err)
	}
}

func TestUnlinkKeepsCollection(t *testing.T) {
	// Unlink is the half-measure Purge is not: the registration goes, the
	// collection stays searchable.
	ctx := context.Background()
	s := newTestDB(t)
	seedUser(t, s, "petya", 100, true)

	if err := s.UnlinkBotUser(ctx, "petya"); err != nil {
		t.Fatalf("unlink failed: %v", err)
	}

	if got, _ := s.GetDeckboxUser(ctx, "petya"); got == nil {
		t.Fatal("unlink removed the deckbox user")
	}
	if found := searchCards(t, s, ctx, "Lightning Bolt", deckbox.ScopeTradelist, false); len(found) == 0 {
		t.Error("unlink removed the cards")
	}

	users, _ := s.AdminUsers(ctx)
	if len(users) != 1 || users[0].TelegramID != nil {
		t.Errorf("expected the collection with no registration, got %+v", users)
	}

	if err := s.UnlinkBotUser(ctx, "petya"); !errors.Is(err, deckbox.ErrUnknownLogin) {
		t.Errorf("expected ErrUnknownLogin unlinking twice, got %v", err)
	}
}

func TestRecordSearchUpsert(t *testing.T) {
	// Counters accumulate per canonical term, and hits and misses are counted
	// separately — the miss column is the whole point of the table.
	ctx := context.Background()
	s := newTestDB(t)

	record := func(terms ...deckbox.TermStat) {
		t.Helper()
		if err := s.RecordSearch(ctx, terms); err != nil {
			t.Fatalf("record failed: %v", err)
		}
	}

	record(
		deckbox.TermStat{Term: "Lightning Bolt", Scope: deckbox.ScopeTradelist, Hit: true},
		deckbox.TermStat{Term: "Ragavan", Scope: deckbox.ScopeTradelist, Hit: false},
	)
	// Different spelling of the same term must fold into the same row.
	record(deckbox.TermStat{Term: "lightning  bolt!", Scope: deckbox.ScopeTradelist, Hit: false})
	// Same term, different scope, is a different row.
	record(deckbox.TermStat{Term: "Ragavan", Scope: deckbox.ScopeWishlist, Hit: false})
	// An all-punctuation query has no canonical form and records nothing.
	record(deckbox.TermStat{Term: "//", Scope: deckbox.ScopeTradelist, Hit: false})

	insights, err := s.AdminInsights(ctx)
	if err != nil {
		t.Fatalf("insights failed: %v", err)
	}

	byKey := map[string]deckbox.TermDemand{}
	for _, d := range insights.TopMisses {
		byKey[d.Term+"|"+string(d.Scope)] = d
	}

	if len(byKey) != 3 {
		t.Fatalf("expected 3 counter rows, got %d: %+v", len(byKey), insights.TopMisses)
	}

	bolt, ok := byKey["Lightning Bolt|tradelist"]
	if !ok {
		t.Fatalf("lightning bolt row missing, got %+v", byKey)
	}
	if bolt.Hits != 1 || bolt.Misses != 1 {
		t.Errorf("expected the two spellings to fold into hits=1 misses=1, got hits=%d misses=%d", bolt.Hits, bolt.Misses)
	}

	if r := byKey["Ragavan|tradelist"]; r.Misses != 1 || r.Hits != 0 {
		t.Errorf("expected ragavan tradelist hits=0 misses=1, got %+v", r)
	}
	if r := byKey["Ragavan|wishlist"]; r.Misses != 1 {
		t.Errorf("expected a separate wishlist row for ragavan, got %+v", r)
	}

	if len(insights.Daily) != 1 {
		t.Fatalf("expected one rollup row, got %+v", insights.Daily)
	}
	// Four countable terms across the four recording calls; the "//" one is not.
	if insights.Daily[0].Searches != 4 || insights.Daily[0].Misses != 3 {
		t.Errorf("expected daily searches=4 misses=3, got %+v", insights.Daily[0])
	}
}

func TestSaveRefreshOutcomeRecordsFailure(t *testing.T) {
	// A failed refresh must not stamp updated_at — that would hide the user
	// from the stale sweep — but its error still has to reach the panel.
	ctx := context.Background()
	s := newTestDB(t)
	seedUser(t, s, "petya", 100, false)

	ts := time.Now().Unix()
	if err := s.SaveRefreshOutcome(ctx, deckbox.RefreshOutcome{
		DeckboxLogin: "petya", CardCount: 6, UpdatedAt: &ts,
		Lists: []deckbox.ListSize{{ListId: 101, CardCount: 2}},
	}); err != nil {
		t.Fatalf("outcome failed: %v", err)
	}

	if err := s.SaveRefreshOutcome(ctx, deckbox.RefreshOutcome{
		DeckboxLogin: "petya", Err: "failed to fetch profile: 500",
	}); err != nil {
		t.Fatalf("failure outcome failed: %v", err)
	}

	users, _ := s.AdminUsers(ctx)
	if len(users) != 1 {
		t.Fatalf("expected one user, got %d", len(users))
	}
	if users[0].LastError == "" {
		t.Error("expected the failure to be recorded")
	}
	if users[0].UpdatedAt == nil || *users[0].UpdatedAt != ts {
		t.Errorf("a failed refresh must leave updated_at alone, got %v", users[0].UpdatedAt)
	}
	if users[0].LastCardCount == nil || *users[0].LastCardCount != 6 {
		t.Errorf("expected the last known good card count to survive, got %v", users[0].LastCardCount)
	}
}

func TestAdminOverviewCountsOrphansAndMissingLists(t *testing.T) {
	// The two data-quality numbers the panel exists to surface: a registration
	// pointing at nothing, and lists that are absent or empty.
	ctx := context.Background()
	s := newTestDB(t)
	seedUser(t, s, "petya", 100, true)

	// A registration whose login has no collection at all.
	if err := s.RegisterUser(ctx, deckbox.BotUser{
		TelegramID: 999, TelegramUsername: "typo", DeckboxLogin: "ptya",
	}); err != nil {
		t.Fatalf("register orphan: %v", err)
	}

	// A collection whose wishlist id exists but holds no rows.
	inv, trade := int64(301), int64(302)
	if err := s.SaveDeckboxUser(ctx, deckbox.DeckboxUser{
		DeckboxLogin: "masha", InventoryID: &inv, TradelistID: &trade,
	}); err != nil {
		t.Fatalf("save masha: %v", err)
	}
	if err := s.SaveCardList(ctx, deckbox.CardList{ListId: inv, Cards: map[string]int16{"Shock": 1}}); err != nil {
		t.Fatalf("save masha inventory: %v", err)
	}

	o, err := s.AdminOverview(ctx, time.Now().Unix())
	if err != nil {
		t.Fatalf("overview failed: %v", err)
	}

	if len(o.Orphans) != 1 || o.Orphans[0].DeckboxLogin != "ptya" {
		t.Errorf("expected one orphan registration, got %+v", o.Orphans)
	}
	if o.BotUsers != 2 {
		t.Errorf("expected 2 registrations, got %d", o.BotUsers)
	}

	// Missing lists are summed from the per-user counts, so they are asserted
	// where they are produced.
	users, err := s.AdminUsers(ctx)
	if err != nil {
		t.Fatalf("admin users failed: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 collections, got %d", len(users))
	}
	missing := 0
	for _, u := range users {
		missing += u.MissingLists
	}
	// masha: an empty tradelist and no wishlist id at all.
	if missing != 2 {
		t.Errorf("expected 2 missing lists, got %d", missing)
	}
}
