package i18n

import (
	"FriendlyCardFinder/internal/message"
	"strings"
)

// Simple in-memory translations. Keys are hierarchical like "start.title".
// Values hold words only: markup is each Platform's business, and every
// placeholder is %s, filled with a span by Format.
var translations = map[string]map[string]string{
	"ru": {
		"start.title":                  "Привет!",
		"start.intro":                  "Я бот для поиска и управления карточками Deckbox.",
		"start.register.heading":       "Как зарегистрироваться",
		"start.register.body":          "Отправь %s — я сохраню твой аккаунт и загружу списки (инвентарь, трейдлист, вишлист).",
		"start.register.code":          "/deckbox <твой_логин>",
		"start.search.heading":         "Как искать",
		"start.telegram.search.body":   "Просто отправь название карты (можно несколько строк — по одной карточке на строку). Бот найдёт совпадения в трейдлистах зарегистрированных пользователей.\nНужно точное совпадение названия — возьми его в кавычки: %s.",
		"start.sell.heading":           "Как продавать / искать в вишлистах",
		"start.telegram.sell.body":     "Отправь %s — я поищу совпадения в вишлистах зарегистрированных пользователей (поддерживается несколько строк — по одной карточке на строку). Кавычки работают и здесь: строка в кавычках ищется по точному названию.",
		"start.sell.code":              "/sell <название_карты>",
		"start.suggest.heading":        "Предложить пользователей Deckbox",
		"start.telegram.suggest.body":  "Отправь %s и перечисли логины по одному на строку — бот обновит их списки.",
		"start.telegram.footer":        "Результаты содержат ссылки на Deckbox и, при наличии, ссылку на Telegram-пользователя.",
		"start.discord.search.body":    "Используй %s с названием карты или без названия, чтобы вставить сразу несколько карт — по одной на строку. Ещё можно написать мне в личку или упомянуть меня с названиями карт. Нужно точное совпадение названия — возьми его в кавычки: %s.",
		"start.discord.sell.body":      "Используй %s с названием карты или без него, чтобы вставить несколько — я поищу совпадения в вишлистах зарегистрированных пользователей. Кавычки работают и здесь.",
		"start.discord.suggest.body":   "Запусти %s и вставь логины по одному на строку — бот обновит их списки.",
		"start.discord.footer":         "Результаты содержат ссылки на Deckbox и, если владелец зарегистрирован, его контакт в Discord или Telegram.",
		"discord.modal.search":         "Поиск в трейдлистах",
		"discord.modal.sell":           "Поиск в вишлистах",
		"discord.modal.suggest":        "Предложить пользователей Deckbox",
		"discord.modal.label.cards":    "Названия карт, по одной на строку",
		"discord.modal.label.logins":   "Логины Deckbox, по одному на строку",
		"deckbox.register_no_argument": "Пожалуйста, укажи логин Deckbox после команды /deckbox",
		"deckbox.register_success":     "Схоронил твой ник для Deckbox, %s %s",
		"deckbox.register_error":       "Произошла ошибка при обработке команды",
		"suggest.no_argument":          "Пожалуйста, укажи список логинов Deckbox (по одному на строку) после команды /suggestdeckbox",
		"suggest.ack":                  "Начинаю обработку %s логинов...",
		"suggest.summary":              "Обработано: %s\nПропущено: %s",
		"suggest.errors_header":        "Ошибки:\n",
		"search.no_results":            "По запросу: %s ничего не найдено.",
		"search.results_header":        "По запросу: %s нашлось:\n",
		"search.deckbox_link":          "В deckbox %s",
		"search.owner":                 " у %s",
		"search.multi_no_results":      "По %s картам ничего не найдено.",
		"search.multi_results_header":  "По %s картам нашлось:\n\n",
		"search.multi_user_header":     " — %s/%s:\n",
		"search.multi_not_found":       "Не найдено ни у кого: %s",
		"sell.no_argument":             "Пожалуйста, укажи название карточки (по одной на строку) после команды /sell",
		"sell.no_results":              "По запросу (в вишлистах): %s ничего не найдено.",
		"sell.results_header":          "В вишлистах найдены совпадения для: %s:\n",
		"sell.multi_no_results":        "По %s картам в вишлистах ничего не найдено.",
		"sell.multi_results_header":    "В вишлистах найдены совпадения по %s картам:\n\n",
		"sell.multi_not_found":         "Ни у кого нет в вишлисте: %s",
	},
	"en": {
		"start.title":                  "Hello!",
		"start.intro":                  "I'm a bot to search and manage Deckbox cards.",
		"start.register.heading":       "How to register",
		"start.register.body":          "Send %s — I'll save your account and fetch lists (inventory, tradelist, wishlist).",
		"start.register.code":          "/deckbox <your_login>",
		"start.search.heading":         "How to search",
		"start.telegram.search.body":   "Just send a card name (can be multiple lines — one card per line). The bot will find matches in the tradelists of registered users.\nFor an exact name match, wrap it in quotes: %s.",
		"start.sell.heading":           "How to sell / search wishlists",
		"start.telegram.sell.body":     "Send %s — I'll search wishlists of registered users (supports multiple lines — one card per line). Quotes work here too: a quoted line is matched by exact name.",
		"start.sell.code":              "/sell <card_name>",
		"start.suggest.heading":        "Suggest Deckbox users",
		"start.telegram.suggest.body":  "Send %s and list logins one per line — the bot will update their lists.",
		"start.telegram.footer":        "Results include links to Deckbox and, when available, a link to the Telegram user.",
		"start.discord.search.body":    "Use %s with a card name, or run it with no name to paste several cards, one per line. You can also DM me or mention me with card names. For an exact name match, wrap it in quotes: %s.",
		"start.discord.sell.body":      "Use %s with a card name, or with no name to paste several — I'll search wishlists of registered users. Quotes work here too.",
		"start.discord.suggest.body":   "Run %s and paste logins, one per line — the bot will update their lists.",
		"start.discord.footer":         "Results include links to Deckbox and the owner's Discord or Telegram contact, when they registered.",
		"discord.modal.search":         "Search tradelists",
		"discord.modal.sell":           "Search wishlists",
		"discord.modal.suggest":        "Suggest Deckbox users",
		"discord.modal.label.cards":    "Card names, one per line",
		"discord.modal.label.logins":   "Deckbox logins, one per line",
		"deckbox.register_no_argument": "Please provide a Deckbox login after the /deckbox command",
		"deckbox.register_success":     "Saved your Deckbox nick, %s %s",
		"deckbox.register_error":       "An error occurred processing your command",
		"suggest.no_argument":          "Please provide a list of Deckbox logins (one per line) after /suggestdeckbox",
		"suggest.ack":                  "Starting processing of %s logins...",
		"suggest.summary":              "Processed: %s\nSkipped: %s",
		"suggest.errors_header":        "Errors:\n",
		"search.no_results":            "No results for: %s",
		"search.results_header":        "Results for: %s:\n",
		"search.deckbox_link":          "On deckbox %s",
		"search.owner":                 " from %s",
		"search.multi_no_results":      "No results for any of the %s cards.",
		"search.multi_results_header":  "Results for %s cards:\n\n",
		"search.multi_user_header":     " — %s/%s:\n",
		"search.multi_not_found":       "Not found anywhere: %s",
		"sell.no_argument":             "Please provide a card name (one per line) after /sell",
		"sell.no_results":              "No wishlist results for: %s",
		"sell.results_header":          "Wishlist matches for: %s:\n",
		"sell.multi_no_results":        "No wishlist results for any of the %s cards.",
		"sell.multi_results_header":    "Wishlist matches for %s cards:\n\n",
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

// Format fills the %s placeholders of key's translation with args, in order,
// and returns the result as a Message. A placeholder with no argument left
// stays literal text, the way fmt marks a missing argument instead of failing.
func Format(lang, key string, args ...message.Span) message.Message {
	pieces := strings.Split(T(lang, key), "%s")
	m := make(message.Message, 0, 2*len(pieces))
	for i, piece := range pieces {
		if piece != "" {
			m = append(m, message.Text(piece))
		}
		switch {
		case i == len(pieces)-1:
		case i < len(args):
			m = append(m, args[i])
		default:
			m = append(m, message.Text("%s"))
		}
	}
	return m
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
