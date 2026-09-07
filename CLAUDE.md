# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Telegram bot for searching Magic: The Gathering cards across Deckbox.org user collections. Scrapes Deckbox profiles, stores inventory/tradelist/wishlist in SQLite, responds to search queries.

## Commands

```bash
go build ./...
go build -tags "fts5" ./...                            # with FTS5
go run main.go
go run -tags "fts5" main.go
go test ./...
go test -tags "fts5" ./...
go test -v ./internal/storage/sqlite/
go test ./internal/storage/sqlite -run TestSaveCardListBatching -v
go test -race ./...
```

FTS5 tests skip automatically when the driver lacks FTS5 support. Scraper integration tests (`scrapper_test.go`) need deckbox auth (`DECKBOX_LOGIN`+`DECKBOX_PASSWORD`, or a `DECKBOX_SESSION_COOKIE` override) and network access; they skip otherwise. The offline scraper unit tests (`parseAuthenticityToken`, cookie file round-trip) always run.

## Architecture

```
main.go                      Bot setup, handler registration, Telegram plumbing
internal/
  config/config.go           .env loading (MustLoad panics on missing required vars)
  deckbox/
    deckbox.go               Domain types + Query/SearchResult + DeckboxSaver interface
    scrapper.go              net/http + goquery scraper (shared keep-alive client) + Deckbox login/cookie auth
    handler.go               Deckbox receiver: Register, Search, RefreshStale, Suggest
  storage/sqlite/sqlite.go   SQLite implementation of DeckboxSaver
  telegram/render.go         Search results → Telegram HTML; CommandArgument
  i18n/i18n.go               RU/EN translations via T(lang, key)
  lib/logger/sl/sl.go        sl.Err(err) helper for slog
env/env.go                   EnvKey type + godotenv Load()
```

`internal/deckbox` deals only in domain values — it imports neither `i18n` nor the Telegram SDK. All wording and markup live in `internal/telegram`.

### Data flow

1. **Card search** (default text handler): a background ticker runs `RefreshStale` (users older than `CARD_LIST_REFRESH_HOURS`, default 3h) so searches never block on a refresh → `Deckbox.Search` → `telegram.RenderSearch` → split/send (4096-byte Telegram limit; `tgutil.SplitMessage` cuts on newlines, respects UTF-8 runes). A query wrapped in quotes (`"Shock"`; also `''`/`«»`/`“”`/`‘’` and low-9 `„“`/`„”` — see `exactQuotePairs`) is an exact-name match: `ParseQuery` strips the quotes and sets `Query.Exact`. One search handles 1..n names: `Search` parses each line once up front, so `SearchQueries`/`NotFound` echo the stripped names, and runs the per-card queries concurrently (8 max) over the read pool, merging deterministically. `RenderSearch` picks the single-card layout (per-owner card lines, Deckbox link pre-filtered to the query) when one name was searched and the ranked multi-card layout otherwise
2. **Registration** (`/deckbox <login>`): `Register` → `RegisterUser` → background refresh through the **same** worker pool every other caller uses (1 worker, 2-minute timeout), so registration also stamps `updated_at`
3. **Batch refresh** (`/suggestdeckbox <logins>`): 5-worker pool with a freshness pre-filter
4. **Wishlist search** (`/sell`): identical to card search with `ScopeWishlist`

### Storage design

- **Dual connections**: `writeDB` (1 conn, serialized) + `readDB` (`max(4, NumCPU)` conns, parallel reads)
- **WAL mode**: `PRAGMA journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000`
- **Batch inserts**: `batchWriter` is the single place the batching policy lives — row clamp (`maxBoundParams / len(cols)`, SQLite's 32,766 bound-parameter limit), reuse of the prepared full-size statement, and failure logging. `SaveCardList` streams the card map into one writer per table inside a single transaction; `repopulateFts` uses the same writer
- **Grouped results**: `SearchCard` takes a `deckbox.Query` and returns `[]deckbox.CardListWithOwner` — rows are grouped into Card Lists here (they arrive ordered by `listId`), so neither the row layout nor the ordering contract leaks to callers
- **FTS5**: virtual table `card_lists_fts(listId, cardName UNINDEXED, cardName_normalized, quantity UNINDEXED)` — `listId` is indexed so list replaces delete via `MATCH 'listId:<id>'` (no full-table scan); `cardName_normalized` (NFD accent-stripped) gives accent-insensitive prefix search with explicit `cardName_normalized:` column filters; `quantity` is stored so searches never join back to `card_lists`; falls back to `LIKE` if unavailable. `ensureFtsTable` auto-rebuilds the table from `card_lists` whenever the column layout (`ftsColumnSpec`) is outdated. Exact search (`exact` flag) canonicalizes the query once (`canonicalCardName`), narrows candidates with an FTS phrase query (`ftsPhraseQuery`) and post-filters rows by canonical equality — exactness ignores case, accents, and punctuation. The non-FTS fallback gets identical semantics via the `canonical_name()` SQL function registered in the driver's ConnectHook. A query with no tokens (all punctuation, e.g. quoted `"//"`) degrades to a regular non-exact search. `queryTokens` mirrors the default unicode61 tokenizer (letters + all number categories); any tokenize option added to `ftsCreateSQL` must be reflected there
- **Prepared statement cache**: hot read/write queries are prepared once via `readStmt`/`writeStmt` and cached for the storage lifetime
- **mmap**: custom `sqlite3_mmap` driver sets a 256 MB `mmap_size` per connection (no DSN param exists for it)
- **Unchanged-list skip**: `saveCardListIfChanged` (a `Deckbox` method) hashes the raw export body (FNV-64a, `CardList.BodyHash`) and skips the delete+insert when it matches the last successful save for that list; the cache lives on the `Deckbox` receiver, is in-memory only, and is populated strictly after successful saves

### Scraper auth

All profile/export fetches go through one shared `http.Transport` (keep-alive, 16 idle conns/host) via `fetchPage`, so worker pools reuse TLS connections instead of handshaking per request. `Scraper` resolves the Deckbox `_tcg_session` cookie lazily in this order: in-memory cache → `DECKBOX_SESSION_COOKIE` override → persisted file (`<storage dir>/deckbox_session`, `0600`) → fresh login. `doLogin` GETs `/accounts/login` (cookie jar captures the session cookie, `parseAuthenticityToken` scrapes the Rails CSRF token), then POSTs credentials (302 = success). `FetchCardList` detects a rejected cookie (response is the login page) and calls `refreshCookie` to re-login once. A `sync.Mutex` serializes logins (single-flight) so the worker pools don't all authenticate at once.

### Worker pool

`Deckbox.refreshUsers` is the reusable coordinator; `Deckbox.refreshUser` fetches profile + 3 lists for one user and stamps `updated_at`. Every caller goes through it — there is no second refresh path:

- `RefreshStale` — 10 workers, no filter
- `Suggest` — 5 workers + freshness pre-filter
- `Register` — 1 worker, in the background with a 2-minute timeout

## Conventions

**Logging**: every function sets `const op = "package.FunctionName"` and uses `log.With(slog.String("operation", op))`. Error field via `sl.Err(err)`. `batchWriter` logs `table`/`batch_num`/`batch_row_count` on failure.

**Errors**: storage ops return `error` directly; `fmt.Errorf("%s: %w", op, err)` in init code only.

**Testing async handlers**: build a `Deckbox` with `New` over a fake `DeckboxSaver` + fake `Fetcher` (see `handler_test.go`), then call the method directly. Background work (registration's refresh) is observed with a channel the fake closes.

**Adding a command**:

1. Register in `main.go`: `b.RegisterHandler(bot.HandlerTypeMessageText, "/cmd", ...)`
2. Handler method on `App`; pull the argument with `telegram.CommandArgument`
3. Domain logic as a method on `Deckbox` in `internal/deckbox/handler.go` — it returns values, never formatted text
4. User-facing wording → `internal/i18n` + `internal/telegram`
5. New storage needed → add to `DeckboxSaver` interface → implement in `sqlite.go` → test in `sqlite_test.go`

**Bulk storage ops**: `Begin()` transaction + one `batchWriter` per table; never hand-roll placeholder batching.

## Configuration (.env)

| Variable                 | Required | Default | Description                                                                   |
| ------------------------ | -------- | ------- | ----------------------------------------------------------------------------- |
| `BOT_TOKEN`              | yes      | —       | Telegram bot token                                                            |
| `STORAGE_PATH`           | yes      | —       | SQLite file path                                                              |
| `DECKBOX_LOGIN`          | yes\*    | —       | Deckbox account login/email; bot logs in to obtain the session cookie         |
| `DECKBOX_PASSWORD`       | yes\*    | —       | Deckbox account password                                                      |
| `DECKBOX_SESSION_COOKIE` | no       | —       | Optional manual `_tcg_session` override; used verbatim if set, skipping login |
| `ENV`                    | yes      | —       | `local`/`dev` = DEBUG logs; `prod` = INFO                                     |
| `FRESHNESS_TIME_LIMIT_HOURS` | no   | 24      | Hours before `Suggest` treats data as stale                                   |
| `CARD_LIST_REFRESH_HOURS` | no      | 3       | Hours before auto-refresh triggers on search                                  |
| `CARD_LIST_BATCH_SIZE`   | no       | 1000    | Cards per INSERT batch (read in `sqlite.go`, not `config.go`)                 |

\* Auth requires **either** `DECKBOX_LOGIN`+`DECKBOX_PASSWORD` **or** `DECKBOX_SESSION_COOKIE`; `MustLoad` fatals if neither is present.

## CI/CD

GitHub Actions on push to `main`: `go test -tags fts5 -race ./...` → build+push image to `ghcr.io/crazyfen/friendlycardfinder` → SCP `docker-compose.yml` to VPS → `docker compose pull && up -d`. Secrets: `SSH_HOST`, `SSH_USER`, `SSH_PRIVATE_KEY`. VPS `.env` is maintained manually at `~/friendlycardfinder/`.

## graphify

This project has a graphify knowledge graph at graphify-out/.

Rules:

- For architecture or codebase questions you cannot answer from the files already in context, read graphify-out/GRAPH_REPORT.md for god nodes and community structure
- If graphify-out/wiki/index.md exists, navigate it instead of reading raw files
- After modifying code files in this session, run `python3 -c "from graphify.watch import _rebuild_code; from pathlib import Path; _rebuild_code(Path('.'))"` to keep the graph current

## Working agreements

Keep responses focused, brief, and concise. Keep disclaimers and caveats short, and spend most of the response on the main answer. When asked to explain something, give a high-level summary unless an in-depth explanation is specifically requested.

Match the length of written documents to what the task needs: cover the substance, but do not pad with filler sections, redundant summaries, or boilerplate.

Before your first tool call, say in one sentence what you're about to do. While working, give a brief update only when you find something important or change direction. When you finish, lead with the outcome: your first sentence should answer "what happened" or "what did you find," with supporting detail after it for readers who want it.

Deliver what was asked, at the scope intended. Make routine judgment calls yourself, and check in only when different readings of the request would lead to materially different work. If the request seems mistaken or a better approach exists, say so in a sentence and continue with the task as asked rather than quietly narrowing, widening, or transforming it. Finish the whole task, and stop short of actions that are clearly beyond what was asked.

Delegate to a subagent only for large tasks that are genuinely independent and parallelizable, such as a wide multi-file investigation. Do not delegate work you can finish yourself in a handful of tool calls, and do not use subagents to verify or double-check your own work. If one subagent can complete the task, use one rather than several, and keep spawn counts low.
