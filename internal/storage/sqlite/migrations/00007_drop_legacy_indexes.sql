-- +goose Up
-- Databases created before goose carry the same indexes under their original,
-- pre-goose names, which 00004 (dropping the goose-named duplicates) never
-- reached. Every card row insert paid for all of them.
DROP INDEX IF EXISTS idx_listId;           -- leftmost prefix of idx_card_lists_unique_list_card
DROP INDEX IF EXISTS idx_listIdToCardName; -- same columns as idx_card_lists_unique_list_card
DROP INDEX IF EXISTS idx_cardName;         -- same column as idx_card_lists_cardName
DROP INDEX IF EXISTS idx_inventoryId;      -- same column as idx_deckbox_users_inventoryId
DROP INDEX IF EXISTS idx_tradelistId;      -- same column as idx_deckbox_users_tradelistId
DROP INDEX IF EXISTS idx_wishlistId;       -- same column as idx_deckbox_users_wishlistId

-- +goose Down
-- Nothing to restore: these were never part of the goose schema.
