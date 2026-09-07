package deckbox_test

import (
	"FriendlyCardFinder/internal/deckbox"
	"testing"
)

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
