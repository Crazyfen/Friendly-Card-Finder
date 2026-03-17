package sqlite

import (
	"FriendlyCardFinder/env"
	"FriendlyCardFinder/internal/deckbox"
	"FriendlyCardFinder/internal/dto"
	"database/sql"
	"fmt"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	_ "github.com/mattn/go-sqlite3"
)

type SQLiteStorage struct {
	db         *DB
	ftsEnabled bool
	batchSize  int
}

const defaultCardBatchSize = 1000

type DB struct {
	writeDB *sql.DB
	readDB  *sql.DB
}

func New(dataSourceName string) (*SQLiteStorage, error) {
	const op = "storage.sqlite.New"

	writeDB, err := sql.Open("sqlite3", dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	writeDB.SetMaxOpenConns(1)

	readDB, err := sql.Open("sqlite3", dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	readDB.SetMaxOpenConns(max(4, runtime.NumCPU()))

	if _, err := writeDB.Exec("PRAGMA journal_mode = WAL;"); err != nil {
		return nil, fmt.Errorf("%s: set WAL: %w", op, err)
	}
	if _, err := writeDB.Exec("PRAGMA synchronous = NORMAL;"); err != nil {
		return nil, fmt.Errorf("%s: set synchronous: %w", op, err)
	}
	if _, err := writeDB.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		return nil, fmt.Errorf("%s: set busy_timeout: %w", op, err)
	}

	if _, err := readDB.Exec("PRAGMA journal_mode = WAL;"); err != nil {
		return nil, fmt.Errorf("%s: set WAL: %w", op, err)
	}
	if _, err := readDB.Exec("PRAGMA synchronous = NORMAL;"); err != nil {
		return nil, fmt.Errorf("%s: set synchronous: %w", op, err)
	}
	if _, err := readDB.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		return nil, fmt.Errorf("%s: set busy_timeout: %w", op, err)
	}

	db := &DB{
		writeDB: writeDB,
		readDB:  readDB,
	}

	err = createUsersTable(db.writeDB)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	err = createDeckboxUsersTable(db.writeDB)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	err = createCardListsTable(db.writeDB)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	// Try to create an FTS5 virtual table to support fast card name searches.
	// If the environment does not support FTS5, we gracefully continue without it.
	ftsEnabled := createFtsTable(db.writeDB)

	// Read batch size from environment variable if set
	batchSize := defaultCardBatchSize
	if bsStr := env.CardListBatchSize.GetValue(); bsStr != "" {
		if parsed, err := strconv.Atoi(bsStr); err == nil && parsed > 0 {
			batchSize = parsed
		}
	}

	return &SQLiteStorage{db: db, ftsEnabled: ftsEnabled, batchSize: batchSize}, nil
}

func (s *SQLiteStorage) Close() error {
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

func (s *SQLiteStorage) RegisterUser(user deckbox.BotUser) error {
	const op = "storage.sqlite.RegisterUser"

	stmt, err := s.db.writeDB.Prepare(`
	INSERT INTO users(telegramId, username, deckboxLogin)
	VALUES (?, ?, ?) 
	`)
	if err != nil {
		return fmt.Errorf("%s preparing statement: %w", op, err)
	}
	defer stmt.Close()

	_, err = stmt.Exec(user.TelegramID, user.TelegramUsername, user.DeckboxLogin)
	if err != nil {
		return err
	}

	return nil
}

func (s *SQLiteStorage) SaveDeckboxUser(user deckbox.DeckboxUser) error {
	const op = "storage.sqlite.SaveDeckboxUser"

	stmt, err := s.db.writeDB.Prepare(`
	INSERT OR IGNORE INTO deckbox_users(deckboxLogin, inventoryId, tradelistId, wishlistId)
	VALUES (?, ?, ?, ?);`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer stmt.Close()

	_, err = stmt.Exec(user.DeckboxLogin, user.InventoryID, user.TradelistID, user.WishlistID)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	stmt, err = s.db.writeDB.Prepare(`UPDATE deckbox_users
	SET inventoryId = ?, tradelistId = ?, wishlistId = ?
	WHERE deckboxLogin = ?
	`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer stmt.Close()

	_, err = stmt.Exec(user.InventoryID, user.TradelistID, user.WishlistID, user.DeckboxLogin)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func (s *SQLiteStorage) UpdateDeckboxUserTimestamp(deckboxLogin string, updatedAt int64) error {
	const op = "storage.sqlite.UpdateDeckboxUserTimestamp"

	stmt, err := s.db.writeDB.Prepare(`
	UPDATE deckbox_users
	SET updated_at = ?
	WHERE deckboxLogin = ?
	`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer stmt.Close()

	_, err = stmt.Exec(updatedAt, deckboxLogin)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func (s *SQLiteStorage) GetDeckboxUser(deckboxLogin string) (*deckbox.DeckboxUser, error) {
	const op = "storage.sqlite.GetDeckboxUser"

	stmt, err := s.db.readDB.Prepare(`
	SELECT deckboxLogin, inventoryId, tradelistId, wishlistId, updated_at
	FROM deckbox_users
	WHERE deckboxLogin = ?
	`)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer stmt.Close()

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

func (s *SQLiteStorage) GetAllDeckboxUsersWithOldLists(thresholdSeconds int64) ([]string, error) {
	const op = "storage.sqlite.GetAllDeckboxUsersWithOldLists"

	stmt, err := s.db.readDB.Prepare(`
	SELECT deckboxLogin FROM deckbox_users
	WHERE updated_at IS NULL OR updated_at < ?
	`)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer stmt.Close()

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

func (s *SQLiteStorage) SaveCardList(list deckbox.CardList) error {
	const op = "storage.sqlite.SaveCardList"
	log := slog.Default()

	err := s.ClearCardList(list.ListId)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	// Convert map to slice for consistent batching
	cards := make([]struct {
		name     string
		quantity int16
	}, 0, len(list.Cards))
	for cardName, quantity := range list.Cards {
		cards = append(cards, struct {
			name     string
			quantity int16
		}{cardName, quantity})
	}

	totalCards := len(cards)
	if totalCards == 0 {
		return nil
	}

	// Begin transaction
	tx, err := s.db.writeDB.Begin()
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer tx.Rollback()

	// Batch insert in chunks using configured batch size
	batchSize := s.batchSize
	totalBatches := (totalCards + batchSize - 1) / batchSize

	startTime := time.Now()
	defer func() {
		log.Info("save card list finished", slog.String("operation", op), slog.Int64("list_id", list.ListId), slog.Duration("duration_ms", time.Since(startTime)))
	}()

	for batchNum := range totalBatches {
		startIdx := batchNum * batchSize
		endIdx := min(startIdx+batchSize, totalCards)

		batchCards := cards[startIdx:endIdx]
		batchCardCount := len(batchCards)

		// Prepare inserts for card_lists
		valueStrings := make([]string, 0, batchCardCount)
		valueArgs := make([]any, 0, batchCardCount*3)

		// Prepare inserts for card_lists_fts (if enabled)
		ftsValueStrings := make([]string, 0, batchCardCount)
		// store three fields per FTS row: listId, cardName, cardName_normalized
		ftsValueArgs := make([]any, 0, batchCardCount*3)

		for _, card := range batchCards {
			valueStrings = append(valueStrings, "(?, ?, ?)")
			valueArgs = append(valueArgs, list.ListId)
			valueArgs = append(valueArgs, card.name)
			valueArgs = append(valueArgs, card.quantity)

			if s.ftsEnabled {
				ftsValueStrings = append(ftsValueStrings, "(?, ?, ?)")
				ftsValueArgs = append(ftsValueArgs, list.ListId)
				ftsValueArgs = append(ftsValueArgs, card.name)
				ftsValueArgs = append(ftsValueArgs, normalizeASCII(card.name))
			}
		}

		query := fmt.Sprintf(`
		INSERT INTO card_lists(listId, cardName, quantity)
		VALUES %s
		`, strings.Join(valueStrings, ", "))

		stmt, err := tx.Prepare(query)
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

		_, err = stmt.Exec(valueArgs...)
		stmt.Close()
		if err != nil {
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
		if s.ftsEnabled && len(ftsValueStrings) > 0 {
			ftsQuery := fmt.Sprintf(`
			INSERT INTO card_lists_fts(listId, cardName, cardName_normalized)
			VALUES %s
			`, strings.Join(ftsValueStrings, ", "))

			ftsStmt, err := tx.Prepare(ftsQuery)
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

			_, err = ftsStmt.Exec(ftsValueArgs...)
			ftsStmt.Close()
			if err != nil {
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

		log.Info(
			"card list batch saved",
			slog.String("operation", op),
			slog.Int64("list_id", list.ListId),
			slog.Int("batch_num", batchNum+1),
			slog.Int("total_batches", totalBatches),
			slog.Int("batch_card_count", batchCardCount),
		)
	}

	// Commit transaction
	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func (s *SQLiteStorage) ClearCardList(listId int64) error {
	const op = "storage.sqlite.ClearCardList"

	stmt, err := s.db.writeDB.Prepare(`
	DELETE FROM card_lists
	WHERE listId = ?
	`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer stmt.Close()

	_, err = stmt.Exec(listId)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	// Also clear FTS table entries if present (non-fatal if FTS table does not exist)
	_, _ = s.db.writeDB.Exec(`DELETE FROM card_lists_fts WHERE listId = ?`, listId)

	return nil
}

func (s *SQLiteStorage) SearchCard(cardName string, scope string) ([]dto.CardSearchDTO, error) {
	const op = "storage.sqlite.SearchCard"

	// determine which deckbox_users column to join on based on scope
	column := "tradelistId"
	switch scope {
	case "wishlist":
		column = "wishlistId"
	case "inventory":
		column = "inventoryId"
	}

	var rows *sql.Rows

	// Prefer FTS5 based search for better performance and tokenized prefix matching
	if s.ftsEnabled {
		matchQuery := buildFtsQueryTerm(cardName)
		if matchQuery == "" {
			return nil, nil
		}

		// Match against normalized column to support accent-insensitive matching
		sqlStmt := fmt.Sprintf(`
		SELECT cl.listId, cl.cardName COLLATE NOCASE, cl.quantity
		FROM card_lists AS cl
		JOIN card_lists_fts AS fts ON cl.listId = fts.listId AND cl.cardName = fts.cardName
		JOIN deckbox_users AS du ON cl.listId = du.%s
		WHERE fts.cardName_normalized MATCH ?
		ORDER BY cl.listId ASC, cl.cardName ASC
		`, column)

		stmt, err := s.db.readDB.Prepare(sqlStmt)
		if err != nil {
			// fall back to LIKE search if FTS isn't usable at runtime
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		defer stmt.Close()

		rows, err = stmt.Query(matchQuery)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
	} else {
		sqlStmt := fmt.Sprintf(`
		SELECT cl.listId, cl.cardName COLLATE NOCASE, cl.quantity
		FROM card_lists AS cl
		LEFT JOIN deckbox_users AS du ON cl.listId = du.%s
		WHERE du.deckboxLogin IS NOT NULL AND cl.cardName LIKE ? 
		ORDER BY cl.listId ASC, cl.cardName ASC
		`, column)

		stmt, err := s.db.readDB.Prepare(sqlStmt)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		defer stmt.Close()

		rows, err = stmt.Query("%" + cardName + "%")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
	}
	defer rows.Close()

	var results []dto.CardSearchDTO
	for rows.Next() {
		var listId int64
		var cardName string
		var quantity int16
		err := rows.Scan(&listId, &cardName, &quantity)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}

		results = append(results, dto.CardSearchDTO{
			ListId:   listId,
			CardName: cardName,
			Quantity: quantity,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	return results, nil
}

func (s *SQLiteStorage) GetOwnerByListId(listId int64) (*deckbox.CardListOwnerInfo, error) {
	const op = "storage.sqlite.GetOwnerByListId"

	stmt, err := s.db.readDB.Prepare(`
	SELECT du.deckboxLogin, u.telegramId, u.username
	FROM deckbox_users AS du
	LEFT JOIN users AS u ON du.deckboxLogin = u.deckboxLogin
	WHERE du.tradelistId = ? OR du.inventoryId = ? OR du.wishlistId = ?
	LIMIT 1
	`)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer stmt.Close()

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

// createFtsTable attempts to create an FTS5 virtual table for fast searching.
// Returns true if the table was successfully created or already exists, false otherwise.
func createFtsTable(db *sql.DB) bool {
	// FTS5 may not be enabled in all SQLite builds. Don't treat failures as fatal.
	// We add a normalized column `cardName_normalized` that stores a lowercase,
	// diacritics-stripped version of the card name to support accent-insensitive searches.
	_, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS card_lists_fts USING fts5(listId UNINDEXED, cardName, cardName_normalized);`)
	if err != nil {
		// log to standard logger; we don't want to depend on slog here during init
		fmt.Printf("warning: FTS5 virtual table not created: %v\n", err)
		return false
	}
	return true
}

// buildFtsQueryTerm converts a user search string into an FTS5 MATCH query supporting
// prefix matching for each term (e.g., "light" -> "light*").
func buildFtsQueryTerm(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}

	// Normalize: split into parts, replace non-alphanumeric characters with spaces,
	// then ASCII-normalize and lower-case each token so accents don't prevent matches.
	parts := strings.Fields(q)
	tokens := make([]string, 0, len(parts))
	for _, p := range parts {
		cleaned := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			// replace punctuation (e.g., hyphen) with space so names like "Vitu-Ghazi" become two tokens
			return ' '
		}, p)
		for sp := range strings.FieldsSeq(cleaned) {
			// strip any leftover quotes just in case
			sp = strings.ReplaceAll(sp, "'", "")
			sp = strings.ReplaceAll(sp, "\"", "")
			if sp != "" {
				norm := normalizeASCII(sp)
				if norm != "" {
					tokens = append(tokens, norm+"*")
				}
			}
		}
	}

	if len(tokens) == 0 {
		return ""
	}
	// Use AND to require all tokens to match
	return strings.Join(tokens, " AND ")
}

// normalizeASCII removes diacritic marks (accents) and lowercases the string.
// It uses NFD normalization followed by removal of non-spacing marks, then recomposes.
func normalizeASCII(s string) string {
	if s == "" {
		return ""
	}
	// Decompose, drop Mn marks, recompose
	t := transform.Chain(norm.NFD, transform.RemoveFunc(func(r rune) bool {
		return unicode.Is(unicode.Mn, r)
	}), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		return strings.ToLower(s)
	}
	return strings.ToLower(out)
}

func createUsersTable(db *sql.DB) error {
	const op = "storage.sqlite.createUsersTable"

	stmt, err := db.Prepare(`
    CREATE TABLE IF NOT EXISTS users(
        telegramId INTEGER PRIMARY KEY,
        deckboxLogin TEXT NOT NULL UNIQUE,
        username TEXT NOT NULL UNIQUE
        );
    `)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer stmt.Close()

	_, err = stmt.Exec()
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	// Create indexes
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_deckboxLogin ON users(deckboxLogin);`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_username ON users(username);`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func createDeckboxUsersTable(db *sql.DB) error {
	const op = "storage.sqlite.createDeckboxUsersTable"

	stmt, err := db.Prepare(`
    CREATE TABLE IF NOT EXISTS deckbox_users(
        deckboxLogin TEXT PRIMARY KEY,
        inventoryId INTEGER,
        tradelistId INTEGER,
        wishlistId INTEGER,
        updated_at INTEGER
        );
    `)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer stmt.Close()

	_, err = stmt.Exec()
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	// Create indexes
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_inventoryId ON deckbox_users(inventoryId);`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_tradelistId ON deckbox_users(tradelistId);`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_wishlistId ON deckbox_users(wishlistId);`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func createCardListsTable(db *sql.DB) error {
	const op = "storage.sqlite.createCardListsTable"

	stmt, err := db.Prepare(`
    CREATE TABLE IF NOT EXISTS card_lists(
        listId INTEGER NOT NULL,
        cardName TEXT NOT NULL,
        quantity INTEGER NOT NULL
        );
    `)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer stmt.Close()

	_, err = stmt.Exec()
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	// Create indexes
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_listId ON card_lists(listId);`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_cardName ON card_lists(cardName);`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_listIdToCardName ON card_lists(listId, cardName);`)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}
