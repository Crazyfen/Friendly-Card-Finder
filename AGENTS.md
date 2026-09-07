# Agent Instructions for FriendlyCardFinder

This file provides essential guidance for AI agents (Cline, Claude Code, etc.) when working with code in this repository.

Telegram bot for searching Magic: The Gathering cards across Deckbox.org user collections. Go + SQLite (WAL mode, FTS5).

## Read CLAUDE.md

**[`CLAUDE.md`](CLAUDE.md) is the complete and authoritative guide for this repository.** Read it before answering questions or modifying code. It covers:

- Build, run, and test commands (including the `fts5` build tag)
- Architecture and data flow
- Storage design — dual connections, WAL, batch inserts, FTS5, prepared statements
- Scraper auth and the worker pool
- Logging, error handling, and testing conventions
- The full `.env` configuration table
- The graphify knowledge graph rules
- CI/CD

Do not duplicate that content here. This file exists only so agents that look for `AGENTS.md` find their way to `CLAUDE.md`; earlier versions carried a partial copy that drifted out of sync with it.

[`README.md`](README.md) is written for humans — project overview, bot commands, setup, and Docker. Read it when you need user-facing behavior or setup steps, not as a source of development conventions.
