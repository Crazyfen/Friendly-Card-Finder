-- +goose Up
-- A Bot User belongs to one Platform (docs/adr/0011): key users by
-- (platform, userId) and let each Platform claim a Deckbox login separately.
-- +goose StatementBegin
CREATE TABLE users_new (
    platform     TEXT    NOT NULL,
    userId       INTEGER NOT NULL,
    username     TEXT,
    deckboxLogin TEXT    NOT NULL,
    created_at   INTEGER,
    PRIMARY KEY (platform, userId),
    UNIQUE (platform, deckboxLogin)
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO users_new (platform, userId, username, deckboxLogin, created_at)
SELECT 'telegram', telegramId, username, deckboxLogin, created_at
FROM users;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE users;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE users_new RENAME TO users;
-- +goose StatementEnd

-- ContactsFor looks up by login alone.
-- +goose StatementBegin
CREATE INDEX idx_users_deckboxLogin ON users (deckboxLogin);
-- +goose StatementEnd

-- +goose Down
-- Discord rows are discarded: the old shape has one claimant per login.
-- +goose StatementBegin
CREATE TABLE users_old (
    telegramId   INTEGER PRIMARY KEY,
    deckboxLogin TEXT    NOT NULL UNIQUE,
    username     TEXT,
    created_at   INTEGER
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO users_old (telegramId, deckboxLogin, username, created_at)
SELECT userId, deckboxLogin, username, created_at FROM users WHERE platform = 'telegram';
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE users;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE users_old RENAME TO users;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_users_deckboxLogin ON users (deckboxLogin);
-- +goose StatementEnd
