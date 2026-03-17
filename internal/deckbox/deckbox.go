package deckbox

import (
	"FriendlyCardFinder/internal/dto"
	"FriendlyCardFinder/internal/i18n"
	b64 "encoding/base64"
	"fmt"
	"log/slog"
	"strings"

	"github.com/go-telegram/bot/models"
)

type BotUser struct {
	TelegramID       int64
	TelegramUsername string
	DeckboxLogin     string
}

type DeckboxUser struct {
	InventoryID  *int64
	TradelistID  *int64
	WishlistID   *int64
	DeckboxLogin string
	UpdatedAt    *int64
}

type CardList struct {
	ListId int64
	Cards  map[string]int16
}

type CardListOwnerInfo struct {
	DeckboxLogin     string
	TelegramID       *int64
	TelegramUsername *string
}

type SearchCardResult struct {
	SearchQuery   string
	SearchResults []CardList
}

const (
	DeckboxBaseURL = "https://deckbox.org"
	ProfileURL     = DeckboxBaseURL + "/users/"
	ExportURL      = DeckboxBaseURL + "/sets/"
	LoginURL       = DeckboxBaseURL + "/accounts/login"
)

// Search scopes for SearchCard
const (
	ScopeTradelist = "tradelist"
	ScopeWishlist  = "wishlist"
	ScopeInventory = "inventory"
)

// DeckboxSaver defines the interface for storage operations
type DeckboxSaver interface {
	RegisterUser(user BotUser) error
	SaveDeckboxUser(user DeckboxUser) error
	SaveCardList(list CardList) error
	ClearCardList(listId int64) error
	SearchCard(cardName string, scope string) ([]dto.CardSearchDTO, error)
	GetOwnerByListId(listId int64) (*CardListOwnerInfo, error)
	GetDeckboxUser(deckboxLogin string) (*DeckboxUser, error)
	UpdateDeckboxUserTimestamp(deckboxLogin string, updatedAt int64) error
	GetAllDeckboxUsersWithOldLists(thresholdSeconds int64) ([]string, error)
}

// Scraper handles fetching data from Deckbox with embedded configuration
type Scraper struct {
	sessionCookie string
	log           *slog.Logger
}

// NewScraper creates a new Scraper instance with the provided session cookie and logger
func NewScraper(log *slog.Logger, sessionCookie string) *Scraper {
	return &Scraper{
		sessionCookie: sessionCookie,
		log:           log,
	}
}

func (scr *SearchCardResult) FormatForTelegram(storage DeckboxSaver, lang string, scope string) string {
	// Select i18n keys based on scope
	noResultsKey := "search.no_results"
	resultsHeaderKey := "search.results_header"
	if scope == ScopeWishlist {
		noResultsKey = "sell.no_results"
		resultsHeaderKey = "sell.results_header"
	}

	if len(scr.SearchResults) == 0 {
		return fmt.Sprintf(i18n.T(lang, noResultsKey), scr.SearchQuery)
	}

	var response strings.Builder
	response.WriteString(fmt.Sprintf(i18n.T(lang, resultsHeaderKey), scr.SearchQuery))

	for _, cl := range scr.SearchResults {
		// Get owner info for this list
		ownerInfo, err := storage.GetOwnerByListId(cl.ListId)
		if err == nil {
			// Format deckbox login with link to tradelist
			uEnc := b64.URLEncoding.EncodeToString([]byte(scr.SearchQuery))
			linkURL := fmt.Sprintf("https://deckbox.org/sets/%d?f=17%v", cl.ListId, uEnc)
			deckboxLink := fmt.Sprintf(i18n.T(lang, "search.deckbox_link"), linkURL, ownerInfo.DeckboxLogin)
			response.WriteString(deckboxLink)

			// Add telegram user link if available
			if ownerInfo.TelegramID != nil && ownerInfo.TelegramUsername != nil {
				fmt.Fprintf(&response, " ")
				fmt.Fprintf(&response, "у <a href=\"tg://user?id=%d\">@%v</a>", *ownerInfo.TelegramID, *ownerInfo.TelegramUsername)
			}
			fmt.Fprintf(&response, ":\n")
		}

		// Add cards
		for cardName, quantity := range cl.Cards {
			response.WriteString(fmt.Sprintf("%s: %d\n", cardName, quantity))
		}
	}

	return response.String()
}

func (cl *CardList) GetCardQuantity(cardName string) (int16, error) {
	quantity, ok := cl.Cards[cardName]
	if !ok {
		return 0, fmt.Errorf("card not found: %s", cardName)
	}
	return quantity, nil
}

func (cl *CardList) AddCard(cardName string, quantity int16) {
	quantityExisting, ok := cl.Cards[cardName]
	if ok {
		cl.Cards[cardName] = quantityExisting + quantity
	} else {
		cl.Cards[cardName] = quantity
	}
}

var shouldBeEscaped = "_*[]()~`>#+-=|{}.!"

// EscapeMarkdown escapes special symbols for Telegram MarkdownV2 syntax
func escapeMarkdown(s string) string {
	var result []rune
	for _, r := range s {
		if strings.ContainsRune(shouldBeEscaped, r) {
			result = append(result, '\\')
		}
		result = append(result, r)
	}
	return string(result)
}

// NewCommandArguments extracts arguments from a command message
func NewCommandArguments(m *models.Message) string {
	if len(m.Entities) == 0 {
		return ""
	}
	entity := m.Entities[0]

	if entity.Type != models.MessageEntityTypeBotCommand {
		return ""
	}

	if len(m.Text) == entity.Length {
		return ""
	}

	return m.Text[entity.Length+1:]
}
