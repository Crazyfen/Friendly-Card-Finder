package deckbox_test

import (
	"FriendlyCardFinder/internal/deckbox"
	"testing"
)

// --- CardList ---

func TestCardListAddCard(t *testing.T) {
	// AddCard accumulates because the export lists one line per printing and
	// their total is the only Quantity that exists — see docs/adr/0008. It is
	// also how SearchCard folds a list's matched rows back together.
	cl := deckbox.CardList{ListId: 1, Cards: make(map[string]int16)}

	cl.AddCard("Bolt", 2)
	if qty := cl.Cards["Bolt"]; qty != 2 {
		t.Fatalf("after first add: want qty=2, got %d", qty)
	}

	cl.AddCard("Bolt", 3)
	if qty := cl.Cards["Bolt"]; qty != 5 {
		t.Fatalf("after second add: want qty=5, got %d", qty)
	}
}
