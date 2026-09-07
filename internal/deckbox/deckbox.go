package deckbox

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
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

// Scope is which of a Deckbox User's three Card Lists an operation runs
// against. It is a named type because both the storage column and the Telegram
// wording are chosen by a switch that falls back to the tradelist: an unlisted
// value would otherwise search and read as a tradelist search with no error
// anywhere.
type Scope string

// Query is one parsed search query. Callers build it with ParseQuery so the
// quote-stripped name — not the raw input — is what gets searched and echoed
// back to the user.
type Query struct {
	Name  string
	Exact bool
	Scope Scope
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

// DeckboxBaseURL is where the real Deckbox lives. The Scraper takes it as a
// field rather than reading it here, so a test can point one at an
// httptest.Server; the renderer builds result links from the constant.
const DeckboxBaseURL = "https://deckbox.org"

// Search scopes for Query.Scope
const (
	ScopeTradelist Scope = "tradelist"
	ScopeWishlist  Scope = "wishlist"
	ScopeInventory Scope = "inventory"
)

// DeckboxSaver is what search and Refresh need from storage. SearchCard returns
// Card Lists already grouped by their owner, so how rows are laid out and
// ordered stays behind the seam.
type DeckboxSaver interface {
	RegisterUser(ctx context.Context, user BotUser) error
	SaveDeckboxUser(ctx context.Context, user DeckboxUser) error
	SaveCardList(ctx context.Context, list CardList) error
	SearchCard(ctx context.Context, q Query) ([]CardListWithOwner, error)
	GetDeckboxUser(ctx context.Context, deckboxLogin string) (*DeckboxUser, error)
	SaveRefreshOutcome(ctx context.Context, outcome RefreshOutcome) error
	GetAllDeckboxUsersWithOldLists(ctx context.Context, thresholdSeconds int64) ([]string, error)

	// RecordSearch counts Demand. It is called off the request path and its
	// failures are logged, never surfaced — see docs/adr/0004.
	RecordSearch(ctx context.Context, terms []TermStat) error
}

// AdminStore is what the Admin Panel's operations need from storage. It is a
// separate interface because the panel's queries grow with the panel, and every
// one added to DeckboxSaver would widen the seam that search and Refresh — and
// their tests — have to cross. One implementation satisfies both.
type AdminStore interface {
	PurgeDeckboxUser(ctx context.Context, deckboxLogin string) (PurgeResult, error)
	UnlinkBotUser(ctx context.Context, deckboxLogin string) error
	AdminOverview(ctx context.Context, staleThreshold int64) (Overview, error)
	AdminUsers(ctx context.Context) ([]AdminUser, error)
	AdminInsights(ctx context.Context) (Insights, error)
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

// Scraper handles fetching data from Deckbox. It owns transport and parsing;
// which cookie to use, and when to get a new one, belongs to session.
type Scraper struct {
	login       string
	password    string
	baseURL     string       // DeckboxBaseURL in production, an httptest.Server in tests
	httpClient  *http.Client // login client: cookie jar, no redirect following
	fetchClient *http.Client // scrape client: follows redirects, no jar

	session *session

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

	s := &Scraper{
		login:    auth.Login,
		password: auth.Password,
		baseURL:  DeckboxBaseURL,
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

	// The session gets the login as a function, so it depends on the Scraper's
	// transport without knowing anything about HTTP.
	s.session = newSession(log, auth.CookieOverride, auth.CookiePath, s.doLogin)

	return s
}

func (cl *CardList) AddCard(cardName string, quantity int16) {
	quantityExisting, ok := cl.Cards[cardName]
	if ok {
		cl.Cards[cardName] = quantityExisting + quantity
	} else {
		cl.Cards[cardName] = quantity
	}
}
