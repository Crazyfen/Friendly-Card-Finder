package deckbox

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestPurgeInvalidatesSavedHashes(t *testing.T) {
	// The BodyHash cache lives in memory, so a Purge that only cleared the
	// database would leave saveCardListIfChanged convinced the deleted lists are
	// still saved — and a re-registration of the same login would stay empty.
	ctx := context.Background()
	log := slog.Default()

	storage := &fakeStorage{purgeResult: PurgeResult{ListIDs: []int64{1}}}
	d := newTestDeckbox(storage, &fakeScraper{})
	list := CardList{ListId: 1, Cards: map[string]int16{"Shock": 1}, BodyHash: 42}

	d.saveCardListIfChanged(ctx, log, list)
	d.saveCardListIfChanged(ctx, log, list)
	if got := storage.saveCalls(); got != 1 {
		t.Fatalf("expected the unchanged list to be skipped once cached, saves=%d", got)
	}

	if _, err := d.Purge(ctx, "petya"); err != nil {
		t.Fatalf("purge failed: %v", err)
	}
	if storage.purgedLogin != "petya" {
		t.Errorf("expected petya to be purged, got %q", storage.purgedLogin)
	}

	d.saveCardListIfChanged(ctx, log, list)
	if got := storage.saveCalls(); got != 2 {
		t.Fatalf("purge did not invalidate the BodyHash cache: saves=%d, want 2", got)
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

func TestUnlinkDoesNotTouchHashes(t *testing.T) {
	// Unlink leaves the collection in place, so the cache must survive it —
	// otherwise every unlink would force a full re-save of three lists.
	ctx := context.Background()
	log := slog.Default()

	storage := &fakeStorage{}
	d := newTestDeckbox(storage, &fakeScraper{})
	list := CardList{ListId: 1, Cards: map[string]int16{"Shock": 1}, BodyHash: 42}

	d.saveCardListIfChanged(ctx, log, list)
	if err := d.Unlink(ctx, "petya"); err != nil {
		t.Fatalf("unlink failed: %v", err)
	}
	d.saveCardListIfChanged(ctx, log, list)

	if got := storage.saveCalls(); got != 1 {
		t.Errorf("unlink should not invalidate the BodyHash cache, saves=%d", got)
	}
}
