package deckbox

import (
	"FriendlyCardFinder/internal/lib/logger/sl"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
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
	admin   AdminStore
	scraper Fetcher

	refreshHours   int // age at which a Deckbox User's lists are auto-refreshed
	freshnessHours int // age below which Suggest skips a Deckbox User
}

// New wires one Deckbox. storage and admin are two interfaces over the same
// implementation in production; keeping them apart means a test of search or
// Refresh never has to satisfy the Admin Panel's queries.
func New(log *slog.Logger, storage DeckboxSaver, admin AdminStore, scraper Fetcher, refreshHours, freshnessHours int) *Deckbox {
	return &Deckbox{
		log:            log,
		storage:        storage,
		admin:          admin,
		scraper:        scraper,
		refreshHours:   refreshHours,
		freshnessHours: freshnessHours,
	}
}

// ErrInvalidLogin is a login that cannot be a Deckbox username. Scraping one
// still "succeeds" — Deckbox answers with an empty profile — and leaves a
// Deckbox User with no lists behind, so it is refused before any fetch.
var ErrInvalidLogin = errors.New("invalid Deckbox login")

// loginPattern is what a Deckbox username looks like in a /users/<login> URL.
// It rules out the usual paste mistakes: a Telegram @handle, an empty line,
// or a line carrying more than the login ("name — deckbox").
var loginPattern = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)

// ParseLogin turns user input into the stored form of a Deckbox login, or
// ErrInvalidLogin. Deckbox usernames are case-insensitive, so the login is
// lowercased: "AlexVoron" and "alexvoron" are one Deckbox User, never two rows
// scraping the same profile.
func ParseLogin(raw string) (string, error) {
	login := strings.ToLower(strings.TrimSpace(raw))
	if !loginPattern.MatchString(login) {
		return "", ErrInvalidLogin
	}
	return login, nil
}

// Registration is the input for /deckbox, carrying only what the domain needs.
type Registration struct {
	Platform     Platform
	UserID       int64
	Handle       string
	DeckboxLogin string
}

// Register stores the Bot User and refreshes their lists in the background, so
// the caller can reply without waiting on Deckbox.
func (d *Deckbox) Register(ctx context.Context, r Registration) error {
	const op = "deckbox.Register"
	log := d.log.With(slog.String("operation", op), slog.String("deckbox_id", r.DeckboxLogin))

	log.Info("registering user", slog.String("platform", string(r.Platform)), slog.String("username", r.Handle))

	login, err := ParseLogin(r.DeckboxLogin)
	if err != nil {
		log.Info("rejected invalid login")
		return err
	}
	r.DeckboxLogin = login

	if err := d.storage.RegisterUser(ctx, BotUser{
		Platform:     r.Platform,
		UserID:       r.UserID,
		Handle:       r.Handle,
		DeckboxLogin: r.DeckboxLogin,
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

// refreshUser fetches one Deckbox User's profile and all three Card Lists and
// reports what happened. It saves the lists but records nothing about the
// attempt itself: refreshUsers interprets the outcome and stores it, so what
// happened and what that means stay one decision in one place.
func (d *Deckbox) refreshUser(ctx context.Context, log *slog.Logger, login string) RefreshOutcome {
	outcome := RefreshOutcome{DeckboxLogin: login}

	select {
	case <-ctx.Done():
		outcome.Err = ctx.Err().Error()
		return outcome
	default:
	}

	user, err := d.scraper.FetchDeckboxUserProfile(ctx, login)
	if err != nil {
		log.Error("failed to fetch user profile", sl.Err(err))
		outcome.Err = fmt.Sprintf("failed to fetch profile: %v", err)
		return outcome
	}

	if err := d.storage.SaveDeckboxUser(ctx, user); err != nil {
		log.Error("failed to save deckbox user", sl.Err(err))
		outcome.Err = fmt.Sprintf("failed to save user: %v", err)
		return outcome
	}
	outcome.ProfileSaved = true

	var failures []string
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
		if errors.Is(err, ErrNotFound) {
			// A Private Card List: keep no copy of it. Saving it empty with an
			// unknown hash clears its rows, FTS rows and stored hash, and it is
			// not a failure — the owner chose this (docs/adr/0009).
			if err := d.storage.SaveCardList(ctx, CardList{ListId: *l.id}); err != nil {
				log.Error("failed to delete private "+l.name, sl.Err(err))
				failures = append(failures, fmt.Sprintf("%s: %v", l.name, err))
			}
			continue
		}
		if err != nil {
			log.Error("failed to fetch "+l.name, sl.Err(err))
			failures = append(failures, fmt.Sprintf("%s: %v", l.name, err))
			continue
		}
		// Storage skips the rewrite itself when the export is unchanged, so a
		// successful save says nothing about whether rows actually moved — the
		// list is up to date either way.
		if err := d.storage.SaveCardList(ctx, cardList); err != nil {
			log.Error("failed to save "+l.name, sl.Err(err))
			failures = append(failures, fmt.Sprintf("%s: %v", l.name, err))
			continue
		}
		outcome.CardCount += len(cardList.Cards)
		outcome.Lists = append(outcome.Lists, ListSize{ListId: cardList.ListId, CardCount: len(cardList.Cards)})
	}
	outcome.Err = strings.Join(failures, "; ")

	return outcome
}

// record stamps and stores one Refresh outcome, and is the only place a Refresh
// is judged.
//
// Reaching and saving the profile is what counts as success. An empty
// collection is a legitimate answer, not a failure — refusing to stamp
// updated_at for one leaves that Deckbox User permanently stale, and the ticker
// re-scrapes them every 30 seconds forever. A Card List that could not be
// fetched is recorded in Err for the Admin Panel and retried in the next
// refresh window, for the same reason.
func (d *Deckbox) record(ctx context.Context, log *slog.Logger, outcome RefreshOutcome) RefreshOutcome {
	if outcome.ProfileSaved {
		now := time.Now().Unix()
		outcome.UpdatedAt = &now
	}
	if err := d.storage.SaveRefreshOutcome(ctx, outcome); err != nil {
		log.Error("failed to save refresh outcome", sl.Err(err))
	}
	return outcome
}

// refreshUsers runs refreshUser over logins with numWorkers in flight.
func (d *Deckbox) refreshUsers(ctx context.Context, logins []string, numWorkers int) []RefreshOutcome {
	jobs := make(chan string, len(logins))
	results := make(chan RefreshOutcome, len(logins))
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
					results <- d.record(ctx, workerLog, d.refreshUser(ctx, workerLog, login))
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
	collectedResults := []RefreshOutcome{}
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
		if res.Err != "" {
			log.Error("refresh reported problems", slog.String("deckbox_id", res.DeckboxLogin), slog.String("error", res.Err))
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
	seen := make(map[string]bool, len(logins))
	for _, raw := range logins {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		login, err := ParseLogin(raw)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%q: %v", strings.TrimSpace(raw), err))
			continue
		}
		if seen[login] {
			continue
		}
		seen[login] = true
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

	// A Deckbox User we read counts as processed even when a Card List came back
	// empty or failed; the problem is reported alongside, not instead.
	for _, res := range d.refreshUsers(ctx, toProcess, 5) {
		if res.Err != "" {
			result.Errors = append(result.Errors, res.Err)
		}
		if res.ProfileSaved {
			result.ProcessedCount++
			result.TotalCards += res.CardCount
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
func ParseQuery(raw string, scope Scope) Query {
	name, exact := ParseExactQuery(raw)
	return Query{Name: name, Exact: exact, Scope: scope}
}

// Search runs one query per card name and aggregates the matches by Card List,
// ranking owners by how much of the requested set they hold. A single name is
// simply the n=1 case.
func (d *Deckbox) Search(ctx context.Context, cardNames []string, scope Scope) (SearchResult, error) {
	const op = "deckbox.Search"
	log := d.log.With(slog.String("operation", op), slog.Int("card_count", len(cardNames)), slog.String("scope", string(scope)))

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
					ListId:       cl.ListId,
					DeckboxLogin: cl.DeckboxLogin,
					FoundCards:   make(map[string]int16),
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

	d.attachContacts(ctx, log, result.Aggregates)

	return result, nil
}

// attachContacts fills each Aggregate's Contacts with one read for all matched
// logins. Contacts are decoration, so a failure is logged and the result is
// returned without them.
func (d *Deckbox) attachContacts(ctx context.Context, log *slog.Logger, aggs []UserSearchAggregate) {
	if len(aggs) == 0 {
		return
	}
	logins := make([]string, 0, len(aggs))
	seen := make(map[string]bool, len(aggs))
	for _, a := range aggs {
		if !seen[a.DeckboxLogin] {
			seen[a.DeckboxLogin] = true
			logins = append(logins, a.DeckboxLogin)
		}
	}
	contacts, err := d.storage.ContactsFor(ctx, logins)
	if err != nil {
		log.Warn("failed to load contacts", sl.Err(err))
		return
	}
	for i := range aggs {
		aggs[i].Contacts = contacts[aggs[i].DeckboxLogin]
	}
}
