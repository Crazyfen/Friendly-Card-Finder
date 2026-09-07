package deckbox

import (
	"FriendlyCardFinder/internal/lib/logger/sl"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// TermStat is one Search Term's contribution to Demand. Storage owns the
// canonicalization that turns Term into a counter key, so callers pass the name
// exactly as ParseQuery produced it.
type TermStat struct {
	Term  string
	Scope string
	Hit   bool
}

// ListSize is one Card List's size at the moment it was refreshed. card_lists is
// replace-in-place, so growth per collection has to be sampled as it happens.
type ListSize struct {
	ListId    int64
	CardCount int
}

// RefreshOutcome is what one Refresh attempt produced. UpdatedAt is nil when the
// refresh failed, which leaves the previous timestamp in place so the Deckbox
// User stays stale and gets retried.
type RefreshOutcome struct {
	DeckboxLogin string
	CardCount    int
	Err          string
	UpdatedAt    *int64
	Lists        []ListSize
}

// PurgeResult reports what a Purge removed. ListIDs is what the caller needs to
// drop the matching BodyHash cache entries.
type PurgeResult struct {
	ListIDs      []int64
	CardRows     int64
	Registration bool
}

// OrphanRegistration is a Bot User whose claimed login has no Deckbox User —
// a login that never produced a collection, so search can never return it.
type OrphanRegistration struct {
	TelegramID       int64
	TelegramUsername *string
	DeckboxLogin     string
}

// AdminUser is one row of the Admin Panel's manage table.
type AdminUser struct {
	DeckboxLogin     string
	TelegramID       *int64
	TelegramUsername *string
	InventoryCount   int
	TradelistCount   int
	WishlistCount    int
	MissingLists     int
	UpdatedAt        *int64
	LastError        string
	LastCardCount    *int64
}

// Overview is the Admin Panel's stat strip. It carries only what AdminUsers
// cannot already answer — the collection count and the missing-list count are
// sums over that slice, and querying them twice would let the two disagree.
type Overview struct {
	BotUsers      int
	StaleUsers    int
	CardRows      int64
	DistinctCards int64
	TotalQuantity int64
	Orphans       []OrphanRegistration
}

// TermDemand is one Search Term's counters.
type TermDemand struct {
	Term     string
	Scope    string
	Hits     int
	Misses   int
	LastSeen int64
}

// CardDemand is how many Deckbox Users want a Card.
type CardDemand struct {
	CardName string
	Wishers  int
}

// TradeMatch is a wishlist Card held by a different Deckbox User's tradelist.
type TradeMatch struct {
	Wisher   string
	CardName string
	Holder   string
	Quantity int16
}

// SupplyBucket is how many Cards are held by exactly Owners tradelists.
type SupplyBucket struct {
	Owners int
	Cards  int
}

// DayCount is one day of the rollup.
type DayCount struct {
	Day      string
	Searches int
	Misses   int
}

// Insights is the analytics page. Its queries scan card_lists, so callers are
// expected to cache it rather than recompute per request.
type Insights struct {
	TopMisses       []TermDemand
	MostWanted      []CardDemand
	TradeMatches    []TradeMatch
	TradeMatchTotal int
	Supply          []SupplyBucket
	Daily           []DayCount
}

// ErrUnknownLogin is returned by admin operations naming a login that has no
// Deckbox User.
var ErrUnknownLogin = errors.New("unknown deckbox login")

// Purge removes a Deckbox User and everything about them, then forgets the
// BodyHash of the Card Lists that went with them. Skipping that last step would
// leave saveCardListIfChanged convinced the (now deleted) lists are already
// saved, so a re-registration of the same login would stay permanently empty.
func (d *Deckbox) Purge(ctx context.Context, login string) (PurgeResult, error) {
	const op = "deckbox.Purge"
	log := d.log.With(slog.String("operation", op), slog.String("deckbox_id", login))

	res, err := d.storage.PurgeDeckboxUser(ctx, login)
	if err != nil {
		log.Error("failed to purge deckbox user", sl.Err(err))
		return PurgeResult{}, err
	}

	for _, id := range res.ListIDs {
		d.savedHashes.Delete(id)
	}

	log.Info("purged deckbox user",
		slog.Int("lists", len(res.ListIDs)),
		slog.Int64("card_rows", res.CardRows),
		slog.Bool("registration_removed", res.Registration))

	return res, nil
}

// demandTimeout bounds the fire-and-forget Demand write. writeDB has a single
// connection that a card list save can hold for seconds, so the write waits —
// but never forever, and never on a goroutine that outlives its usefulness.
const demandTimeout = 30 * time.Second

// recordDemand counts one search's terms without making the searcher wait for
// the write connection. Failures are logged and dropped: Demand is statistics,
// and a lost counter is cheaper than a slow search. See docs/adr/0004.
func (d *Deckbox) recordDemand(ctx context.Context, log *slog.Logger, queries []Query, matches [][]CardListWithOwner) {
	terms := make([]TermStat, len(queries))
	for i, q := range queries {
		terms[i] = TermStat{Term: q.Name, Scope: q.Scope, Hit: len(matches[i]) > 0}
	}

	// The request context is cancelled as soon as the reply is sent, so the
	// write gets a detached one with its own deadline.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), demandTimeout)
	go func() {
		defer cancel()
		if err := d.storage.RecordSearch(writeCtx, terms); err != nil {
			log.Warn("failed to record search demand", sl.Err(err))
		}
	}()
}

// Unlink removes the Bot User registration claiming a login, leaving the
// Deckbox User and its Card Lists in place.
func (d *Deckbox) Unlink(ctx context.Context, login string) error {
	const op = "deckbox.Unlink"
	log := d.log.With(slog.String("operation", op), slog.String("deckbox_id", login))

	if err := d.storage.UnlinkBotUser(ctx, login); err != nil {
		log.Error("failed to unlink registration", sl.Err(err))
		return err
	}

	log.Info("unlinked registration")
	return nil
}

// RefreshNow refreshes one Deckbox User immediately, bypassing the freshness
// filter Suggest applies. It goes through refreshUsers like every other caller.
func (d *Deckbox) RefreshNow(ctx context.Context, login string) error {
	const op = "deckbox.RefreshNow"

	results := d.refreshUsers(ctx, []string{login}, 1)
	if len(results) == 0 {
		return fmt.Errorf("%s: no result for %s", op, login)
	}
	if results[0].err != "" {
		return errors.New(results[0].err)
	}
	return nil
}

// AdminOverview, AdminUsers and AdminInsights exist so the Admin Panel reads
// through the domain rather than reaching into storage — the same seam that
// keeps the BodyHash cache correct for Purge.
func (d *Deckbox) AdminOverview(ctx context.Context) (Overview, error) {
	// The staleness threshold is the same refresh window the ticker uses, so the
	// panel's "stale" count means exactly what the bot means by it.
	threshold := time.Now().Add(-time.Duration(d.refreshHours) * time.Hour).Unix()
	return d.storage.AdminOverview(ctx, threshold)
}

func (d *Deckbox) AdminUsers(ctx context.Context) ([]AdminUser, error) {
	return d.storage.AdminUsers(ctx)
}

func (d *Deckbox) AdminInsights(ctx context.Context) (Insights, error) {
	return d.storage.AdminInsights(ctx)
}
