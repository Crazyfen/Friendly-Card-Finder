package deckbox

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"sync"
	"time"
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
	// BodyHash is the FNV-64a hash of the raw export body this list was parsed
	// from (0 when unknown). It lets refresh skip rewriting unchanged lists.
	BodyHash uint64
}

type CardListWithOwner struct {
	CardList
	DeckboxLogin     string
	TelegramID       *int64
	TelegramUsername *string
}

// Query is one parsed search query. Callers build it with ParseQuery so the
// quote-stripped name — not the raw input — is what gets searched and echoed
// back to the user.
type Query struct {
	Name  string
	Exact bool
	Scope string
}

// UserSearchAggregate is one Card List's contribution to a search: every
// queried card it holds, plus the counts the ranking sorts on.
type UserSearchAggregate struct {
	ListId           int64
	DeckboxLogin     string
	TelegramID       *int64
	TelegramUsername *string
	FoundCards       map[string]int16
	UniqueCount      int
	TotalQuantity    int
}

// SearchResult is the outcome of one search over 1..n card names. A single-card
// search is the n=1 case: Queries holds one name and every aggregate matched it.
type SearchResult struct {
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

// Search scopes for Query.Scope
const (
	ScopeTradelist = "tradelist"
	ScopeWishlist  = "wishlist"
	ScopeInventory = "inventory"
)

// DeckboxSaver defines the interface for storage operations. SearchCard returns
// Card Lists already grouped by their owner, so how rows are laid out and
// ordered stays behind the seam.
type DeckboxSaver interface {
	RegisterUser(ctx context.Context, user BotUser) error
	SaveDeckboxUser(ctx context.Context, user DeckboxUser) error
	SaveCardList(ctx context.Context, list CardList) error
	SearchCard(ctx context.Context, q Query) ([]CardListWithOwner, error)
	GetDeckboxUser(ctx context.Context, deckboxLogin string) (*DeckboxUser, error)
	UpdateDeckboxUserTimestamp(ctx context.Context, deckboxLogin string, updatedAt int64) error
	GetAllDeckboxUsersWithOldLists(ctx context.Context, thresholdSeconds int64) ([]string, error)
}

// ScraperAuth holds the credentials and cookie configuration for the Scraper.
// Either Login+Password (to log in) or CookieOverride (a manual _tcg_session value)
// must be provided.
type ScraperAuth struct {
	Login          string
	Password       string
	CookieOverride string // DECKBOX_SESSION_COOKIE, optional; used verbatim if set
	CookiePath     string // file where the obtained session cookie is persisted
}

// Scraper handles fetching data from Deckbox with embedded configuration.
type Scraper struct {
	login          string
	password       string
	cookieOverride string
	cookiePath     string
	httpClient     *http.Client // login client: cookie jar, no redirect following
	fetchClient    *http.Client // scrape client: follows redirects, no jar

	mu            sync.Mutex // guards sessionCookie and serializes logins (single-flight)
	sessionCookie string

	log *slog.Logger
}

// NewScraper creates a new Scraper instance with the provided auth config and logger.
func NewScraper(log *slog.Logger, auth ScraperAuth) *Scraper {
	// Cookie jar is required so the CSRF-bound session cookie from the login GET
	// is sent with the login POST. CheckRedirect stops on the success redirect so
	// we can detect it (302) versus a failed login (200 re-rendering the form).
	jar, _ := cookiejar.New(nil)

	// One shared transport keeps TLS connections to deckbox.org alive across all
	// profile/export fetches; the worker pools issue 4 requests per user, so
	// without keep-alive every request would pay a fresh TCP+TLS handshake.
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	}

	return &Scraper{
		login:          auth.Login,
		password:       auth.Password,
		cookieOverride: auth.CookieOverride,
		cookiePath:     auth.CookiePath,
		httpClient: &http.Client{
			Transport: transport,
			Jar:       jar,
			Timeout:   30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		fetchClient: &http.Client{
			Transport: transport,
			Timeout:   60 * time.Second,
		},
		log: log,
	}
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
