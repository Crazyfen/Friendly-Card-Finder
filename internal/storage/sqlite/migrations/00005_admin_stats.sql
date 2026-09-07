-- +goose Up
-- Demand counters, keyed on the canonicalized Search Term rather than a matched
-- Card: a miss has no Card to attribute it to. See docs/adr/0004.
CREATE TABLE IF NOT EXISTS search_stats (
    term      TEXT    NOT NULL,
    scope     TEXT    NOT NULL,
    display   TEXT    NOT NULL,
    hits      INTEGER NOT NULL DEFAULT 0,
    misses    INTEGER NOT NULL DEFAULT 0,
    last_seen INTEGER NOT NULL,
    PRIMARY KEY (term, scope)
);

-- Daily rollup: the time series without an event log or any identifier.
CREATE TABLE IF NOT EXISTS daily_stats (
    day      TEXT PRIMARY KEY,
    searches INTEGER NOT NULL DEFAULT 0,
    misses   INTEGER NOT NULL DEFAULT 0
);

-- Registrations per day come from the registration itself rather than a counter:
-- exact, restart-proof, and one fewer write path. Pre-existing rows stay NULL.
ALTER TABLE users ADD COLUMN created_at INTEGER;

-- card_lists is replace-in-place, so per-list size over time has to be sampled.
CREATE TABLE IF NOT EXISTS list_snapshots (
    listId    INTEGER NOT NULL,
    day       TEXT    NOT NULL,
    cardCount INTEGER NOT NULL,
    PRIMARY KEY (listId, day)
);

-- Refresh outcome, which until now only ever reached stdout.
ALTER TABLE deckbox_users ADD COLUMN last_error TEXT;
ALTER TABLE deckbox_users ADD COLUMN last_card_count INTEGER;

-- +goose Down
DROP TABLE IF EXISTS search_stats;
DROP TABLE IF EXISTS daily_stats;
DROP TABLE IF EXISTS list_snapshots;
ALTER TABLE deckbox_users DROP COLUMN last_error;
ALTER TABLE deckbox_users DROP COLUMN last_card_count;
ALTER TABLE users DROP COLUMN created_at;
