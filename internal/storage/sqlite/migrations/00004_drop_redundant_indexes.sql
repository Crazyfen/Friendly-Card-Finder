-- +goose Up
-- Drop indexes that are fully covered by another index, so SaveCardList no longer
-- pays to maintain them on every clear+insert:
--   * idx_card_lists_listId          — leftmost prefix of idx_card_lists_unique_list_card (listId, cardName)
--   * idx_card_lists_listId_cardName  — identical columns to the UNIQUE idx_card_lists_unique_list_card
--   * idx_users_deckboxLogin / idx_users_username — duplicate the auto-indexes SQLite
--     creates for the UNIQUE constraints on those columns.
-- idx_card_lists_cardName is kept: it serves the LIKE fallback and the FTS join on cardName.
DROP INDEX IF EXISTS idx_card_lists_listId;
DROP INDEX IF EXISTS idx_card_lists_listId_cardName;
DROP INDEX IF EXISTS idx_users_deckboxLogin;
DROP INDEX IF EXISTS idx_users_username;

-- +goose Down
CREATE INDEX IF NOT EXISTS idx_card_lists_listId          ON card_lists (listId);
CREATE INDEX IF NOT EXISTS idx_card_lists_listId_cardName ON card_lists (listId, cardName);
CREATE INDEX IF NOT EXISTS idx_users_deckboxLogin ON users (deckboxLogin);
CREATE INDEX IF NOT EXISTS idx_users_username     ON users (username);