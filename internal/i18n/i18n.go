package i18n

import (
	"strings"
)

// Simple in-memory translations. Keys are hierarchical like "start.welcome".
var translations = map[string]map[string]string{
	"ru": {
		"start.welcome": `<b>Привет!</b>

Я бот для поиска и управления карточками Deckbox.

<b>Как зарегистрироваться</b>
Отправь <code>/deckbox &lt;твой_логин&gt;</code> — я сохраню твой аккаунт и загружу списки (инвентарь, трейдлист, вишлист).

<b>Как искать</b>
Просто отправь название карты (можно несколько строк — по одной карточке на строку). Бот найдёт совпадения в трейдлистах зарегистрированных пользователей.
Нужно точное совпадение названия — возьми его в кавычки: <code>"Shock"</code>.

<b>Как продавать / искать в вишлистах</b>
Отправь <code>/sell &lt;название_карты&gt;</code> — я поищу совпадения в вишлистах зарегистрированных пользователей (поддерживается несколько строк — по одной карточке на строку). Кавычки работают и здесь: строка в кавычках ищется по точному названию.

<b>Предложить пользователей Deckbox</b>
Отправь <code>/suggestdeckbox</code> и перечисли логины по одному на строку — бот обновит их списки.

Результаты содержат ссылки на Deckbox и, при наличии, ссылку на Telegram-пользователя.`,
		"deckbox.register_no_argument": "Пожалуйста, укажи логин Deckbox после команды /deckbox",
		"deckbox.register_success":     "Схоронил твой ник для Deckbox, %s %s",
		"deckbox.register_error":       "Произошла ошибка при обработке команды",
		"suggest.no_argument":          "Пожалуйста, укажи список логинов Deckbox (по одному на строку) после команды /suggestdeckbox",
		"suggest.ack":                  "Начинаю обработку %d логинов...",
		"suggest.summary":              "Обработано: %d\nПропущено: %d",
		"suggest.errors_header":        "Ошибки:\n",
		"search.no_results":            "По запросу: <b>%s</b> ничего не найдено.",
		"search.results_header":        "По запросу: <b>%s</b> нашлось:\n",
		"search.deckbox_link":          "В deckbox <a href=\"%s\">%s</a>",
		"search.telegram_owner":        " у <a href=\"tg://user?id=%d\">@%s</a>",
		"search.multi_no_results":      "По %d картам ничего не найдено.",
		"search.multi_results_header":  "По %d картам нашлось:\n\n",
		"search.multi_user_header":     " — %d/%d:\n",
		"search.multi_not_found":       "Не найдено ни у кого: %s",
		"sell.no_argument":             "Пожалуйста, укажи название карточки (по одной на строку) после команды /sell",
		"sell.no_results":              "По запросу (в вишлистах): <b>%s</b> ничего не найдено.",
		"sell.results_header":          "В вишлистах найдены совпадения для: <b>%s</b>:\n",
		"sell.multi_no_results":        "По %d картам в вишлистах ничего не найдено.",
		"sell.multi_results_header":    "В вишлистах найдены совпадения по %d картам:\n\n",
		"sell.multi_not_found":         "Ни у кого нет в вишлисте: %s",
	},
	"en": {
		"start.welcome": `<b>Hello!</b>

I'm a bot to search and manage Deckbox cards.

<b>How to register</b>
Send <code>/deckbox &lt;your_login&gt;</code> — I'll save your account and fetch lists (inventory, tradelist, wishlist).

<b>How to search</b>
Just send a card name (can be multiple lines — one card per line). The bot will find matches in the tradelists of registered users.
For an exact name match, wrap it in quotes: <code>"Shock"</code>.

<b>How to sell / search wishlists</b>
Send <code>/sell &lt;card_name&gt;</code> — I'll search wishlists of registered users (supports multiple lines — one card per line). Quotes work here too: a quoted line is matched by exact name.

<b>Suggest Deckbox users</b>
Send <code>/suggestdeckbox</code> and list logins one per line — the bot will update their lists.

Results include links to Deckbox and, when available, a link to the Telegram user.`,
		"deckbox.register_no_argument": "Please provide a Deckbox login after the /deckbox command",
		"deckbox.register_success":     "Saved your Deckbox nick, %s %s",
		"deckbox.register_error":       "An error occurred processing your command",
		"suggest.no_argument":          "Please provide a list of Deckbox logins (one per line) after /suggestdeckbox",
		"suggest.ack":                  "Starting processing of %d logins...",
		"suggest.summary":              "Processed: %d\nSkipped: %d",
		"suggest.errors_header":        "Errors:\n",
		"search.no_results":            "No results for: <b>%s</b>",
		"search.results_header":        "Results for: <b>%s</b>:\n",
		"search.deckbox_link":          `On deckbox <a href="%s">%s</a>`,
		"search.telegram_owner":        ` from <a href="tg://user?id=%d">@%s</a>`,
		"search.multi_no_results":      "No results for any of the %d cards.",
		"search.multi_results_header":  "Results for %d cards:\n\n",
		"search.multi_user_header":     " — %d/%d:\n",
		"search.multi_not_found":       "Not found anywhere: %s",
		"sell.no_argument":             "Please provide a card name (one per line) after /sell",
		"sell.no_results":              "No wishlist results for: <b>%s</b>",
		"sell.results_header":          "Wishlist matches for: <b>%s</b>:\n",
		"sell.multi_no_results":        "No wishlist results for any of the %d cards.",
		"sell.multi_results_header":    "Wishlist matches for %d cards:\n\n",
		"sell.multi_not_found":         "Nobody has in wishlist: %s",
	},
}

// T returns translated string for key in the given language (no formatting applied)
func T(lang, key string) string {
	if lang == "" {
		lang = "en"
	}
	lang = strings.ToLower(lang)
	m, ok := translations[lang]
	if !ok {
		m = translations["en"]
	}
	if val, ok := m[key]; ok {
		return val
	}
	// fallback: return key
	return key
}

// DetectLang maps Telegram language codes to supported languages.
func DetectLang(code string) string {
	if code == "" {
		return "en"
	}
	code = strings.ToLower(code)
	if strings.HasPrefix(code, "ru") {
		return "ru"
	}
	return "en"
}
