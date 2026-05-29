# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## graphify

This project has a graphify knowledge graph at `graphify-out/`.

- Before answering architecture/codebase questions, read [graphify-out/GRAPH_REPORT.md](graphify-out/GRAPH_REPORT.md) for god nodes and community structure
- If [graphify-out/wiki/index.md](graphify-out/wiki/index.md) exists, navigate it instead of reading raw files
- After modifying code files, run `python3 -c "from graphify.watch import _rebuild_code; from pathlib import Path; _rebuild_code(Path('.'))"` to keep the graph current

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
main.go                      Bot setup, handler registration, context injection
internal/
  config/config.go           .env loading (MustLoad panics on missing required vars)
  deckbox/
    deckbox.go               Domain types + DeckboxSaver interface
    scrapper.go              Colly-based HTML scraper + Deckbox login/cookie auth
    handler.go               NewUser, SearchCard, RefreshStaleUserLists, SuggestDeckbox
  storage/sqlite/sqlite.go   SQLite implementation of DeckboxSaver
  i18n/i18n.go               RU/EN translations via T(lang, key)
  lib/logger/sl/sl.go        sl.Err(err) helper for slog
env/env.go                   EnvKey type + godotenv Load()
```

### Data flow

1. **Card search** (default text handler): `RefreshStaleUserLists` refreshes users older than `CARD_LIST_REFRESH_HOURS` (default 3h) via 5-worker pool → `SearchCard` → format → split/send (4096-byte Telegram limit; `splitMessage` cuts on newlines, respects UTF-8 runes)
2. **Registration** (`/deckbox <login>`): `RegisterUser` → `go GetProfileData(...)` fire-and-forget → scrape profile → save all 3 lists
3. **Batch refresh** (`/suggestdeckbox <logins>`): 3-worker pool with `shouldProcessFn` freshness pre-filter
4. **Wishlist search** (`/sell`): multi-line input, one card per line

### Storage design

- **Dual connections**: `writeDB` (1 conn, serialized) + `readDB` (`max(4, NumCPU)` conns, parallel reads)
- **WAL mode**: `PRAGMA journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000`
- **Batch inserts**: `SaveCardList` batches 1,000 cards per transaction (SQLite 32,766 param limit; 3 params/card → ~10,922 max per statement)
- **FTS5**: virtual table `card_lists_fts` with `cardName_normalized` (NFD accent-stripped) for accent-insensitive prefix search; falls back to `LIKE` if unavailable

### Scraper auth

`Scraper` resolves the Deckbox `_tcg_session` cookie lazily in this order: in-memory cache → `DECKBOX_SESSION_COOKIE` override → persisted file (`<storage dir>/deckbox_session`, `0600`) → fresh login. `doLogin` GETs `/accounts/login` (cookie jar captures the session cookie, `parseAuthenticityToken` scrapes the Rails CSRF token), then POSTs credentials (302 = success). `FetchCardList` detects a rejected cookie (response is the login page) and calls `refreshCookie` to re-login once. A `sync.Mutex` serializes logins (single-flight) so the worker pools don't all authenticate at once.

### Worker pool

`runUserRefreshWorkerPool` is the reusable coordinator; `refreshUserListsWorker` fetches profile + 3 lists per user. Callers:
- `RefreshStaleUserLists` — 5 workers, no filter
- `SuggestDeckbox` — 3 workers + `shouldProcessFn` freshness check

## Conventions

**Logging**: every function sets `const op = "package.FunctionName"` and uses `log.With(slog.String("operation", op))`. Error field via `sl.Err(err)`. Batch ops log `batch_num`/`total_batches`/`batch_card_count` on failure.

**Errors**: storage ops return `error` directly; `fmt.Errorf("%s: %w", op, err)` in init code only.

**Testing async handlers**: call worker functions directly (`GetProfileData`, `UpdateCardList`) or use mock `DeckboxSaver` + channels/waitgroups to observe side effects.

**Adding a command**:
1. Register in `main.go`: `b.RegisterHandler(bot.HandlerTypeMessageText, "/cmd", ...)`
2. Extract context: `log := ctx.Value(logKey).(*slog.Logger)`
3. Logic in `internal/deckbox/handler.go`
4. New storage needed → add to `DeckboxSaver` interface → implement in `sqlite.go` → test in `sqlite_test.go`

**Bulk storage ops**: `Begin()` transaction + ~1,000 items per batch, log `batch_num`/`total_batches` on failure.

## Configuration (.env)

| Variable | Required | Default | Description |
|---|---|---|---|
| `BOT_TOKEN` | yes | — | Telegram bot token |
| `STORAGE_PATH` | yes | — | SQLite file path |
| `DECKBOX_LOGIN` | yes* | — | Deckbox account login/email; bot logs in to obtain the session cookie |
| `DECKBOX_PASSWORD` | yes* | — | Deckbox account password |
| `DECKBOX_SESSION_COOKIE` | no | — | Optional manual `_tcg_session` override; used verbatim if set, skipping login |
| `ENV` | yes | — | `local`/`dev` = DEBUG logs; `prod` = INFO |

\* Auth requires **either** `DECKBOX_LOGIN`+`DECKBOX_PASSWORD` **or** `DECKBOX_SESSION_COOKIE`; `MustLoad` fatals if neither is present.
| `FRESHNESS_TIME_LIMIT_HOURS` | no | 24 | Hours before SuggestDeckbox treats data as stale |
| `CARD_LIST_REFRESH_HOURS` | no | 3 | Hours before auto-refresh triggers on search |
| `CARD_LIST_BATCH_SIZE` | no | 1000 | Cards per INSERT batch |

## CI/CD

GitHub Actions on push to `main`: `go test -tags fts5 -race ./...` → build+push image to `ghcr.io/crazyfen/friendlycardfinder` → SCP `docker-compose.yml` to VPS → `docker compose pull && up -d`. Secrets: `SSH_HOST`, `SSH_USER`, `SSH_PRIVATE_KEY`. VPS `.env` is maintained manually at `~/friendlycardfinder/`.
