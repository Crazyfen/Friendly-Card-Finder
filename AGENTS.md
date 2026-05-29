# Agent Instructions for FriendlyCardFinder

This file provides essential guidance for AI agents (Cline, Claude Code, etc.) when working with code in this repository.

## Priority: Read Documentation First

## graphify

This project has a graphify knowledge graph at `graphify-out/`.

- Before answering architecture/codebase questions, read [graphify-out/GRAPH_REPORT.md](graphify-out/GRAPH_REPORT.md) for god nodes and community structure
- If [graphify-out/wiki/index.md](graphify-out/wiki/index.md) exists, navigate it instead of reading raw files
- After modifying code files, run `python3 -c "from graphify.watch import _rebuild_code; from pathlib import Path; _rebuild_code(Path('.'))"` to keep the graph current

**Before answering any questions or modifying code, you MUST read the following files first:**

1. **`CLAUDE.md`** - Detailed development guidelines, conventions, and data flow
2. **`README.md`** - Project overview, commands, and architecture

These files contain critical context about:

- Project purpose and domain (Telegram bot for MTG card search)
- Architecture patterns and data flow
- Storage design (SQLite with WAL mode, batch inserts, FTS5)
- Logging/error handling conventions
- Worker pool implementations
- Configuration requirements

## Repository Overview

**Type:** Go + SQLite Telegram Bot  
**Purpose:** Magic: The Gathering card search across Deckbox.org user collections  
**Database:** SQLite with WAL mode and FTS5 for full-text search

## Commands

```bash
go build ./...
go build -tags "fts5" ./...    # with FTS5 support
go run main.go
go run -tags "fts5" main.go
go test ./...
go test -v ./internal/storage/sqlite/
go test -race ./...
```

## Architecture

- `main.go` — Bot setup, handler registration, context injection
- `internal/config/` — Environment loading (MustLoad panics on missing required vars)
- `internal/deckbox/` — Domain types, scraper (Deckbox login/cookie auth), handlers for user operations
- `internal/storage/sqlite/` — SQLite implementation of DeckboxSaver interface
- `internal/i18n/` — RU/EN translations

## Critical Conventions

**Logging:** Every function must set `const op = "package.FunctionName"` and use `log.With(slog.String("operation", op))`. Errors via `sl.Err(err)`.

**Errors:** Storage ops return `error` directly; `fmt.Errorf("%s: %w", op, err)` in init code only.

**Worker pools:** Use `runUserRefreshWorkerPool` pattern from existing implementations.

**Adding commands:** Register in `main.go`, logic in `internal/deckbox/handler.go`, new storage → update `DeckboxSaver` interface + `sqlite.go` + `sqlite_test.go`.

## Configuration (.env)

| Variable                 | Required | Default | Description                                                       |
| ------------------------ | -------- | ------- | ----------------------------------------------------------------- |
| `BOT_TOKEN`              | yes      | —       | Telegram bot token                                                |
| `STORAGE_PATH`           | yes      | —       | SQLite file path                                                  |
| `DECKBOX_LOGIN`          | yes\*    | —       | Deckbox login/email; bot logs in to obtain the session cookie     |
| `DECKBOX_PASSWORD`       | yes\*    | —       | Deckbox account password                                          |
| `DECKBOX_SESSION_COOKIE` | no       | —       | Optional manual `_tcg_session` override; used verbatim if set     |
| `ENV`                    | yes      | —       | `local`/`dev` = DEBUG, `prod` = INFO                              |

\* Auth requires **either** `DECKBOX_LOGIN`+`DECKBOX_PASSWORD` **or** `DECKBOX_SESSION_COOKIE`; `MustLoad` fatals if neither is set. The obtained cookie is persisted to `<storage dir>/deckbox_session` and reused across restarts.

## CI/CD

GitHub Actions on push to `main`: test → build/push Docker image → deploy to VPS via SSH.
