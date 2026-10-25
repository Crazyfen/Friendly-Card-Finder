package telegram_test

import (
	"FriendlyCardFinder/internal/chat"
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/message"
	"FriendlyCardFinder/internal/telegram"
	"context"
	"io"
	"log/slog"
	"testing"
)

func ptr(s string) *string { return &s }

// The goldens below were captured from the pre-refactor renderer, which wrote
// HTML straight into the translations. Moving to message spans must not change
// a single byte a Telegram user receives.
func TestTelegramOutputUnchanged(t *testing.T) {
	petya := []deckbox.Contact{{Platform: deckbox.PlatformTelegram, UserID: 7, Handle: ptr("petya")}}

	t.Run("single card", func(t *testing.T) {
		r := deckbox.SearchResult{
			SearchQueries: []string{"Shock"},
			Aggregates: []deckbox.UserSearchAggregate{{
				ListId: 42, DeckboxLogin: "petya", FoundCards: map[string]int16{"Shock": 4},
				UniqueCount: 1, TotalQuantity: 4, Contacts: petya,
			}},
		}
		main, notFound := chat.RenderSearch(r, "en", deckbox.ScopeTradelist)

		want := "Results for: <b>Shock</b>:\nOn deckbox <a href=\"https://deckbox.org/sets/42?f=17U2hvY2s=\">petya</a> from <a href=\"tg://user?id=7\">@petya</a>:\nShock: 4\n"
		if got := telegram.Format(main); got != want {
			t.Errorf("main:\n got %q\nwant %q", got, want)
		}
		if notFound != nil {
			t.Errorf("single-card search has no not-found message, got %q", telegram.Format(notFound))
		}
	})

	multi := deckbox.SearchResult{
		SearchQueries: []string{"Shock", "Lightning Bolt", "Ragavan"},
		Aggregates: []deckbox.UserSearchAggregate{
			{ListId: 42, DeckboxLogin: "petya", FoundCards: map[string]int16{"Shock": 4, "Lightning Bolt": 2},
				UniqueCount: 2, TotalQuantity: 6, Contacts: petya},
			{ListId: 43, DeckboxLogin: "masha", FoundCards: map[string]int16{"Shock": 1}, UniqueCount: 1, TotalQuantity: 1},
		},
		NotFound: []string{"Ragavan"},
	}
	multiGoldens := map[string][2]string{
		"en": {
			"Results for 3 cards:\n\nOn deckbox <a href=\"https://deckbox.org/sets/42\">petya</a> from <a href=\"tg://user?id=7\">@petya</a> — 2/3:\n  Lightning Bolt: 2\n  Shock: 4\n\nOn deckbox <a href=\"https://deckbox.org/sets/43\">masha</a> — 1/3:\n  Shock: 1",
			"Not found anywhere: Ragavan",
		},
		"ru": {
			"По 3 картам нашлось:\n\nВ deckbox <a href=\"https://deckbox.org/sets/42\">petya</a> у <a href=\"tg://user?id=7\">@petya</a> — 2/3:\n  Lightning Bolt: 2\n  Shock: 4\n\nВ deckbox <a href=\"https://deckbox.org/sets/43\">masha</a> — 1/3:\n  Shock: 1",
			"Не найдено ни у кого: Ragavan",
		},
	}
	for lang, want := range multiGoldens {
		t.Run("multi card "+lang, func(t *testing.T) {
			main, notFound := chat.RenderSearch(multi, lang, deckbox.ScopeTradelist)
			if got := telegram.Format(main); got != want[0] {
				t.Errorf("main:\n got %q\nwant %q", got, want[0])
			}
			if got := telegram.Format(notFound); got != want[1] {
				t.Errorf("notFound:\n got %q\nwant %q", got, want[1])
			}
		})
	}

	welcomeGoldens := map[string]string{
		"en": "<b>Hello!</b>\n\nI'm a bot to search and manage Deckbox cards.\n\n<b>How to register</b>\nSend <code>/deckbox &lt;your_login&gt;</code> — I'll save your account and fetch lists (inventory, tradelist, wishlist).\n\n<b>How to search</b>\nJust send a card name (can be multiple lines — one card per line). The bot will find matches in the tradelists of registered users.\nFor an exact name match, wrap it in quotes: <code>\"Shock\"</code>.\n\n<b>How to sell / search wishlists</b>\nSend <code>/sell &lt;card_name&gt;</code> — I'll search wishlists of registered users (supports multiple lines — one card per line). Quotes work here too: a quoted line is matched by exact name.\n\n<b>Suggest Deckbox users</b>\nSend <code>/suggestdeckbox</code> and list logins one per line — the bot will update their lists.\n\nResults include links to Deckbox and, when available, a link to the Telegram user.",
		"ru": "<b>Привет!</b>\n\nЯ бот для поиска и управления карточками Deckbox.\n\n<b>Как зарегистрироваться</b>\nОтправь <code>/deckbox &lt;твой_логин&gt;</code> — я сохраню твой аккаунт и загружу списки (инвентарь, трейдлист, вишлист).\n\n<b>Как искать</b>\nПросто отправь название карты (можно несколько строк — по одной карточке на строку). Бот найдёт совпадения в трейдлистах зарегистрированных пользователей.\nНужно точное совпадение названия — возьми его в кавычки: <code>\"Shock\"</code>.\n\n<b>Как продавать / искать в вишлистах</b>\nОтправь <code>/sell &lt;название_карты&gt;</code> — я поищу совпадения в вишлистах зарегистрированных пользователей (поддерживается несколько строк — по одной карточке на строку). Кавычки работают и здесь: строка в кавычках ищется по точному названию.\n\n<b>Предложить пользователей Deckbox</b>\nОтправь <code>/suggestdeckbox</code> и перечисли логины по одному на строку — бот обновит их списки.\n\nРезультаты содержат ссылки на Deckbox и, при наличии, ссылку на Telegram-пользователя.",
	}
	cmd := chat.NewCommands(slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	for lang, want := range welcomeGoldens {
		t.Run("welcome "+lang, func(t *testing.T) {
			replies := cmd.Start(context.Background(), chat.Request{Lang: lang, Platform: deckbox.PlatformTelegram})
			if len(replies) != 1 {
				t.Fatalf("expected one welcome reply, got %d", len(replies))
			}
			if got := telegram.Format(replies[0].Message); got != want {
				t.Errorf("welcome:\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestFormatEscapesText(t *testing.T) {
	m := message.Message{message.Text("R&D <x>"), message.Bold("a<b")}
	want := "R&amp;D &lt;x&gt;<b>a&lt;b</b>"
	if got := telegram.Format(m); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatEscapesLinks(t *testing.T) {
	m := message.Message{message.Link("<R&D>", `https://x/?a="b"&c`)}
	want := `<a href="https://x/?a=&quot;b&quot;&amp;c">&lt;R&amp;D&gt;</a>`
	if got := telegram.Format(m); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatContacts(t *testing.T) {
	m := message.Message{message.Contacts([]deckbox.Contact{
		{Platform: deckbox.PlatformDiscord, UserID: 9, Handle: ptr("alice_mtg")},
		{Platform: deckbox.PlatformTelegram, UserID: 8}, // no Handle: nothing to link to
		{Platform: deckbox.PlatformTelegram, UserID: 7, Handle: ptr("pe<tya")},
	})}
	want := `<a href="tg://user?id=7">@pe&lt;tya</a>, Discord: alice_mtg`
	if got := telegram.Format(m); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
