package sqlite

import (
	"FriendlyCardFinder/env"
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/dto"
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pressly/goose/v3"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// driverName is a sqlite3 driver variant whose connections get a 256 MB mmap
// window. mmap'd reads come straight out of the OS page cache without a read()
// syscall + copy per page, which speeds up searches once the DB outgrows the
// 8 MB connection cache. There is no DSN parameter for mmap_size, hence the
// ConnectHook.
const driverName = "sqlite3_mmap"

func init() {
	sql.Register(driverName, &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			// canonical_name backs exact search on the non-FTS fallback path
			// (see SearchCard), keeping its equality semantics — case-, accent-
			// and punctuation-insensitive — identical to the FTS build.
			if err := conn.RegisterFunc("canonical_name", canonicalCardName, true); err != nil {
				return err
			}
			_, err := conn.Exec("PRAGMA mmap_size=268435456", nil)
			return err
		},
	})
}

//go:embed migrations/*.sql
var embedMigrations embed.FS

type SQLiteStorage struct {
	db         *DB
	log        *slog.Logger
	ftsEnabled bool
	batchSize  int

	// stmtCache holds lazily prepared read statements keyed by SQL text. The hot
	// read queries (search, freshness checks) run on every user message, so we
	// prepare them once instead of parse+prepare per call. *sql.Stmt is safe for
	// concurrent use and re-prepares per pooled connection internally.
	stmtMu    sync.RWMutex
	stmtCache map[string]*sql.Stmt
}

const defaultCardBatchSize = 1000

type DB struct {
	writeDB *sql.DB
	readDB  *sql.DB
}

// buildDSN appends the per-connection PRAGMAs as DSN query parameters so every
// connection in a pool inherits them. Setting these via a single PRAGMA Exec on
// a multi-connection pool would only configure whichever one connection ran it.
//   - journal_mode=WAL is persisted in the DB header, but harmless to set per conn.
//   - synchronous=NORMAL and busy_timeout are per-connection.
//   - cache_size=-8000 gives each connection an 8 MB page cache.
func buildDSN(dataSourceName string) string {
	sep := "?"
	if strings.Contains(dataSourceName, "?") {
		sep = "&"
	}
	return dataSourceName + sep + "_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000&_cache_size=-8000"
}

func New(dataSourceName string, log *slog.Logger) (*SQLiteStorage, error) {
	const op = "storage.sqlite.New"

	dsn := buildDSN(dataSourceName)

	writeDB, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	writeDB.SetMaxOpenConns(1)

	readDB, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	readDB.SetMaxOpenConns(max(4, runtime.NumCPU()))

	db := &DB{
		writeDB: writeDB,
		readDB:  readDB,
	}

	goose.SetBaseFS(embedMigrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return nil, fmt.Errorf("%s: set goose dialect: %w", op, err)
	}
	if err := goose.Up(db.writeDB, "migrations"); err != nil {
		return nil, fmt.Errorf("%s: run migrations: %w", op, err)
	}

	// FTS5 virtual table is handled separately because its availability depends
	// on the SQLite build — we degrade gracefully if it is not supported.
	ftsEnabled := ensureFtsTable(db)

	// Read batch size from environment variable if set
	batchSize := defaultCardBatchSize
	if bsStr := env.CardListBatchSize.GetValue(); bsStr != "" {
		if parsed, err := strconv.Atoi(bsStr); err == nil && parsed > 0 {
			batchSize = parsed
		}
	}

	return &SQLiteStorage{
		db:         db,
		log:        log,
		ftsEnabled: ftsEnabled,
		batchSize:  batchSize,
		stmtCache:  make(map[string]*sql.Stmt),
	}, nil
}

// cachedStmt returns a cached prepared statement for query on the given pool,
// preparing and caching it on first use. Read and write queries never share
// SQL text, so one cache serves both pools.
func (s *SQLiteStorage) cachedStmt(ctx context.Context, db *sql.DB, query string) (*sql.Stmt, error) {
	s.stmtMu.RLock()
	stmt, ok := s.stmtCache[query]
	s.stmtMu.RUnlock()
	if ok {
		return stmt, nil
	}

	stmt, err := db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}

	s.stmtMu.Lock()
	if existing, ok := s.stmtCache[query]; ok {
		s.stmtMu.Unlock()
		stmt.Close()
		return existing, nil
	}
	s.stmtCache[query] = stmt
	s.stmtMu.Unlock()
	return stmt, nil
}

// readStmt returns a cached prepared statement for query on the read pool.
func (s *SQLiteStorage) readStmt(ctx context.Context, query string) (*sql.Stmt, error) {
	return s.cachedStmt(ctx, s.db.readDB, query)
}

// writeStmt returns a cached prepared statement for query on the write connection.
func (s *SQLiteStorage) writeStmt(ctx context.Context, query string) (*sql.Stmt, error) {
	return s.cachedStmt(ctx, s.db.writeDB, query)
}

func (s *SQLiteStorage) Close() error {
	s.stmtMu.Lock()
	for _, stmt := range s.stmtCache {
		stmt.Close()
	}
	s.stmtCache = make(map[string]*sql.Stmt)
	s.stmtMu.Unlock()

	var firstErr error
	if err := s.db.writeDB.Close(); err != nil {
		firstErr = fmt.Errorf("writeDB close: %w", err)
	}
	if err := s.db.readDB.Close(); err != nil {
		if firstErr != nil {
			return fmt.Errorf("%v; readDB close: %w", firstErr, err)
		}
		return fmt.Errorf("readDB close: %w", err)
	}
	return firstErr
}

func (s *SQLiteStorage) RegisterUser(ctx context.Context, user deckbox.BotUser) error {
	const op = "storage.sqlite.RegisterUser"

	stmt, err := s.writeStmt(ctx, `
	INSERT INTO users(telegramId, username, deckboxLogin)
	VALUES (?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("%s preparing statement: %w", op, err)
	}

	_, err = stmt.Exec(user.TelegramID, user.TelegramUsername, user.DeckboxLogin)
	if err != nil {
		return err
	}

	return nil
}

func (s *SQLiteStorage) SaveDeckboxUser(ctx context.Context, user deckbox.DeckboxUser) error {
	const op = "storage.sqlite.SaveDeckboxUser"

	stmt, err := s.writeStmt(ctx, `
	INSERT INTO deckbox_users(deckboxLogin, inventoryId, tradelistId, wishlistId)
	VALUES (?, ?, ?, ?)
	ON CONFLICT(deckboxLogin) DO UPDATE SET
		inventoryId = excluded.inventoryId,
		tradelistId = excluded.tradelistId,
		wishlistId  = excluded.wishlistId;`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	_, err = stmt.Exec(user.DeckboxLogin, user.InventoryID, user.TradelistID, user.WishlistID)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func (s *SQLiteStorage) UpdateDeckboxUserTimestamp(ctx context.Context, deckboxLogin string, updatedAt int64) error {
	const op = "storage.sqlite.UpdateDeckboxUserTimestamp"

	stmt, err := s.writeStmt(ctx, `
	UPDATE deckbox_users
	SET updated_at = ?
	WHERE deckboxLogin = ?
	`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	_, err = stmt.Exec(updatedAt, deckboxLogin)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func (s *SQLiteStorage) GetDeckboxUser(ctx context.Context, deckboxLogin string) (*deckbox.DeckboxUser, error) {
	const op = "storage.sqlite.GetDeckboxUser"

	stmt, err := s.readStmt(ctx, `
	SELECT deckboxLogin, inventoryId, tradelistId, wishlistId, updated_at
	FROM deckbox_users
	WHERE deckboxLogin = ?
	`)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	var inventoryId *int64
	var tradelistId *int64
	var wishlistId *int64
	var updatedAt *int64

	err = stmt.QueryRow(deckboxLogin).Scan(&deckboxLogin, &inventoryId, &tradelistId, &wishlistId, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	return &deckbox.DeckboxUser{
		DeckboxLogin: deckboxLogin,
		InventoryID:  inventoryId,
		TradelistID:  tradelistId,
		WishlistID:   wishlistId,
		UpdatedAt:    updatedAt,
	}, nil
}

func (s *SQLiteStorage) GetAllDeckboxUsersWithOldLists(ctx context.Context, thresholdSeconds int64) ([]string, error) {
	const op = "storage.sqlite.GetAllDeckboxUsersWithOldLists"

	stmt, err := s.readStmt(ctx, `
	SELECT deckboxLogin FROM deckbox_users
	WHERE updated_at IS NULL OR updated_at < ?
	`)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	rows, err := stmt.Query(thresholdSeconds)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer rows.Close()

	var logins []string
	for rows.Next() {
		var login string
		if err := rows.Scan(&login); err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		logins = append(logins, login)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	return logins, nil
}

func (s *SQLiteStorage) SaveCardList(ctx context.Context, list deckbox.CardList) error {
	const op = "storage.sqlite.SaveCardList"
	log := s.log

	totalCards := len(list.Cards)

	// Begin transaction. The old rows are cleared and the new rows inserted in a
	// single transaction so the replace is atomic (no window where the list reads
	// empty) and costs one commit/fsync instead of three separate writes.
	tx, err := s.db.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM card_lists WHERE listId = ?`, list.ListId); err != nil {
		return fmt.Errorf("%s: clear card_lists: %w", op, err)
	}
	if s.ftsEnabled {
		// MATCH on the indexed listId column finds the list's rows through the
		// FTS index; a plain `WHERE listId = ?` would scan the entire table.
		if _, err := tx.ExecContext(ctx, `DELETE FROM card_lists_fts WHERE card_lists_fts MATCH ?`, ftsListIdQuery(list.ListId)); err != nil {
			return fmt.Errorf("%s: clear card_lists_fts: %w", op, err)
		}
	}

	// An empty new list still needs the clear above committed.
	if totalCards == 0 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("%s: %w", op, err)
		}
		return nil
	}

	// Batch insert in chunks using configured batch size. The FTS insert binds
	// 4 params per card, so clamp the batch to stay under SQLite's 32,766
	// bound-parameter limit regardless of CARD_LIST_BATCH_SIZE.
	batchSize := min(s.batchSize, 8000)
	totalBatches := (totalCards + batchSize - 1) / batchSize

	startTime := time.Now()
	defer func() {
		log.Info("save card list finished", slog.String("operation", op), slog.Int64("list_id", list.ListId), slog.Duration("duration_ms", time.Since(startTime)))
	}()

	// All batches except the last have identical SQL, so prepare those
	// statements once and reuse them across batches.
	var fullStmt, fullFtsStmt *sql.Stmt
	defer func() {
		if fullStmt != nil {
			fullStmt.Close()
		}
		if fullFtsStmt != nil {
			fullFtsStmt.Close()
		}
	}()

	valueArgs := make([]any, 0, min(totalCards, batchSize)*3)
	ftsValueArgs := make([]any, 0, min(totalCards, batchSize)*4)
	batchNum := 0

	// flushBatch writes the accumulated args as one multi-row INSERT per table.
	flushBatch := func(batchCardCount int) error {
		isFullBatch := batchCardCount == batchSize

		stmt := fullStmt
		if stmt == nil || !isFullBatch {
			query := `INSERT INTO card_lists(listId, cardName, quantity) VALUES ` + placeholderRows(batchCardCount, 3)
			var err error
			stmt, err = tx.PrepareContext(ctx, query)
			if err != nil {
				log.Error(
					"failed to prepare batch statement",
					slog.String("operation", op),
					slog.Int64("list_id", list.ListId),
					slog.Int("batch_num", batchNum+1),
					slog.Int("batch_card_count", batchCardCount),
				)
				return fmt.Errorf("%s: prepare batch %d: %w", op, batchNum+1, err)
			}
			if isFullBatch {
				fullStmt = stmt
			}
			// One-off statements for the final partial batch are closed by the
			// transaction itself on Commit/Rollback.
		}

		if _, err := stmt.Exec(valueArgs...); err != nil {
			log.Error(
				"failed to execute batch statement",
				slog.String("operation", op),
				slog.Int64("list_id", list.ListId),
				slog.Int("batch_num", batchNum+1),
				slog.Int("batch_card_count", batchCardCount),
			)
			return fmt.Errorf("%s: execute batch %d: %w", op, batchNum+1, err)
		}

		// Insert into FTS table as well when enabled
		if s.ftsEnabled {
			ftsStmt := fullFtsStmt
			if ftsStmt == nil || !isFullBatch {
				ftsQuery := `INSERT INTO card_lists_fts(listId, cardName, cardName_normalized, quantity) VALUES ` + placeholderRows(batchCardCount, 4)
				var err error
				ftsStmt, err = tx.PrepareContext(ctx, ftsQuery)
				if err != nil {
					log.Error(
						"failed to prepare fts batch statement",
						slog.String("operation", op),
						slog.Int64("list_id", list.ListId),
						slog.Int("batch_num", batchNum+1),
						slog.Int("batch_card_count", batchCardCount),
					)
					return fmt.Errorf("%s: prepare fts batch %d: %w", op, batchNum+1, err)
				}
				if isFullBatch {
					fullFtsStmt = ftsStmt
				}
			}

			if _, err := ftsStmt.Exec(ftsValueArgs...); err != nil {
				log.Error(
					"failed to execute fts batch statement",
					slog.String("operation", op),
					slog.Int64("list_id", list.ListId),
					slog.Int("batch_num", batchNum+1),
					slog.Int("batch_card_count", batchCardCount),
				)
				return fmt.Errorf("%s: execute fts batch %d: %w", op, batchNum+1, err)
			}
		}

		log.Debug(
			"card list batch saved",
			slog.String("operation", op),
			slog.Int64("list_id", list.ListId),
			slog.Int("batch_num", batchNum+1),
			slog.Int("total_batches", totalBatches),
			slog.Int("batch_card_count", batchCardCount),
		)
		batchNum++
		valueArgs = valueArgs[:0]
		ftsValueArgs = ftsValueArgs[:0]
		return nil
	}

	// Stream the map directly into batches — no intermediate slice copy.
	pending := 0
	for cardName, quantity := range list.Cards {
		valueArgs = append(valueArgs, list.ListId, cardName, quantity)
		if s.ftsEnabled {
			ftsValueArgs = append(ftsValueArgs, list.ListId, cardName, normalizeASCII(cardName), quantity)
		}
		pending++
		if pending == batchSize {
			if err := flushBatch(pending); err != nil {
				return err
			}
			pending = 0
		}
	}
	if pending > 0 {
		if err := flushBatch(pending); err != nil {
			return err
		}
	}

	// Commit transaction
	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func (s *SQLiteStorage) ClearCardList(ctx context.Context, listId int64) error {
	const op = "storage.sqlite.ClearCardList"

	stmt, err := s.writeStmt(ctx, `
	DELETE FROM card_lists
	WHERE listId = ?
	`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	_, err = stmt.Exec(listId)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	// Also clear FTS table entries if present (non-fatal if FTS table does not exist)
	if s.ftsEnabled {
		_, _ = s.db.writeDB.Exec(`DELETE FROM card_lists_fts WHERE card_lists_fts MATCH ?`, ftsListIdQuery(listId))
	}

	return nil
}

func (s *SQLiteStorage) SearchCard(ctx context.Context, cardName string, scope string, exact bool) ([]dto.CardSearchDTO, error) {
	const op = "storage.sqlite.SearchCard"

	// determine which deckbox_users column to join on based on scope
	column := "tradelistId"
	switch scope {
	case deckbox.ScopeWishlist:
		column = "wishlistId"
	case deckbox.ScopeInventory:
		column = "inventoryId"
	}

	// Canonicalize the query once: the same string serves as the FTS phrase
	// body, the fallback's canonical_name() argument, and the per-row equality
	// filter below. A query with no tokens (all punctuation, e.g. a quoted
	// "//") has no canonical form to compare against, so it degrades to a
	// regular non-exact search instead of filtering every row out.
	var wantCanonical string
	if exact {
		wantCanonical = canonicalCardName(cardName)
		if wantCanonical == "" {
			exact = false
		}
	}

	var rows *sql.Rows

	// Prefer FTS5 based search for better performance and tokenized prefix matching.
	// Fall back to LIKE when FTS is disabled or the query reduces to no tokens
	// (e.g. an all-punctuation input like "//"), so callers always get LIKE semantics.
	if s.ftsEnabled {
		var matchQuery string
		if exact {
			matchQuery = ftsPhraseQuery(wantCanonical)
		} else {
			matchQuery = buildFtsQueryTerm(cardName)
		}
		if matchQuery != "" {
			// Match against normalized column to support accent-insensitive matching.
			// quantity is stored in the FTS table, so no join back to card_lists.
			sqlStmt := fmt.Sprintf(`
			SELECT fts.listId, fts.cardName, fts.quantity,
			       du.deckboxLogin, u.telegramId, u.username
			FROM card_lists_fts AS fts
			JOIN deckbox_users AS du ON fts.listId = du.%s
			LEFT JOIN users AS u ON du.deckboxLogin = u.deckboxLogin
			WHERE fts.card_lists_fts MATCH ?
			ORDER BY fts.listId ASC
			`, column)

			stmt, err := s.readStmt(ctx, sqlStmt)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", op, err)
			}

			rows, err = stmt.QueryContext(ctx, matchQuery)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", op, err)
			}
		}
		// matchQuery == "" means all-punctuation input — fall through to LIKE below
	}

	if rows == nil {
		// Fallback path (FTS disabled or the query has no tokens). Exact search
		// compares canonical forms via the canonical_name() Go function
		// registered in the ConnectHook, so exactness ignores case, accents,
		// and punctuation just like the FTS build. Both variants scan the
		// table (a leading-wildcard LIKE cannot use an index either).
		var predicate string
		var arg string
		if exact {
			predicate = "canonical_name(cl.cardName) = ?"
			arg = wantCanonical
		} else {
			predicate = "cl.cardName LIKE ?"
			arg = "%" + cardName + "%"
		}
		sqlStmt := fmt.Sprintf(`
		SELECT cl.listId, cl.cardName COLLATE NOCASE, cl.quantity,
		       du.deckboxLogin, u.telegramId, u.username
		FROM card_lists AS cl
		JOIN deckbox_users AS du ON cl.listId = du.%s
		LEFT JOIN users AS u ON du.deckboxLogin = u.deckboxLogin
		WHERE %s
		ORDER BY cl.listId ASC
		`, column, predicate)

		stmt, err := s.readStmt(ctx, sqlStmt)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}

		rows, err = stmt.QueryContext(ctx, arg)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
	}
	defer rows.Close()

	var results []dto.CardSearchDTO
	for rows.Next() {
		var listId int64
		var rowCardName string
		var quantity int16
		var deckboxLogin string
		var telegramId *int64
		var username *string
		err := rows.Scan(&listId, &rowCardName, &quantity, &deckboxLogin, &telegramId, &username)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}

		// The FTS phrase match may return names that merely contain the query
		// ("Sudden Shock" for "shock"), so exact search keeps only rows whose
		// canonical token form equals the query's.
		if exact && canonicalCardName(rowCardName) != wantCanonical {
			continue
		}

		results = append(results, dto.CardSearchDTO{
			ListId:           listId,
			CardName:         rowCardName,
			Quantity:         quantity,
			DeckboxLogin:     deckboxLogin,
			TelegramID:       telegramId,
			TelegramUsername: username,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	return results, nil
}

func (s *SQLiteStorage) GetOwnerByListId(ctx context.Context, listId int64) (*deckbox.CardListOwnerInfo, error) {
	const op = "storage.sqlite.GetOwnerByListId"

	stmt, err := s.readStmt(ctx, `
	SELECT du.deckboxLogin, u.telegramId, u.username
	FROM deckbox_users AS du
	LEFT JOIN users AS u ON du.deckboxLogin = u.deckboxLogin
	WHERE du.tradelistId = ? OR du.inventoryId = ? OR du.wishlistId = ?
	LIMIT 1
	`)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	var deckboxLogin string
	var telegramId *int64
	var username *string

	err = stmt.QueryRow(listId, listId, listId).Scan(&deckboxLogin, &telegramId, &username)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%s: owner not found for listId %d", op, listId)
		}
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	return &deckbox.CardListOwnerInfo{
		DeckboxLogin:     deckboxLogin,
		TelegramID:       telegramId,
		TelegramUsername: username,
	}, nil
}

// ftsColumnSpec is the current column layout of the search table, also used to
// recognize an up-to-date table in sqlite_master.
//   - listId is INDEXED so DELETE ... MATCH 'listId:<id>' uses the FTS index
//     instead of scanning the whole table on every list replace.
//   - cardName is display-only (searches always go through cardName_normalized),
//     so it is UNINDEXED to keep the index small and writes fast.
//   - quantity is stored (UNINDEXED) so a search reads everything from the FTS
//     table directly instead of joining every matched row back to card_lists.
const ftsColumnSpec = `(listId, cardName UNINDEXED, cardName_normalized, quantity UNINDEXED)`

// The table uses the default unicode61 tokenizer. queryTokens mirrors its
// token boundaries in Go for phrase queries and canonical comparison — any
// tokenize option added here must be reflected there.
const ftsCreateSQL = `CREATE VIRTUAL TABLE IF NOT EXISTS card_lists_fts USING fts5` + ftsColumnSpec + `;`

// ensureFtsTable attempts to create the FTS5 virtual table for fast searching.
// Returns true if the table exists with the current schema, false otherwise.
// A pre-existing table with an outdated column layout is dropped, recreated,
// and repopulated from card_lists.
func ensureFtsTable(db *DB) bool {
	// FTS5 may not be enabled in all SQLite builds. Don't treat failures as fatal.
	// We add a normalized column `cardName_normalized` that stores a lowercase,
	// diacritics-stripped version of the card name to support accent-insensitive searches.
	var schema string
	err := db.writeDB.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='card_lists_fts'`).Scan(&schema)
	switch {
	case err == sql.ErrNoRows:
		// Fresh database: create below.
	case err != nil:
		// log to standard logger; we don't want to depend on slog here during init
		fmt.Printf("warning: could not inspect FTS5 schema: %v\n", err)
		return false
	case strings.Contains(schema, ftsColumnSpec):
		return true
	default:
		// Outdated column layout: rebuild from card_lists.
		if _, err := db.writeDB.Exec(`DROP TABLE card_lists_fts`); err != nil {
			fmt.Printf("warning: could not drop outdated FTS5 table: %v\n", err)
			return false
		}
	}

	if _, err := db.writeDB.Exec(ftsCreateSQL); err != nil {
		fmt.Printf("warning: FTS5 virtual table not created: %v\n", err)
		return false
	}
	if err := repopulateFts(db); err != nil {
		fmt.Printf("warning: FTS5 table rebuild failed: %v\n", err)
		return false
	}
	return true
}

// repopulateFts fills an empty card_lists_fts from card_lists. Normalization
// must run in Go (SQLite lower() is ASCII-only and cannot strip diacritics), so
// rows are read through the read pool and inserted in batches.
func repopulateFts(db *DB) error {
	rows, err := db.readDB.Query(`SELECT listId, cardName, quantity FROM card_lists`)
	if err != nil {
		return err
	}

	type ftsRow struct {
		listId   int64
		cardName string
		quantity int64
	}
	var all []ftsRow
	for rows.Next() {
		var r ftsRow
		if err := rows.Scan(&r.listId, &r.cardName, &r.quantity); err != nil {
			rows.Close()
			return err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if len(all) == 0 {
		return nil
	}

	tx, err := db.writeDB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const batch = 1000
	args := make([]any, 0, batch*4)
	for start := 0; start < len(all); start += batch {
		end := min(start+batch, len(all))
		args = args[:0]
		for _, r := range all[start:end] {
			args = append(args, r.listId, r.cardName, normalizeASCII(r.cardName), r.quantity)
		}
		query := `INSERT INTO card_lists_fts(listId, cardName, cardName_normalized, quantity) VALUES ` + placeholderRows(end-start, 4)
		if _, err := tx.Exec(query, args...); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// placeholderRows returns n comma-separated placeholder tuples of cols columns,
// e.g. placeholderRows(2, 3) == "(?, ?, ?), (?, ?, ?)".
func placeholderRows(n, cols int) string {
	row := "(?" + strings.Repeat(", ?", cols-1) + ")"
	return row + strings.Repeat(", "+row, n-1)
}

// ftsListIdQuery returns the FTS5 MATCH expression selecting all rows of a list
// via the indexed listId column, e.g. "listId:12345".
func ftsListIdQuery(listId int64) string {
	return "listId:" + strconv.FormatInt(listId, 10)
}

// queryTokens splits a search string or card name into normalized (lowercase,
// accent-stripped) tokens, mirroring how the FTS5 unicode61 tokenizer splits
// stored card names: unicode61 treats all Unicode letter (L*) and number (N*)
// categories as token characters, hence unicode.IsNumber (Nd+Nl+No, e.g. "½")
// rather than unicode.IsDigit. Everything else (punctuation such as a hyphen)
// becomes a token boundary, so "Vitu-Ghazi" produces two tokens. If tokenizer
// options are ever added to ftsCreateSQL, this function must change in
// lockstep or exact phrase queries will silently stop matching.
func queryTokens(q string) []string {
	parts := strings.Fields(q)
	tokens := make([]string, 0, len(parts))
	for _, p := range parts {
		cleaned := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				return r
			}
			return ' '
		}, p)
		for sp := range strings.FieldsSeq(cleaned) {
			if norm := normalizeASCII(sp); norm != "" {
				tokens = append(tokens, norm)
			}
		}
	}
	return tokens
}

// buildFtsQueryTerm converts a user search string into an FTS5 MATCH query
// supporting prefix matching for each term (e.g., "light" ->
// "cardName_normalized:light*"). Every term carries an explicit column filter
// so numeric card-name searches can never match the indexed listId column.
func buildFtsQueryTerm(q string) string {
	tokens := queryTokens(q)
	if len(tokens) == 0 {
		return ""
	}
	terms := make([]string, len(tokens))
	for i, t := range tokens {
		terms[i] = "cardName_normalized:" + t + "*"
	}
	// Use AND to require all tokens to match
	return strings.Join(terms, " AND ")
}

// ftsPhraseQuery wraps a canonical card name (see canonicalCardName) in an
// FTS5 phrase query, e.g. "lightning bolt" -> `cardName_normalized:"lightning
// bolt"`. The phrase narrows candidates through the index; true exact equality
// is enforced by the canonicalCardName post-filter in SearchCard, because a
// phrase also matches names containing it with extra tokens ("Sudden Shock"
// for "shock"). Canonical tokens contain only letter/number runes, so
// embedding them in the quoted phrase is safe.
func ftsPhraseQuery(canonical string) string {
	return `cardName_normalized:"` + canonical + `"`
}

// canonicalCardName reduces a card name to its normalized token sequence
// ("Vitu-Ghazi, the City-Tree" -> "vitu ghazi the city tree"), so exact-match
// comparison ignores case, accents, and punctuation the same way FTS matching
// does.
func canonicalCardName(s string) string {
	return strings.Join(queryTokens(s), " ")
}

// normalizerPool holds NFD→strip-combining-marks→NFC transformer chains. Building
// the chain allocates ~8KB, so we reuse them across calls. x/text transformers are
// not safe for concurrent use, and SaveCardList normalizes from worker pools, so
// each goroutine borrows one from the pool. transform.String calls Reset itself.
var normalizerPool = sync.Pool{New: func() any {
	return transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
}}

// normalizeASCII removes diacritic marks (accents) and lowercases the string.
// It uses NFD normalization followed by removal of non-spacing marks, then recomposes.
func normalizeASCII(s string) string {
	if s == "" {
		return ""
	}
	// Fast path: pure-ASCII strings have no combining marks, so the Unicode
	// decomposition is a no-op — only lowercasing is needed. Card names are
	// overwhelmingly ASCII, so this avoids the expensive transformer almost always.
	if isASCII(s) {
		return strings.ToLower(s)
	}
	// Accented path: decompose, drop Mn marks, recompose, then lowercase.
	t := normalizerPool.Get().(transform.Transformer)
	out, _, err := transform.String(t, s)
	normalizerPool.Put(t)
	if err != nil {
		return strings.ToLower(s)
	}
	return strings.ToLower(out)
}

// isASCII reports whether s contains only ASCII bytes.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
