# Copilot Instructions for FriendlyCardFinder

## Project Overview

FriendlyCardFinder is a Telegram bot that helps Magic: The Gathering card collectors search and manage their Deckbox collections. The bot scrapes user profile data from deckbox.org and stores card inventory information in SQLite.

## Architecture

### Core Components

**Telegram Bot Integration** ([main.go](../main.go))

- Uses `github.com/go-telegram/bot` for Telegram API
- Three handler types: `/start` (welcome), `/deckbox` (registration), default text (card search)
- Message splitting for responses >4096 chars (Telegram limit) - see `splitMessage()`
- Context carries logger and storage instance to all handlers

**Deckbox Integration** (`internal/deckbox/`)

- `scrapper.go`: HTML scraping with Colly to extract user profile (inventory/tradelist/wishlist IDs)
- `handler.go`: Business logic for commands:
  - `NewUser`: Register new deckbox user
  - `GetProfileData`: Async fetch and save user profile
  - `SearchCard`: Query storage for cards
  - `RefreshStaleUserLists`: Check timestamp and auto-refresh stale lists before search
  - `SuggestDeckbox`: Batch update with freshness filtering
  - `refreshUserListsWorker`: Shared worker logic for fetching and saving card lists
  - `runUserRefreshWorkerPool`: Reusable worker pool coordinator
- `deckbox.go`: Domain models (BotUser, DeckboxUser, CardList) and DeckboxSaver interface
- Profile fetch runs asynchronously as goroutine after user registration
- Auto-refresh: Worker pool pattern with configurable workers (5 for stale lists, 3 for batch updates)

**Storage Layer** (`internal/storage/sqlite/`)

- Separate read/write connections: writeDB (1 connection, serialized) and readDB (parallel, CPU count)
- Tables: users, deckbox_users, card_lists (auto-created on init)
- Uses INSERT OR IGNORE for idempotent upserts
- Storage implements `DeckboxSaver` interface (abstract dependency)
- **SaveCardList**: Batch insert pattern (1,000 cards per statement) within transaction to handle large collections (>10,922 cards)

**Configuration** (`internal/config/`, `env/`)

- Loads from .env file via godotenv
- Required vars: BOT_TOKEN, STORAGE_PATH, ENV (local/dev/prod), and deckbox auth — either DECKBOX_LOGIN+DECKBOX_PASSWORD or DECKBOX_SESSION_COOKIE
- Optional vars: DECKBOX_SESSION_COOKIE (manual `_tcg_session` override; used verbatim if set, skipping login), FRESHNESS_TIME_LIMIT_HOURS (default 24), CARD_LIST_REFRESH_HOURS (default 3), CARD_LIST_BATCH_SIZE (default 1000)
- **CARD_LIST_BATCH_SIZE** controls the per-transaction batch size used by `SaveCardList()` (default 1000) — set via `env.CardListBatchSize`.
- ENV controls logger level (debug for local/dev, info for prod)

### Data Flow

1. User sends Telegram message → handler receives bot.Update
2. For `/deckbox <username>`: RegisterUser → async GetProfileData → scrape deckbox.org → SaveDeckboxUser + UpdateCardList (async)
3. **For text messages (card search)**:
   - RefreshStaleUserLists: Query users with updated_at older than 3 hours (or NULL)
   - 5 concurrent workers fetch profiles and save all 3 lists (inventory, tradelist, wishlist)
   - Wait for all refreshes to complete
   - SearchCard → query storage for matches → format results → split/send
4. For `/suggestdeckbox` (batch refresh): Worker pool with freshness checking, skips fresh data
5. All storage ops use DeckboxSaver interface (testable, mockable)

## Key Patterns & Conventions

**Logging with Operation Context** ([internal/lib/logger/sl/sl.go](../internal/lib/logger/sl/sl.go))

```go
const op = "package.function.name"
log = log.With(slog.String("operation", op), slog.String("context_field", value))
// Error with shorthand: sl.Err(err) wraps error field
```

All errors logged with operation name for traceability. Batch operations log batch_num and batch_card_count on failure.

**Error Handling**

- Database operations return `error` directly (no wrapping in most cases)
- Use `fmt.Errorf("%s: %w", op, err)` in initialization code
- `MustLoad()` pattern for required config - panics on failure

**Interface-Based Storage** ([internal/deckbox/deckbox.go](../internal/deckbox/deckbox.go))

```go
type DeckboxSaver interface {
    RegisterUser(user BotUser) error
    SaveDeckboxUser(user DeckboxUser) error
    SaveCardList(list CardList) error
    // ... more methods
}
```

All deckbox operations accept `DeckboxSaver` - swap implementations for testing.

**SQLite Batch Insert Pattern** ([internal/storage/sqlite/sqlite.go#L208](../internal/storage/sqlite/sqlite.go))

Large MTG collections can exceed SQLite's 32,766 parameter limit in a single INSERT. `SaveCardList()` batches cards into 1,000-card chunks within a transaction:

```go
const batchSize = 1000
// Prepare transaction, loop through batches, execute per batch
// Commit on success, rollback on any error (atomic)
log.Info("card list batch saved", slog.Int("batch_num", n), slog.Int("total_batches", totalBatches))
```

On error: logs batch_num and batch_card_count, then returns error with batch context.

**Prepared Statements**

- Always use `Prepare()` + `Exec()` for SQL (prevents injection)
- Remember `defer stmt.Close()` immediately after Prepare
- For batch statements within transactions, call Prepare on tx, not db

**Goroutines Without Waiting**

- `go GetProfileData(...)` spawned without wait groups for user registration (async, fire-and-forget)
- `RefreshStaleUserLists` uses sync.WaitGroup for worker pool to wait for completion

**Worker Pool Pattern** ([internal/deckbox/handler.go](../internal/deckbox/handler.go))

Parallel collection refresh uses bounded worker pool with reusable coordinator:

```go
// Private helper - fetches profile and all 3 card lists for a single user
func refreshUserListsWorker(log, storage, scraper, login) (cardCount int, errStr string)

// Reusable worker pool coordinator - handles job distribution and result collection
func runUserRefreshWorkerPool(log, storage, scraper, logins, numWorkers, shouldProcessFn)

// Auto-refresh: Gets stale users (updated_at older than threshold) and refreshes them
func RefreshStaleUserLists(log, storage, scraper, refreshHours)
```

- `RefreshStaleUserLists` uses 5 concurrent workers by default for maximum throughput
- `SuggestDeckbox` reuses the same worker pool with 3 workers + freshness check callback
- Each worker logs with `worker_id` for debugging parallel execution
- `shouldProcessFn` callback allows filtering (e.g., freshness checks in SuggestDeckbox)
- Results collected in order for consistent processing

## Developer Workflows

**Build & Run**

- The project uses `github.com/joho/godotenv` to load `.env` from the repo root. Ensure the following vars are set: `BOT_TOKEN`, `STORAGE_PATH`, and deckbox auth (`DECKBOX_LOGIN`+`DECKBOX_PASSWORD`, or `DECKBOX_SESSION_COOKIE`). Change `ENV` to `local`/`dev`/`prod` to control logger level.

```bash
# build
go build ./...
# run (loads .env automatically)
go run main.go
```

- To enable FTS5 support (for `github.com/mattn/go-sqlite3` builds that require the tag), build and run with Go build tags:

```bash
# build with FTS5 enabled
go build -tags "fts5" ./...
# run with FTS5 enabled
go run -tags "fts5" main.go
```

- For local debugging, set `ENV=local` in `.env` to enable debug logging (`slog.LevelDebug`).

**Testing**

- **Unit tests**: Mock `DeckboxSaver` for isolated logic tests (see `internal/deckbox/handler_test.go` for `fakeStorage` examples).
- **Integration tests**: `scrapper_test.go` performs real HTTP calls against deckbox.org and needs deckbox auth (`DECKBOX_LOGIN`+`DECKBOX_PASSWORD`, or a `DECKBOX_SESSION_COOKIE` override); they skip when neither is set. These tests require network access and stable responses. Offline scraper unit tests (`parseAuthenticityToken`, cookie file round-trip in `scrapper_internal_test.go`) always run.
- **Storage tests** ([internal/storage/sqlite/sqlite_test.go](../internal/storage/sqlite/sqlite_test.go)) verify batching and transactional behavior:
  - `TestSaveCardListWithLargeCollection`: 20,000 cards verify batching works
  - `TestSaveCardListBatching`: Batch boundary tests (0, 1, 500, 1000, 1500, 3500, 15000 cards)
  - `TestSaveCardListTransactionRollback`: Verify ClearCardList + batch inserts are atomic

Run tests (examples):

```bash
# All tests
go test ./...

# All tests (exercise FTS5 code path if driver is built with FTS5)
go test -tags "fts5" ./...

# Run a single package verbosely
go test -v ./internal/storage/sqlite/

# Run a single package with FTS5 enabled
go test -tags "fts5" -v ./internal/storage/sqlite/

# Run a single test
go test ./internal/storage/sqlite -run TestSaveCardListBatching -v

# Use the race detector
go test -race ./...
```

Note: when iterating on `scrapper.go` or `scrapper_test.go` set deckbox auth (`DECKBOX_LOGIN`+`DECKBOX_PASSWORD`, or a `DECKBOX_SESSION_COOKIE` override) and expect network flakiness — add retries when reproducing failures locally.

**Debugging**

- Change ENV to "local" in .env for JSON debug logs with slog.LevelDebug
- All handlers log message.ID for correlation with Telegram chat history
- `splitMessage()` (in `main.go`) prefers to cut at the nearest newline before the 4096-byte limit and correctly handles UTF-8 rune boundaries — see `main_test.go` for unit tests and a benchmark.
- Operation context visible in every log line
- Batch insert failures log batch_num, total_batches, batch_card_count to identify large collection issues

## External Dependencies

| Dependency                    | Usage                            |
| ----------------------------- | -------------------------------- |
| `github.com/go-telegram/bot`  | Telegram API client              |
| `github.com/gocolly/colly`    | HTML scraping (deckbox profiles) |
| `github.com/mattn/go-sqlite3` | SQLite driver                    |
| `github.com/joho/godotenv`    | Load .env configuration          |

## Common Modifications

**Adding a New Command**

1. Add handler in main.go: `b.RegisterHandler(bot.HandlerTypeMessageText, "/cmd", ...)`
2. Create business logic in deckbox/handler.go
3. Use context values: `log := ctx.Value(logKey).(*slog.Logger)`

**Testing async flows:** Some handlers spawn background goroutines (e.g., `NewUser` calls `go GetProfileData(...)`). When writing tests, either invoke the worker functions directly (call `GetProfileData`/`UpdateCardList`) or use a mock `DeckboxSaver`/test `Scraper` with synchronization (channels, waitgroups) to observe side effects deterministically.

**Adding Storage Operations**

1. Add method to DeckboxSaver interface
2. Implement in sqlite/sqlite.go with prepared statement
3. For bulk operations: use batch pattern + transaction like SaveCardList
4. Write test by mocking interface or creating integration test in sqlite_test.go

**Handling Large Data Inserts**

1. Batch into ~1,000 item chunks (3,000 SQL parameters per statement)
2. Use transaction with `Begin()` + `Commit()` for atomicity
3. Log batch progress with slog (batch_num, total_batches, item_count)
4. On error, include batch context in error message

**Agent quick tasks (start here) 🔧**

- Add a bot command: update `main.go` to register the handler, add handler tests in `main_test.go` and implementation in `internal/deckbox/handler.go`.
- Change export parsing: update `internal/deckbox/scrapper.go` (`FetchCardList`) and `internal/deckbox/scrapper_test.go`.
- Increase batch size or change batching behaviour: update `CARD_LIST_BATCH_SIZE` in `.env` or the `CardListBatchSize` in `internal/config/config.go` and adjust tests in `internal/storage/sqlite/sqlite_test.go`.
- Speed up searches: check FTS5 creation in `createFtsTable()` and `SearchCard()` logic in `internal/storage/sqlite/sqlite.go` — add tests to assert FTS vs LIKE behavior.

## Special Notes

- **SQLite variable limit**: Default is 32,766 parameters per statement. 3 parameters per card = ~10,922 cards max per INSERT. Use batching for larger collections.
- **Turkish locale bug risk**: Scraper extracts from HTML - HTML entities (e.g., &ouml;) need decoding (see `html.UnescapeString()`)
- **Message ID correlation**: Store `message.ID` in logs for chat history debugging
- **SQLite concurrent write**: `writeDB` limited to 1 connection to prevent write contention; `readDB` is opened with max open connections = max(4, runtime.NumCPU()). The DB is initialized with `PRAGMA journal_mode = WAL`, `PRAGMA synchronous = NORMAL`, and `PRAGMA busy_timeout = 5000`.
- **FTS5**: The code attempts to create an FTS5 virtual table (`card_lists_fts`) at startup. If FTS5 isn't available in the SQLite build, the code falls back to a LIKE-based search; when present the FTS index stores a normalized `cardName_normalized` for accent-insensitive matching. To exercise the FTS5 code path (and run FTS-related tests), build or test with the Go build tag: `-tags "fts5"` (e.g., `go test -tags "fts5" ./...` or `go run -tags "fts5" main.go`).
- **Telegram markdown escaping**: Special chars need backslash escape (seen in /start message)
- **Deckbox session cookie**: Session expires; refresh logic in scraper validates HTML response structure for stale auth
- **Card list refresh threshold**: CARD_LIST_REFRESH_HOURS (default 3) controls stale data detection - users with updated_at older than this get refreshed before searches
- **Freshness check callback**: SuggestDeckbox uses shouldProcessFn to skip already-fresh data - independent of CARD_LIST_REFRESH_HOURS threshold
