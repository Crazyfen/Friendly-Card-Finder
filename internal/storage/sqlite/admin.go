package sqlite

import (
	"FriendlyCardFinder/internal/deckbox"
	"context"
	"database/sql"
	"fmt"
	"time"
)

// dayKey is the UTC day a rollup row is keyed by. UTC rather than local time so
// the key never shifts under a timezone or DST change.
func dayKey(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// RecordSearch folds one search into the Demand counters and the daily rollup.
// The counter key is the canonical form of what the user typed — not a matched
// Card, because a miss matched none. See docs/adr/0004.
func (s *SQLiteStorage) RecordSearch(ctx context.Context, terms []deckbox.TermStat) error {
	const op = "storage.sqlite.RecordSearch"

	if len(terms) == 0 {
		return nil
	}

	now := time.Now()

	tx, err := s.db.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer tx.Rollback()

	searches, misses := 0, 0
	for _, t := range terms {
		// An all-punctuation query has no canonical form, so it has no Demand
		// to record — the same input SearchCard degrades to a plain LIKE.
		key := canonicalCardName(t.Term)
		if key == "" {
			continue
		}

		hit, miss := 1, 0
		if !t.Hit {
			hit, miss = 0, 1
		}
		searches++
		misses += miss

		if _, err := tx.ExecContext(ctx, `
		INSERT INTO search_stats(term, scope, display, hits, misses, last_seen)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(term, scope) DO UPDATE SET
			hits      = search_stats.hits + excluded.hits,
			misses    = search_stats.misses + excluded.misses,
			last_seen = excluded.last_seen`,
			key, string(t.Scope), t.Term, hit, miss, now.Unix()); err != nil {
			return fmt.Errorf("%s: search_stats: %w", op, err)
		}
	}

	if searches == 0 {
		return nil
	}

	if _, err := tx.ExecContext(ctx, `
	INSERT INTO daily_stats(day, searches, misses)
	VALUES (?, ?, ?)
	ON CONFLICT(day) DO UPDATE SET
		searches = daily_stats.searches + excluded.searches,
		misses   = daily_stats.misses + excluded.misses`,
		dayKey(now), searches, misses); err != nil {
		return fmt.Errorf("%s: daily_stats: %w", op, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// SaveRefreshOutcome stamps the result of one Refresh and samples the sizes of
// the Card Lists it saved. A failed refresh records its error but leaves
// updated_at untouched, so the user stays stale and the ticker retries them.
func (s *SQLiteStorage) SaveRefreshOutcome(ctx context.Context, o deckbox.RefreshOutcome) error {
	const op = "storage.sqlite.SaveRefreshOutcome"

	tx, err := s.db.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer tx.Rollback()

	if o.UpdatedAt != nil {
		if _, err := tx.ExecContext(ctx, `
		UPDATE deckbox_users
		SET updated_at = ?, last_card_count = ?, last_error = NULL
		WHERE deckboxLogin = ?`, *o.UpdatedAt, o.CardCount, o.DeckboxLogin); err != nil {
			return fmt.Errorf("%s: %w", op, err)
		}
	} else if _, err := tx.ExecContext(ctx, `
		UPDATE deckbox_users SET last_error = ? WHERE deckboxLogin = ?`,
		o.Err, o.DeckboxLogin); err != nil {
		// last_card_count is deliberately left alone: the last known good size
		// is more useful than a zero from a failed scrape.
		return fmt.Errorf("%s: %w", op, err)
	}

	day := dayKey(time.Now())
	for _, l := range o.Lists {
		if _, err := tx.ExecContext(ctx, `
		INSERT INTO list_snapshots(listId, day, cardCount)
		VALUES (?, ?, ?)
		ON CONFLICT(listId, day) DO UPDATE SET cardCount = excluded.cardCount`,
			l.ListId, day, l.CardCount); err != nil {
			return fmt.Errorf("%s: list_snapshots: %w", op, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// PurgeDeckboxUser removes a Deckbox User and everything about them: the three
// Card Lists' rows, the FTS rows, the stored body hashes, the snapshots, the
// user, and the Bot User registration claiming that login. Dropping the hash
// alongside the rows is what lets a re-registration of the same login refill
// the collection instead of being skipped as "unchanged". ListIDs is reported
// so the caller can say how many Card Lists went.
func (s *SQLiteStorage) PurgeDeckboxUser(ctx context.Context, login string) (deckbox.PurgeResult, error) {
	const op = "storage.sqlite.PurgeDeckboxUser"

	var result deckbox.PurgeResult

	tx, err := s.db.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("%s: %w", op, err)
	}
	defer tx.Rollback()

	var inventoryId, tradelistId, wishlistId *int64
	err = tx.QueryRowContext(ctx, `
	SELECT inventoryId, tradelistId, wishlistId FROM deckbox_users WHERE deckboxLogin = ?`,
		login).Scan(&inventoryId, &tradelistId, &wishlistId)
	if err == sql.ErrNoRows {
		return result, deckbox.ErrUnknownLogin
	}
	if err != nil {
		return result, fmt.Errorf("%s: %w", op, err)
	}

	for _, id := range []*int64{inventoryId, tradelistId, wishlistId} {
		if id == nil {
			continue
		}
		result.ListIDs = append(result.ListIDs, *id)

		res, err := tx.ExecContext(ctx, `DELETE FROM card_lists WHERE listId = ?`, *id)
		if err != nil {
			return result, fmt.Errorf("%s: card_lists: %w", op, err)
		}
		if n, err := res.RowsAffected(); err == nil {
			result.CardRows += n
		}

		if s.ftsEnabled {
			// MATCH on the indexed listId column, as in SaveCardList: a plain
			// WHERE listId = ? would scan the whole FTS table.
			if _, err := tx.ExecContext(ctx, `DELETE FROM card_lists_fts WHERE card_lists_fts MATCH ?`, ftsListIdQuery(*id)); err != nil {
				return result, fmt.Errorf("%s: card_lists_fts: %w", op, err)
			}
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM card_list_hashes WHERE listId = ?`, *id); err != nil {
			return result, fmt.Errorf("%s: card_list_hashes: %w", op, err)
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM list_snapshots WHERE listId = ?`, *id); err != nil {
			return result, fmt.Errorf("%s: list_snapshots: %w", op, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM deckbox_users WHERE deckboxLogin = ?`, login); err != nil {
		return result, fmt.Errorf("%s: deckbox_users: %w", op, err)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE deckboxLogin = ?`, login)
	if err != nil {
		return result, fmt.Errorf("%s: users: %w", op, err)
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		result.Registration = true
	}

	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("%s: %w", op, err)
	}
	return result, nil
}

// UnlinkBotUser removes only the registration claiming a login, leaving the
// Deckbox User and its Card Lists searchable.
func (s *SQLiteStorage) UnlinkBotUser(ctx context.Context, login string) error {
	const op = "storage.sqlite.UnlinkBotUser"

	res, err := s.db.writeDB.ExecContext(ctx, `DELETE FROM users WHERE deckboxLogin = ?`, login)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return deckbox.ErrUnknownLogin
	}
	return nil
}

// AdminOverview counts the manage page's stat strip. Everything here is an
// index count, so the page stays fast enough to reload after each action — the
// expensive analytics live in AdminInsights. Counts the page can derive from
// AdminUsers (collections tracked, missing lists) are deliberately absent.
func (s *SQLiteStorage) AdminOverview(ctx context.Context, staleThreshold int64) (deckbox.Overview, error) {
	const op = "storage.sqlite.AdminOverview"

	var o deckbox.Overview

	// One round trip: five independent counts as five scalar subqueries.
	if err := s.db.readDB.QueryRowContext(ctx, `
	SELECT (SELECT COUNT(*) FROM users),
	       (SELECT COUNT(*) FROM card_lists),
	       (SELECT COUNT(DISTINCT cardName) FROM card_lists),
	       (SELECT COALESCE(SUM(quantity), 0) FROM card_lists),
	       (SELECT COUNT(*) FROM deckbox_users WHERE updated_at IS NULL OR updated_at < ?)`,
		staleThreshold).Scan(&o.BotUsers, &o.CardRows, &o.DistinctCards, &o.TotalQuantity, &o.StaleUsers); err != nil {
		return o, fmt.Errorf("%s: %w", op, err)
	}

	orphans, err := collect(ctx, s.db.readDB, `
	SELECT u.telegramId, u.username, u.deckboxLogin
	FROM users AS u
	LEFT JOIN deckbox_users AS d ON d.deckboxLogin = u.deckboxLogin
	WHERE d.deckboxLogin IS NULL
	ORDER BY u.deckboxLogin COLLATE NOCASE`, nil,
		func(r *sql.Rows) (deckbox.OrphanRegistration, error) {
			var orphan deckbox.OrphanRegistration
			err := r.Scan(&orphan.TelegramID, &orphan.TelegramUsername, &orphan.DeckboxLogin)
			return orphan, err
		})
	if err != nil {
		return o, fmt.Errorf("%s: orphans: %w", op, err)
	}

	o.Orphans = orphans
	return o, nil
}

// AdminUsers returns the manage table: every Deckbox User with its per-scope
// card counts, registration, freshness and last refresh error.
func (s *SQLiteStorage) AdminUsers(ctx context.Context) ([]deckbox.AdminUser, error) {
	const op = "storage.sqlite.AdminUsers"

	// The three correlated counts each resolve through the (listId, cardName)
	// unique index, so this is an index range count per list rather than a scan.
	users, err := collect(ctx, s.db.readDB, `
	SELECT d.deckboxLogin, u.telegramId, u.username,
	       d.updated_at, d.last_error, d.last_card_count,
	       (SELECT COUNT(*) FROM card_lists c WHERE c.listId = d.inventoryId),
	       (SELECT COUNT(*) FROM card_lists c WHERE c.listId = d.tradelistId),
	       (SELECT COUNT(*) FROM card_lists c WHERE c.listId = d.wishlistId)
	FROM deckbox_users AS d
	LEFT JOIN users AS u ON u.deckboxLogin = d.deckboxLogin
	ORDER BY d.deckboxLogin COLLATE NOCASE`, nil,
		func(r *sql.Rows) (deckbox.AdminUser, error) {
			var u deckbox.AdminUser
			var lastError *string
			if err := r.Scan(&u.DeckboxLogin, &u.TelegramID, &u.TelegramUsername,
				&u.UpdatedAt, &lastError, &u.LastCardCount,
				&u.InventoryCount, &u.TradelistCount, &u.WishlistCount); err != nil {
				return u, err
			}
			if lastError != nil {
				u.LastError = *lastError
			}
			// A list counts as missing when the profile carries no id for it or
			// when it holds no rows: an empty scrape looks the same to a searcher
			// as a list that was never there, and a NULL id counts zero rows.
			for _, n := range []int{u.InventoryCount, u.TradelistCount, u.WishlistCount} {
				if n == 0 {
					u.MissingLists++
				}
			}
			return u, nil
		})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return users, nil
}

// insightsLimit caps each analytics list. The tables behind them hold hundreds
// of thousands of rows, and a page nobody scrolls past the top of does not need
// to render all of it.
const (
	insightsLimit     = 50
	tradeMatchLimit   = 200
	dailyHistoryDays  = 30
	supplyBucketLimit = 12
)

// tradeMatchFrom selects a Trade Match: a wishlist Card held in a different
// owner's tradelist. Shared by the count and the listing so they cannot disagree.
const tradeMatchFrom = `
	FROM card_lists AS w
	JOIN deckbox_users AS dw ON w.listId = dw.wishlistId
	JOIN card_lists AS t ON t.cardName = w.cardName
	JOIN deckbox_users AS dt ON t.listId = dt.tradelistId
	WHERE dt.deckboxLogin <> dw.deckboxLogin`

// AdminInsights builds the analytics page. Three of its queries scan card_lists
// (~0.5s each on a 200k-row database), so callers cache the result rather than
// recomputing it per request.
func (s *SQLiteStorage) AdminInsights(ctx context.Context) (deckbox.Insights, error) {
	const op = "storage.sqlite.AdminInsights"

	var in deckbox.Insights
	var err error

	// Demand: what people asked for and did not get, first.
	in.TopMisses, err = collect(ctx, s.db.readDB, `
	SELECT display, scope, hits, misses, last_seen
	FROM search_stats
	ORDER BY misses DESC, hits DESC
	LIMIT ?`, []any{insightsLimit},
		func(r *sql.Rows) (deckbox.TermDemand, error) {
			var t deckbox.TermDemand
			var scope string // Scope is a named type; the driver only scans into *string
			err := r.Scan(&t.Term, &scope, &t.Hits, &t.Misses, &t.LastSeen)
			t.Scope = deckbox.Scope(scope)
			return t, err
		})
	if err != nil {
		return in, fmt.Errorf("%s: demand: %w", op, err)
	}

	// Wishlist demand, which exists independently of anyone searching.
	in.MostWanted, err = collect(ctx, s.db.readDB, `
	SELECT w.cardName, COUNT(DISTINCT d.deckboxLogin) AS wishers
	FROM card_lists AS w
	JOIN deckbox_users AS d ON w.listId = d.wishlistId
	GROUP BY w.cardName
	ORDER BY wishers DESC, w.cardName
	LIMIT ?`, []any{insightsLimit},
		func(r *sql.Rows) (deckbox.CardDemand, error) {
			var c deckbox.CardDemand
			err := r.Scan(&c.CardName, &c.Wishers)
			return c, err
		})
	if err != nil {
		return in, fmt.Errorf("%s: most wanted: %w", op, err)
	}

	if err := s.db.readDB.QueryRowContext(ctx, `SELECT COUNT(*) `+tradeMatchFrom).Scan(&in.TradeMatchTotal); err != nil {
		return in, fmt.Errorf("%s: trade match count: %w", op, err)
	}

	in.TradeMatches, err = collect(ctx, s.db.readDB, `
	SELECT dw.deckboxLogin, w.cardName, dt.deckboxLogin, t.quantity `+tradeMatchFrom+`
	ORDER BY dw.deckboxLogin COLLATE NOCASE, w.cardName
	LIMIT ?`, []any{tradeMatchLimit},
		func(r *sql.Rows) (deckbox.TradeMatch, error) {
			var m deckbox.TradeMatch
			err := r.Scan(&m.Wisher, &m.CardName, &m.Holder, &m.Quantity)
			return m, err
		})
	if err != nil {
		return in, fmt.Errorf("%s: trade matches: %w", op, err)
	}

	// Supply concentration: how many Cards only one tradelist carries.
	in.Supply, err = collect(ctx, s.db.readDB, `
	SELECT owners, COUNT(*) AS cards FROM (
		SELECT c.cardName, COUNT(DISTINCT d.deckboxLogin) AS owners
		FROM card_lists AS c
		JOIN deckbox_users AS d ON c.listId = d.tradelistId
		GROUP BY c.cardName
	)
	GROUP BY owners
	ORDER BY owners
	LIMIT ?`, []any{supplyBucketLimit},
		func(r *sql.Rows) (deckbox.SupplyBucket, error) {
			var b deckbox.SupplyBucket
			err := r.Scan(&b.Owners, &b.Cards)
			return b, err
		})
	if err != nil {
		return in, fmt.Errorf("%s: supply: %w", op, err)
	}

	// The rollup, oldest first so the sparkline reads left to right.
	in.Daily, err = collect(ctx, s.db.readDB, `
	SELECT day, searches, misses FROM (
		SELECT day, searches, misses FROM daily_stats ORDER BY day DESC LIMIT ?
	) ORDER BY day ASC`, []any{dailyHistoryDays},
		func(r *sql.Rows) (deckbox.DayCount, error) {
			var d deckbox.DayCount
			err := r.Scan(&d.Day, &d.Searches, &d.Misses)
			return d, err
		})
	if err != nil {
		return in, fmt.Errorf("%s: daily: %w", op, err)
	}

	return in, nil
}

// collect runs a read query and scans every row with scan. The panel's queries
// differ only in their SQL and their scan, so the rows/Close/Err dance lives
// here once instead of being repeated at each call site.
func collect[T any](ctx context.Context, db *sql.DB, query string, args []any, scan func(*sql.Rows) (T, error)) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
