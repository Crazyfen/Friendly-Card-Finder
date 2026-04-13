-- +goose Up
-- +goose StatementBegin
-- Telegram usernames are optional: remove NOT NULL and UNIQUE from username.
-- Empty strings from the old schema are converted to NULL.
CREATE TABLE users_new (
    telegramId   INTEGER PRIMARY KEY,
    deckboxLogin TEXT    NOT NULL UNIQUE,
    username     TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO users_new
SELECT telegramId, deckboxLogin, NULLIF(username, '')
FROM users;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE users;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE users_new RENAME TO users;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_users_deckboxLogin ON users (deckboxLogin);
-- +goose StatementEnd

-- +goose Down
-- Not safely reversible: NULLs can't go back into NOT NULL UNIQUE.
