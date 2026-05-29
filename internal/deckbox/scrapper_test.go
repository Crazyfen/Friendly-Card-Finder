package deckbox_test

import (
	"FriendlyCardFinder/env"
	"FriendlyCardFinder/internal/deckbox"
	"context"
	"log/slog"
	"os"
	"reflect"
	"testing"
)

func scraperFromEnv(t *testing.T) *deckbox.Scraper {
	t.Helper()
	auth := deckbox.ScraperAuth{
		Login:          env.DeckboxLogin.GetValue(),
		Password:       env.DeckboxPassword.GetValue(),
		CookieOverride: env.DeckboxSessionCookie.GetValue(),
		CookiePath:     t.TempDir() + "/deckbox_session",
	}
	if auth.CookieOverride == "" && (auth.Login == "" || auth.Password == "") {
		t.Skip("no deckbox auth configured (set DECKBOX_SESSION_COOKIE or DECKBOX_LOGIN+DECKBOX_PASSWORD); skipping integration test")
	}
	return deckbox.NewScraper(slog.New(slog.NewJSONHandler(os.Stdout, nil)), auth)
}

func TestUserScrapper(t *testing.T) {
	scraper := scraperFromEnv(t)
	ctx := context.Background()
	got, _ := scraper.FetchDeckboxUserProfile(ctx, "Crazyfen")

	inventoryId := int64(3191615)
	tradelistId := int64(3191616)
	wishlistId := int64(3191617)

	expected := deckbox.DeckboxUser{
		InventoryID:  &inventoryId,
		TradelistID:  &tradelistId,
		WishlistID:   &wishlistId,
		DeckboxLogin: "Crazyfen",
	}

	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("wanted %v, got %v", expected, got)
	}
}

func TestCardListScrapper(t *testing.T) {
	scraper := scraperFromEnv(t)
	ctx := context.Background()
	cardList, _ := scraper.FetchCardList(ctx, 3191615)

	t.Run("known first card", func(t *testing.T) {
		got, _ := cardList.GetCardQuantity("\"Brims\" Barone, Midway Mobster")
		expected := int16(1)

		if got != expected {
			t.Fatalf("wanted %v, got %v", expected, got)
		}
	})

	t.Run("known last card", func(t *testing.T) {
		got, _ := cardList.GetCardQuantity("Zurgo's Vanguard")
		expected := int16(3)

		if got != expected {
			t.Fatalf("wanted %v, got %v", expected, got)
		}
	})

	t.Run("unknown card", func(t *testing.T) {
		_, err := cardList.GetCardQuantity("Some Unknown Card")
		if err == nil {
			t.Fatal("expected an error, got none")
		}
	})
}
