package deckbox

import (
	"FriendlyCardFinder/internal/dto"
	"FriendlyCardFinder/internal/i18n"
	"context"
	b64 "encoding/base64"
	"fmt"
	"log/slog"
	"sort"
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

type CardListWithOwner struct {
	CardList
	DeckboxLogin     string
	TelegramID       *int64
	TelegramUsername *string
}

type SearchCardResult struct {
	SearchQuery   string
	SearchResults []CardListWithOwner
}

type UserSearchAggregate struct {
	ListId           int64
	DeckboxLogin     string
	TelegramID       *int64
	TelegramUsername *string
	FoundCards       map[string]int16
	UniqueCount      int
	TotalQuantity    int
}

type MultiCardSearchResult struct {
	SearchQueries []string
	Aggregates    []UserSearchAggregate
	NotFound      []string
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
	RegisterUser(ctx context.Context, user BotUser) error
	SaveDeckboxUser(ctx context.Context, user DeckboxUser) error
	SaveCardList(ctx context.Context, list CardList) error
	ClearCardList(ctx context.Context, listId int64) error
	SearchCard(ctx context.Context, cardName string, scope string) ([]dto.CardSearchDTO, error)
	GetOwnerByListId(ctx context.Context, listId int64) (*CardListOwnerInfo, error)
	GetDeckboxUser(ctx context.Context, deckboxLogin string) (*DeckboxUser, error)
	UpdateDeckboxUserTimestamp(ctx context.Context, deckboxLogin string, updatedAt int64) error
	GetAllDeckboxUsersWithOldLists(ctx context.Context, thresholdSeconds int64) ([]string, error)
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

func (scr *SearchCardResult) FormatForTelegram(lang string, scope string) string {
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
		uEnc := b64.URLEncoding.EncodeToString([]byte(scr.SearchQuery))
		linkURL := fmt.Sprintf("https://deckbox.org/sets/%d?f=17%v", cl.ListId, uEnc)
		response.WriteString(fmt.Sprintf(i18n.T(lang, "search.deckbox_link"), linkURL, cl.DeckboxLogin))

		if cl.TelegramID != nil && cl.TelegramUsername != nil {
			fmt.Fprintf(&response, " у <a href=\"tg://user?id=%d\">@%v</a>", *cl.TelegramID, *cl.TelegramUsername)
		}
		fmt.Fprintf(&response, ":\n")

		for cardName, quantity := range cl.Cards {
			response.WriteString(fmt.Sprintf("%s: %d\n", cardName, quantity))
		}
	}

	return response.String()
}

// FormatForTelegram returns (mainMessage, notFoundMessage). The second string is
// empty when every queried card was found at least once.
func (m *MultiCardSearchResult) FormatForTelegram(lang string, scope string) (string, string) {
	noResultsKey := "search.multi_no_results"
	headerKey := "search.multi_results_header"
	notFoundKey := "search.multi_not_found"
	if scope == ScopeWishlist {
		noResultsKey = "sell.multi_no_results"
		headerKey = "sell.multi_results_header"
		notFoundKey = "sell.multi_not_found"
	}

	totalQueried := len(m.SearchQueries)

	var notFoundMsg string
	if len(m.NotFound) > 0 {
		notFoundMsg = fmt.Sprintf(i18n.T(lang, notFoundKey), strings.Join(m.NotFound, ", "))
	}

	if len(m.Aggregates) == 0 {
		return fmt.Sprintf(i18n.T(lang, noResultsKey), totalQueried), ""
	}

	var response strings.Builder
	response.WriteString(fmt.Sprintf(i18n.T(lang, headerKey), totalQueried))

	for _, agg := range m.Aggregates {
		linkURL := fmt.Sprintf("https://deckbox.org/sets/%d", agg.ListId)
		response.WriteString(fmt.Sprintf(i18n.T(lang, "search.deckbox_link"), linkURL, agg.DeckboxLogin))

		if agg.TelegramID != nil && agg.TelegramUsername != nil {
			fmt.Fprintf(&response, " у <a href=\"tg://user?id=%d\">@%v</a>", *agg.TelegramID, *agg.TelegramUsername)
		}
		fmt.Fprintf(&response, i18n.T(lang, "search.multi_user_header"), agg.UniqueCount, totalQueried)

		for _, cardName := range sortedCardNames(agg.FoundCards) {
			response.WriteString(fmt.Sprintf("  %s: %d\n", cardName, agg.FoundCards[cardName]))
		}
		response.WriteString("\n")
	}

	return strings.TrimRight(response.String(), "\n"), notFoundMsg
}

func sortedCardNames(cards map[string]int16) []string {
	names := make([]string, 0, len(cards))
	for name := range cards {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
