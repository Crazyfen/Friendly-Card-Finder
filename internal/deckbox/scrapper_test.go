package deckbox_test

import (
	"context"
	"FriendlyCardFinder/internal/deckbox"
	"log/slog"
	"os"
	"reflect"
	"testing"
)

func TestUserScrapper(t *testing.T) {
	scraper := deckbox.NewScraper(slog.New(slog.NewJSONHandler(os.Stdout, nil)), "elw4SSuWz2pP25WWXkVKduggYn7gT0kau9G4pqh42EPetphb%2FpoClHVXfT4uN%2BYYPB%2Figc4OHeDkSZ49rfFfRi3Mr7me8mKMrZ8I%2Bo%2BCB3j8SwYxHQznOZ3wcD727%2BHxc%2FakTCIPMI1gSPtSgTcQC%2B1HhxMNW0Ug6IVwVkwIzkb6TCKAyShOOeOjFdGxw1BE4ZpxNmLCzLW51dnRaFpxHaOcEH9PE%2FDmij48ArAmS%2BSWHudZlQ%2FfdXrQq%2Fp%2Bi8WW1v7Th44AJFqZY0CxGznRYTu99hkfoTtV%2FNRah4y8fCctAzA0N23XO6jzfM%2FO3cvpYr%2BXuShlMeKhS5hQZUobjH971LugZEOyoAJsXX6697%2BhnIuFjFGwu%2B85kDNMSsByGpPIsNXiwni7cxCKtjGbm%2BVk4egBSUkuVMI%3D--JXSCe4R%2FSfdGhbaY--DgOf0B40TkwqBCA6Xgjfvg%3D%3D")
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
	scraper := deckbox.NewScraper(slog.New(slog.NewJSONHandler(os.Stdout, nil)), "elw4SSuWz2pP25WWXkVKduggYn7gT0kau9G4pqh42EPetphb%2FpoClHVXfT4uN%2BYYPB%2Figc4OHeDkSZ49rfFfRi3Mr7me8mKMrZ8I%2Bo%2BCB3j8SwYxHQznOZ3wcD727%2BHxc%2FakTCIPMI1gSPtSgTcQC%2B1HhxMNW0Ug6IVwVkwIzkb6TCKAyShOOeOjFdGxw1BE4ZpxNmLCzLW51dnRaFpxHaOcEH9PE%2FDmij48ArAmS%2BSWHudZlQ%2FfdXrQq%2Fp%2Bi8WW1v7Th44AJFqZY0CxGznRYTu99hkfoTtV%2FNRah4y8fCctAzA0N23XO6jzfM%2FO3cvpYr%2BXuShlMeKhS5hQZUobjH971LugZEOyoAJsXX6697%2BhnIuFjFGwu%2B85kDNMSsByGpPIsNXiwni7cxCKtjGbm%2BVk4egBSUkuVMI%3D--JXSCe4R%2FSfdGhbaY--DgOf0B40TkwqBCA6Xgjfvg%3D%3D")
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
