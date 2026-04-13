-- +goose Up
CREATE TABLE IF NOT EXISTS users (
    telegramId   INTEGER PRIMARY KEY,
    deckboxLogin TEXT    NOT NULL UNIQUE,
    username     TEXT    NOT NULL UNIQUE
);

CREATE INDEX IF NOT EXISTS idx_users_deckboxLogin ON users (deckboxLogin);
CREATE INDEX IF NOT EXISTS idx_users_username     ON users (username);

CREATE TABLE IF NOT EXISTS deckbox_users (
    deckboxLogin TEXT    PRIMARY KEY,
    inventoryId  INTEGER,
    tradelistId  INTEGER,
    wishlistId   INTEGER,
    updated_at   INTEGER
);

CREATE INDEX IF NOT EXISTS idx_deckbox_users_inventoryId ON deckbox_users (inventoryId);
CREATE INDEX IF NOT EXISTS idx_deckbox_users_tradelistId ON deckbox_users (tradelistId);
CREATE INDEX IF NOT EXISTS idx_deckbox_users_wishlistId  ON deckbox_users (wishlistId);

CREATE TABLE IF NOT EXISTS card_lists (
    listId   INTEGER NOT NULL,
    cardName TEXT    NOT NULL,
    quantity INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_card_lists_listId          ON card_lists (listId);
CREATE INDEX IF NOT EXISTS idx_card_lists_cardName        ON card_lists (cardName);
CREATE INDEX IF NOT EXISTS idx_card_lists_listId_cardName ON card_lists (listId, cardName);

-- +goose Down
DROP TABLE IF EXISTS card_lists;
DROP TABLE IF EXISTS deckbox_users;
DROP TABLE IF EXISTS users;
