-- +goose Up
-- Remove any duplicates that may exist before adding the unique constraint
-- (keeps the row with the lowest rowid for each listId+cardName pair).
DELETE FROM card_lists
WHERE rowid NOT IN (
    SELECT MIN(rowid)
    FROM card_lists
    GROUP BY listId, cardName
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_card_lists_unique_list_card ON card_lists (listId, cardName);

-- +goose Down
DROP INDEX IF EXISTS idx_card_lists_unique_list_card;
