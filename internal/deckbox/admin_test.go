package deckbox

import (
	"context"
	"testing"
	"time"
)

func TestPurgeReportsWhatStorageRemoved(t *testing.T) {
	// The unchanged-list skip is stored with the rows it describes (see
	// TestPurgeDropsStoredBodyHash in internal/storage/sqlite), so Purge has
	// nothing to invalidate — it forwards the login and reports the result.
	ctx := context.Background()

	store := &fakeAdmin{purgeResult: PurgeResult{ListIDs: []int64{1, 2}, CardRows: 7, Registration: true}}
	d := newTestDeckboxAdmin(store)

	res, err := d.Purge(ctx, "petya")
	if err != nil {
		t.Fatalf("purge failed: %v", err)
	}
	if store.purgedLogin != "petya" {
		t.Errorf("expected petya to be purged, got %q", store.purgedLogin)
	}
	if len(res.ListIDs) != 2 || res.CardRows != 7 || !res.Registration {
		t.Errorf("purge result not passed through, got %+v", res)
	}
}

func TestSearchRecordsDemand(t *testing.T) {
	// Demand is recorded off the request path, so the test waits on the fake
	// rather than assuming the goroutine already ran.
	ctx := context.Background()

	storage := &fakeStorage{
		searchRecorded: make(chan struct{}, 1),
		searchFn: func(name string) []CardListWithOwner {
			if name == "Shock" {
				return []CardListWithOwner{listMatch(1, "petya", map[string]int16{"Shock": 2})}
			}
			return nil
		},
	}
	d := newTestDeckbox(storage, &fakeScraper{})

	if _, err := d.Search(ctx, []string{"Shock", "Ragavan"}, ScopeTradelist); err != nil {
		t.Fatalf("search failed: %v", err)
	}

	select {
	case <-storage.searchRecorded:
	case <-time.After(2 * time.Second):
		t.Fatal("demand was never recorded")
	}

	storage.mu.Lock()
	terms := append([]TermStat(nil), storage.recordedTerms...)
	storage.mu.Unlock()

	if len(terms) != 2 {
		t.Fatalf("expected both searched names recorded, got %+v", terms)
	}
	if terms[0].Term != "Shock" || !terms[0].Hit {
		t.Errorf("expected Shock recorded as a hit, got %+v", terms[0])
	}
	if terms[1].Term != "Ragavan" || terms[1].Hit {
		t.Errorf("expected Ragavan recorded as a miss, got %+v", terms[1])
	}
	if terms[0].Scope != ScopeTradelist {
		t.Errorf("expected the scope to be carried through, got %q", terms[0].Scope)
	}
}

func TestUnlinkLeavesTheCollection(t *testing.T) {
	// Unlink removes only the registration, so it must never reach the Card
	// Lists — the Deckbox User stays searchable under its login.
	ctx := context.Background()

	store := &fakeAdmin{}
	d := newTestDeckboxAdmin(store)

	if err := d.Unlink(ctx, "petya"); err != nil {
		t.Fatalf("unlink failed: %v", err)
	}
	if store.unlinkedLogin != "petya" {
		t.Errorf("expected petya to be unlinked, got %q", store.unlinkedLogin)
	}
	if store.purgedLogin != "" {
		t.Errorf("unlink must not purge, purged %q", store.purgedLogin)
	}
}
