package deckbox

import (
	"FriendlyCardFinder/internal/lib/logger/sl"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// Fetcher is the Deckbox-scraping seam: *Scraper in production, a fake in tests.
type Fetcher interface {
	FetchDeckboxUserProfile(ctx context.Context, login string) (DeckboxUser, error)
	FetchCardList(ctx context.Context, listId int64) (CardList, error)
}

// Deckbox holds the dependencies every operation needs, so callers pass a
// receiver instead of re-threading storage, scraper and config at each site.
type Deckbox struct {
	log     *slog.Logger
	storage DeckboxSaver
	scraper Fetcher

	refreshHours   int // age at which a Deckbox User's lists are auto-refreshed
	freshnessHours int // age below which Suggest skips a Deckbox User

	// savedHashes remembers the BodyHash of the last successfully saved export
	// per listId, so refresh cycles skip the expensive delete+insert when the
	// list did not change on Deckbox (the common case). Entries are only written
	// after a successful save, and the cache starts empty — so a skip can never
	// hide unsaved data.
	savedHashes sync.Map // map[int64]uint64
}

func New(log *slog.Logger, storage DeckboxSaver, scraper Fetcher, refreshHours, freshnessHours int) *Deckbox {
	return &Deckbox{
		log:            log,
		storage:        storage,
		scraper:        scraper,
		refreshHours:   refreshHours,
		freshnessHours: freshnessHours,
	}
}

// Registration is the input for /deckbox, carrying only what the domain needs.
type Registration struct {
	TelegramID       int64
	TelegramUsername string
	DeckboxLogin     string
}

// Register stores the Bot User and refreshes their lists in the background, so
// the caller can reply without waiting on Deckbox.
func (d *Deckbox) Register(ctx context.Context, r Registration) error {
	const op = "deckbox.Register"
	log := d.log.With(slog.String("operation", op), slog.String("deckbox_id", r.DeckboxLogin))

	log.Info("registering user", slog.String("username", r.TelegramUsername))

	if err := d.storage.RegisterUser(ctx, BotUser{
		TelegramID:       r.TelegramID,
		TelegramUsername: r.TelegramUsername,
		DeckboxLogin:     r.DeckboxLogin,
	}); err != nil {
		log.Error("failed to register user", sl.Err(err))
		return err
	}

	// Same refresh every other caller uses, so registration also stamps
	// updated_at and the user is not immediately picked up as stale. Its own
	// timeout avoids an orphaned worker when the request context ends.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		d.refreshUsers(ctx, []string{r.DeckboxLogin}, 1)
	}()

	return nil
}

// saveCardListIfChanged saves the list unless its body hash matches the last
// successful save for that listId. Returns false only when saving failed.
func (d *Deckbox) saveCardListIfChanged(ctx context.Context, log *slog.Logger, cardList CardList) bool {
	if cardList.BodyHash != 0 {
		if prev, ok := d.savedHashes.Load(cardList.ListId); ok && prev.(uint64) == cardList.BodyHash {
			log.Debug("card list unchanged, skipping save", slog.Int64("list_id", cardList.ListId))
			return true
		}
	}
	if err := d.storage.SaveCardList(ctx, cardList); err != nil {
		log.Error("failed to save card list", sl.Err(err))
		return false
	}
	if cardList.BodyHash != 0 {
		d.savedHashes.Store(cardList.ListId, cardList.BodyHash)
	}
	return true
}

// refreshUser fetches one Deckbox User's profile and all three Card Lists, and
// stamps updated_at when at least one list was saved.
func (d *Deckbox) refreshUser(ctx context.Context, log *slog.Logger, login string) (cardCount int, errStr string) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err().Error()
	default:
	}

	user, err := d.scraper.FetchDeckboxUserProfile(ctx, login)
	if err != nil {
		log.Error("failed to fetch user profile", sl.Err(err))
		return 0, d.recordFailure(ctx, log, login, fmt.Sprintf("failed to fetch profile: %v", err))
	}

	err = d.storage.SaveDeckboxUser(ctx, user)
	if err != nil {
		log.Error("failed to save deckbox user", sl.Err(err))
		return 0, d.recordFailure(ctx, log, login, fmt.Sprintf("failed to save user: %v", err))
	}

	totalCards := 0
	var sizes []ListSize

	for _, l := range []struct {
		name string
		id   *int64
	}{
		{"inventory", user.InventoryID},
		{"tradelist", user.TradelistID},
		{"wishlist", user.WishlistID},
	} {
		if l.id == nil {
			continue
		}
		cardList, err := d.scraper.FetchCardList(ctx, *l.id)
		if err != nil {
			log.Error("failed to fetch "+l.name, sl.Err(err))
			continue
		}
		if d.saveCardListIfChanged(ctx, log, cardList) {
			totalCards += len(cardList.Cards)
			sizes = append(sizes, ListSize{ListId: cardList.ListId, CardCount: len(cardList.Cards)})
		}
	}

	// One write records the outcome and the day's list sizes together. A failed
	// refresh deliberately leaves updated_at alone so the user stays stale and
	// the ticker retries them — but its error still has to reach the panel.
	outcome := RefreshOutcome{DeckboxLogin: login, CardCount: totalCards, Lists: sizes}
	if totalCards == 0 {
		// No cards processed indicates possible errors fetching/saving lists
		return 0, d.recordFailure(ctx, log, login, "failed to refresh lists or no cards found")
	}

	now := time.Now().Unix()
	outcome.UpdatedAt = &now
	if err := d.storage.SaveRefreshOutcome(ctx, outcome); err != nil {
		log.Error("failed to save refresh outcome", sl.Err(err))
		return 0, fmt.Sprintf("failed to update timestamp: %v", err)
	}

	return totalCards, ""
}

// recordFailure stores why a Refresh failed and echoes the message back, so the
// Admin Panel can show what the logs used to be the only record of.
func (d *Deckbox) recordFailure(ctx context.Context, log *slog.Logger, login, msg string) string {
	if err := d.storage.SaveRefreshOutcome(ctx, RefreshOutcome{DeckboxLogin: login, Err: msg}); err != nil {
		log.Warn("failed to record refresh failure", sl.Err(err))
	}
	return msg
}

type refreshResult struct {
	login string
	cards int
	err   string
}

// refreshUsers runs refreshUser over logins with numWorkers in flight.
func (d *Deckbox) refreshUsers(ctx context.Context, logins []string, numWorkers int) []refreshResult {
	jobs := make(chan string, len(logins))
	results := make(chan refreshResult, len(logins))
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case login, ok := <-jobs:
					if !ok {
						return
					}
					workerLog := d.log.With(slog.String("deckbox_id", login), slog.Int("worker_id", workerID))
					cards, errStr := d.refreshUser(ctx, workerLog, login)
					results <- refreshResult{login: login, cards: cards, err: errStr}
				}
			}
		}(i)
	}

	// Send jobs
	go func() {
		for _, login := range logins {
			jobs <- strings.TrimSpace(login)
		}
		close(jobs)
	}()

	// Collect results
	collectedResults := []refreshResult{}
	go func() {
		wg.Wait()
		close(results)
	}()

	for res := range results {
		collectedResults = append(collectedResults, res)
	}

	return collectedResults
}

// RefreshStale refreshes every Deckbox User whose lists are older than the
// configured refresh window.
func (d *Deckbox) RefreshStale(ctx context.Context) {
	const op = "deckbox.RefreshStale"
	log := d.log.With(slog.String("operation", op))

	thresholdTime := time.Now().Add(-time.Duration(d.refreshHours) * time.Hour).Unix()
	staleLogins, err := d.storage.GetAllDeckboxUsersWithOldLists(ctx, thresholdTime)
	if err != nil {
		log.Error("failed to get stale users", sl.Err(err))
		return
	}

	if len(staleLogins) == 0 {
		log.Debug("no stale users to refresh")
		return
	}

	log.Info("starting refresh of stale user lists", slog.Int("user_count", len(staleLogins)))

	for _, res := range d.refreshUsers(ctx, staleLogins, 10) {
		if res.err != "" {
			log.Error("failed to refresh user", slog.String("deckbox_id", res.login), slog.String("error", res.err))
		}
	}

	log.Info("completed refresh of stale user lists")
}

type SuggestResult struct {
	ProcessedCount int
	SkippedCount   int
	TotalCards     int
	Errors         []string
}

// Suggest refreshes the given Deckbox logins, skipping any whose data is still
// fresh enough.
func (d *Deckbox) Suggest(ctx context.Context, logins []string) SuggestResult {
	const op = "deckbox.Suggest"
	log := d.log.With(slog.String("operation", op))

	result := SuggestResult{
		Errors: []string{},
	}

	if len(logins) == 0 {
		return result
	}

	log.Info("starting suggest deckbox refresh", slog.Int("user_count", len(logins)))

	// Pre-filter: freshness checks are cheap (one DB read each) and serial access
	// avoids races on result.Errors / result.SkippedCount inside worker goroutines.
	freshnessLimit := time.Duration(d.freshnessHours) * time.Hour
	toProcess := make([]string, 0, len(logins))
	for _, login := range logins {
		login = strings.TrimSpace(login)
		existingUser, err := d.storage.GetDeckboxUser(ctx, login)
		if err != nil {
			log.Error("failed to get existing user", sl.Err(err))
			result.Errors = append(result.Errors, fmt.Sprintf("failed to check existing user: %v", err))
			continue
		}
		if existingUser != nil && existingUser.UpdatedAt != nil {
			timeSinceLast := time.Since(time.Unix(*existingUser.UpdatedAt, 0))
			if timeSinceLast < freshnessLimit {
				log.With(slog.String("deckbox_id", login)).Info("skipping, data too fresh", slog.Duration("time_since_update", timeSinceLast), slog.Duration("freshness_limit", freshnessLimit))
				result.SkippedCount++
				continue
			}
		}
		toProcess = append(toProcess, login)
	}

	if len(toProcess) == 0 {
		return result
	}

	for _, res := range d.refreshUsers(ctx, toProcess, 5) {
		if res.err != "" {
			result.Errors = append(result.Errors, res.err)
		} else if res.cards > 0 {
			result.ProcessedCount++
			result.TotalCards += res.cards
		}
	}

	return result
}

// exactQuotePairs are the quote styles recognized as an exact-match request:
// straight double/single quotes plus the smart, guillemet, and low-9 pairs
// that mobile keyboards substitute in different locales. Single quotes are
// safe as a pair because no card name both starts and ends with an apostrophe.
var exactQuotePairs = []struct{ open, close string }{
	{`"`, `"`},
	{"'", "'"},
	{"«", "»"},
	{"“", "”"},
	{"‘", "’"},
	{"„", "“"}, // German/Czech low-9
	{"„", "”"}, // Polish/Dutch/Hungarian low-9
}

// ParseExactQuery reports whether the query is wrapped in matching quotes
// (an exact-match request) and returns it with the quotes stripped.
func ParseExactQuery(q string) (string, bool) {
	q = strings.TrimSpace(q)
	for _, p := range exactQuotePairs {
		if len(q) > len(p.open)+len(p.close) && strings.HasPrefix(q, p.open) && strings.HasSuffix(q, p.close) {
			if inner := strings.TrimSpace(q[len(p.open) : len(q)-len(p.close)]); inner != "" {
				return inner, true
			}
		}
	}
	return q, false
}

// ParseQuery turns one raw input line into a Query for the given scope.
func ParseQuery(raw, scope string) Query {
	name, exact := ParseExactQuery(raw)
	return Query{Name: name, Exact: exact, Scope: scope}
}

// Search runs one query per card name and aggregates the matches by Card List,
// ranking owners by how much of the requested set they hold. A single name is
// simply the n=1 case.
func (d *Deckbox) Search(ctx context.Context, cardNames []string, scope string) (SearchResult, error) {
	const op = "deckbox.Search"
	log := d.log.With(slog.String("operation", op), slog.Int("card_count", len(cardNames)), slog.String("scope", scope))

	// Parse quoting once up front so the stripped names — not the raw quoted
	// lines — are what the searches run on and what SearchQueries and NotFound
	// echo back.
	queries := make([]Query, len(cardNames))
	names := make([]string, len(cardNames))
	for i, raw := range cardNames {
		queries[i] = ParseQuery(raw, scope)
		names[i] = queries[i].Name
	}

	result := SearchResult{SearchQueries: names}
	aggregates := make(map[int64]*UserSearchAggregate)

	// Each card is an independent read query, so run them concurrently across
	// the read connection pool. Results land in per-index slots and are merged
	// sequentially below, keeping the output (incl. NotFound order) deterministic.
	matches := make([][]CardListWithOwner, len(queries))
	searchErrs := make([]error, len(queries))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i := range queries {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			matches[i], searchErrs[i] = d.storage.SearchCard(ctx, queries[i])
		}(i)
	}
	wg.Wait()

	for _, err := range searchErrs {
		if err != nil {
			log.Error("failed to search card", sl.Err(err))
			return SearchResult{}, err
		}
	}

	d.recordDemand(ctx, log, queries, matches)

	for i, name := range names {
		if len(matches[i]) == 0 {
			result.NotFound = append(result.NotFound, name)
			continue
		}
		for _, cl := range matches[i] {
			agg, ok := aggregates[cl.ListId]
			if !ok {
				agg = &UserSearchAggregate{
					ListId:           cl.ListId,
					DeckboxLogin:     cl.DeckboxLogin,
					TelegramID:       cl.TelegramID,
					TelegramUsername: cl.TelegramUsername,
					FoundCards:       make(map[string]int16),
				}
				aggregates[cl.ListId] = agg
			}
			for cardName, qty := range cl.Cards {
				agg.FoundCards[cardName] += qty
			}
		}
	}

	result.Aggregates = make([]UserSearchAggregate, 0, len(aggregates))
	for _, agg := range aggregates {
		agg.UniqueCount = len(agg.FoundCards)
		total := 0
		for _, qty := range agg.FoundCards {
			total += int(qty)
		}
		agg.TotalQuantity = total
		result.Aggregates = append(result.Aggregates, *agg)
	}

	sort.Slice(result.Aggregates, func(i, j int) bool {
		a, b := result.Aggregates[i], result.Aggregates[j]
		if a.UniqueCount != b.UniqueCount {
			return a.UniqueCount > b.UniqueCount
		}
		if a.TotalQuantity != b.TotalQuantity {
			return a.TotalQuantity > b.TotalQuantity
		}
		return a.DeckboxLogin < b.DeckboxLogin
	})

	return result, nil
}
