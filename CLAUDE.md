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
main.go                      Bot setup, handler registration, Telegram SDK adapter
internal/
  admin/                     Admin Panel: net/http + embedded html/template (manage + insights)
  config/config.go           Load() validates and returns; MustLoad fatals on the error
  deckbox/
    deckbox.go               Domain types + Scope/Query/SearchResult + DeckboxSaver/AdminStore
    scrapper.go              net/http transport (host is a field), Deckbox login, page parsing
    session.go               Cookie resolution, single-flight login, persistence
    handler.go               Deckbox receiver: Register, Search, RefreshStale, Suggest
  storage/sqlite/sqlite.go   SQLite implementation of DeckboxSaver + AdminStore
  storage/sqlite/admin.go    Purge/Unlink, Demand counters, panel queries
  telegram/command.go        Request/Reply + Commands: every command's behaviour
  telegram/render.go         Search results → Telegram HTML
  i18n/i18n.go               RU/EN translations via T(lang, key)
  lib/logger/sl/sl.go        sl.Err(err) helper for slog
env/env.go                   EnvKey type + godotenv Load()
```

`internal/deckbox` deals only in domain values — it imports neither `i18n` nor the Telegram SDK. All wording and markup live in `internal/telegram`, and the Telegram SDK itself appears only in `main.go`, which maps `models.Update` → `telegram.Request` and `telegram.Reply` → `SendMessage`. Every command is therefore reachable from a test as a plain function.

Storage is split across two interfaces, both satisfied by the same `*sqlite.SQLiteStorage`: `DeckboxSaver` for search and Refresh, `AdminStore` for the Admin Panel. A new panel query widens only `AdminStore`, so it never reaches the fakes in the search and Refresh tests.

`Scope` is a named string type. Both the storage column and the Telegram wording are picked by a switch that falls back to the tradelist, so an unlisted value would otherwise search and read as a tradelist search with no error anywhere.

### Data flow

1. **Card search** (default text handler): `App.handle` → `telegram.NewRequest` → `Commands.Search` → `App.send`. A background ticker runs `RefreshStale` (users older than `CARD_LIST_REFRESH_HOURS`, default 3h) so searches never block on a refresh → `Deckbox.Search` → `telegram.RenderSearch` → split/send (4096-byte Telegram limit; `tgutil.SplitMessage` cuts on newlines, respects UTF-8 runes). A query wrapped in quotes (`"Shock"`; also `''`/`«»`/`“”`/`‘’` and low-9 `„“`/`„”` — see `exactQuotePairs`) is an exact-name match: `ParseQuery` strips the quotes and sets `Query.Exact`. One search handles 1..n names: `Search` parses each line once up front, so `SearchQueries`/`NotFound` echo the stripped names, and runs the per-card queries concurrently (8 max) over the read pool, merging deterministically. `RenderSearch` picks the single-card layout (per-owner card lines, Deckbox link pre-filtered to the query) when one name was searched and the ranked multi-card layout otherwise
2. **Registration** (`/deckbox <login>`): `Register` → `RegisterUser` → background refresh through the **same** worker pool every other caller uses (1 worker, 2-minute timeout), so registration also stamps `updated_at`
3. **Batch refresh** (`/suggestdeckbox <logins>`): 5-worker pool with a freshness pre-filter. The one command that answers twice — `Commands.Suggest` returns the acknowledgement plus a function producing the summary, and `App.suggestHandler` decides where that runs (a goroutine in production, inline in tests)
4. **Wishlist search** (`/sell`): identical to card search with `ScopeWishlist`
5. **Admin Panel** (`/` and `/insights` over HTTP): `internal/admin` → `Deckbox` (`Purge`, `Unlink`, `RefreshNow`, `AdminOverview`/`AdminUsers`/`AdminInsights`) → `AdminStore`. It never touches storage directly, so panel actions and bot commands share one set of domain operations

### Admin Panel

Runs in the bot process, listens on `ADMIN_ADDR` (default `:8081`), and publishes **no** port: a Caddy sidecar terminates TLS for `admin.ivash.net` and proxies to it over the compose network, so there is no route that bypasses TLS and auth. HTTP Basic Auth (`ADMIN_USER`/`ADMIN_PASSWORD`, `subtle.ConstantTimeCompare`, one-second sleep on failure); the panel refuses to start when `ADMIN_PASSWORD` is empty. Mutating routes are POST-only and rejected unless `Sec-Fetch-Site` is `same-origin` (Basic Auth makes CSRF worse, not better — the browser re-sends credentials automatically). Purge is confirmed by a `GET /purge?login=` page rather than a JS dialog, which keeps the CSP at `default-src 'none'`. Analytics are memoized for 5 minutes because three of their queries scan `card_lists`. See `docs/adr/0003`.

### Demand

`search_stats(term, scope, display, hits, misses, last_seen)` counts searches per canonicalized Search Term — not per matched card, since a miss matched none. `daily_stats(day, searches, misses)` is the time series; registrations per day come from `users.created_at`. `list_snapshots(listId, day, cardCount)` samples list sizes, which `card_lists` cannot show because it is replace-in-place. `deckbox_users.last_error`/`last_card_count` record refresh outcomes that previously only reached stdout. Writes are fire-and-forget from `Search` so a counter never queues behind a refresh on the single write connection. See `docs/adr/0004`.

### Storage design

- **Dual connections**: `writeDB` (1 conn, serialized) + `readDB` (`max(4, NumCPU)` conns, parallel reads)
- **WAL mode**: `PRAGMA journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000`
- **Batch inserts**: `batchWriter` is the single place the batching policy lives — row clamp (`maxBoundParams / len(cols)`, SQLite's 32,766 bound-parameter limit), reuse of the prepared full-size statement, and failure logging. `SaveCardList` streams the card map into one writer per table inside a single transaction; `repopulateFts` uses the same writer
- **Grouped results**: `SearchCard` takes a `deckbox.Query` and returns `[]deckbox.CardListWithOwner` — rows are grouped into Card Lists here (they arrive ordered by `listId`), so neither the row layout nor the ordering contract leaks to callers
- **FTS5**: virtual table `card_lists_fts(listId, cardName UNINDEXED, cardName_normalized, quantity UNINDEXED)` — `listId` is indexed so list replaces delete via `MATCH 'listId:<id>'` (no full-table scan); `cardName_normalized` (NFD accent-stripped) gives accent-insensitive prefix search with explicit `cardName_normalized:` column filters; `quantity` is stored so searches never join back to `card_lists`; falls back to `LIKE` if unavailable. `ensureFtsTable` auto-rebuilds the table from `card_lists` whenever the column layout (`ftsColumnSpec`) is outdated. Exact search (`exact` flag) canonicalizes the query once (`canonicalCardName`), narrows candidates with an FTS phrase query (`ftsPhraseQuery`) and post-filters rows by canonical equality — exactness ignores case, accents, and punctuation. The non-FTS fallback gets identical semantics via the `canonical_name()` SQL function registered in the driver's ConnectHook. A query with no tokens (all punctuation, e.g. quoted `"//"`) degrades to a regular non-exact search. `queryTokens` mirrors the default unicode61 tokenizer (letters + all number categories); any tokenize option added to `ftsCreateSQL` must be reflected there
- **Prepared statement cache**: hot read/write queries are prepared once via `readStmt`/`writeStmt` and cached for the storage lifetime
- **mmap**: custom `sqlite3_mmap` driver sets a 256 MB `mmap_size` per connection (no DSN param exists for it)
- **Unchanged-list skip**: the scraper hashes the raw export body (FNV-64a, `CardList.BodyHash`) and `SaveCardList` skips the delete+insert when it matches the hash stored for that list in `card_list_hashes`. The hash is read and written inside the same transaction as the rows it describes, so any statement that deletes a list deletes its hash too and no caller has anything to invalidate. It also survives restarts. A `BodyHash` of 0 (unknown) never skips and clears any stored hash. See `docs/adr/0005`

### Scraper auth

All profile/export fetches go through one shared `http.Transport` (keep-alive, 16 idle conns/host) via `fetchPage`, so worker pools reuse TLS connections instead of handshaking per request. `doLogin` GETs `/accounts/login` (cookie jar captures the session cookie, `parseAuthenticityToken` scrapes the Rails CSRF token), then POSTs credentials (302 = success); it is also where the "no credentials configured" check lives.

`Scraper.baseURL` holds the host — `DeckboxBaseURL` in production, an `httptest.Server` in `scrapper_http_test.go`, which is what gives the retry, the redirect-to-login detection and the re-login handshake a test surface. `fetchPage` makes three attempts with 200ms doubling backoff and does not sleep before giving up.

`session` (`session.go`) owns everything about *which* cookie to use and *when* to get a new one, and knows nothing about HTTP — the login arrives as a `func(ctx) (string, error)`, so `session_test.go` drives it with a fake that counts calls. `Cookie` resolves lazily in this order: in-memory cache → `DECKBOX_SESSION_COOKIE` override → persisted file (`<storage dir>/deckbox_session`, `0600`) → fresh login. `FetchCardList` detects a rejected cookie (response is the login page) and calls `session.Refresh` to re-login once. A `sync.Mutex` makes the login single-flight, and `Refresh` returns a cookie another goroutine already fetched rather than logging in again — the worker pools all hit an expired cookie within the same second.

### Configuration

`config.Load` reads the environment, validates it and returns `(*Config, error)`; `MustLoad` is the three-line wrapper `main` calls, and the only place a bad configuration ends the process. Splitting them is what makes the boot rules testable with `t.Setenv` (`config_test.go`). A missing `.env` is **not** an error — the VPS and CI supply the environment directly — but a missing `BOT_TOKEN`, `STORAGE_PATH` or Deckbox auth is. Every other setting reaches its module as an argument; nothing below `main.go` reads the environment.

### Page parsing

`parseExport` and `parseProfile` take bytes and return domain values, so the Deckbox page formats are described by `parse_test.go` (fixtures, no network) rather than by the credential-gated `scrapper_test.go`. Everything above them needs the network; everything below needs only a byte slice. Quantities for a repeated card name are summed because the export carries no variation data — see `docs/adr/0008` and the **Quantity** entry in `CONTEXT.md`, and do not "fix" it into a de-duplication.

### Worker pool

`Deckbox.refreshUsers` is the reusable coordinator; `Deckbox.refreshUser` fetches profile + 3 lists for one user and **returns** a `RefreshOutcome` without writing anything. `refreshUsers` is the only place an outcome is judged and stored: reaching and saving the profile (`ProfileSaved`) is what stamps `updated_at`, so an empty collection or a Card List that failed to fetch is recorded in `Err` but still counts as read — see `docs/adr/0006`. Every caller goes through this pool — there is no second refresh path:

- `RefreshStale` — 10 workers, no filter
- `Suggest` — 5 workers + freshness pre-filter
- `Register` — 1 worker, in the background with a 2-minute timeout

## Conventions

**Logging**: every function sets `const op = "package.FunctionName"` and uses `log.With(slog.String("operation", op))`. Error field via `sl.Err(err)`. `batchWriter` logs `table`/`batch_num`/`batch_row_count` on failure.

**Errors**: storage ops return `error` directly; `fmt.Errorf("%s: %w", op, err)` in init code only.

**Testing async handlers**: build a `Deckbox` with `New` over a fake `DeckboxSaver` + fake `AdminStore` + fake `Fetcher` (see `handler_test.go`), then call the method directly. Commands are tested against a fake `telegram.Domain` with no bot, database or scraper (`command_test.go`). Background work (registration's refresh) is observed with a channel the fake closes.

**Adding a command**:

1. Method on `telegram.Commands` with the signature `func(context.Context, Request) []Reply` — argument in `req.Text` (already stripped of the command prefix), wording via `i18n.T(req.Lang, ...)`. Test it in `command_test.go`
2. Add whatever it needs to `telegram.Domain`
3. Register in `main.go`: `b.RegisterHandler(bot.HandlerTypeMessageText, "cmd", bot.MatchTypeCommandStartOnly, app.handle("cmd", app.cmd.Yours))` — no new plumbing, `handle`/`send` already cover splitting, HTML and errors
4. Domain logic as a method on `Deckbox` in `internal/deckbox/handler.go` — it returns values, never formatted text
5. New storage needed → add to `DeckboxSaver` (or `AdminStore`, if only the panel uses it) → implement in `sqlite.go` → test in `sqlite_test.go`

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
| `CARD_LIST_BATCH_SIZE`   | no       | 1000    | Cards per INSERT batch; passed to `sqlite.New`, which never reads the env     |
| `ADMIN_ADDR`             | no       | `:8081` | Admin Panel listen address inside the container (never published to the host) |
| `ADMIN_USER`             | no       | `admin` | Admin Panel Basic Auth user                                                   |
| `ADMIN_PASSWORD`         | no       | —       | Admin Panel Basic Auth password; **empty disables the panel**                 |
| `ADMIN_DOMAIN`           | no       | `admin.ivash.net` | Read by the Caddyfile, not the bot; the panel's public hostname     |

\* Auth requires **either** `DECKBOX_LOGIN`+`DECKBOX_PASSWORD` **or** `DECKBOX_SESSION_COOKIE`. See **Configuration** above for what `Load` rejects.

## CI/CD

GitHub Actions on push to `main`: `go test -tags fts5 -race ./...` → build+push image to `ghcr.io/crazyfen/friendlycardfinder` → SCP `docker-compose.yml` + `Caddyfile` to VPS → `docker compose pull && up -d`. Secrets: `SSH_HOST`, `SSH_USER`, `SSH_PRIVATE_KEY`. VPS `.env` is maintained manually at `~/friendlycardfinder/`.

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
