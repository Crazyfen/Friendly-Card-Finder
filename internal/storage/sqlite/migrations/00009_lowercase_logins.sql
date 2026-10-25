-- +goose Up
-- Deckbox usernames are case-insensitive, so "AlexVoron" and "alexvoron" are
-- one Deckbox User scraped twice. Logins are lowercased on the way in from now
-- on; this merges the rows that already differ only by case, then lowercases
-- the rest. lower() is ASCII-only, which is all a valid login can contain.

-- The row a Telegram Bot User claims (in that exact case) wins; among the rest,
-- the freshest Refresh. Both rows point at the same profile, and lists the
-- loser referenced alone are swept at startup (deleteUnreferencedLists).
-- +goose StatementBegin
DELETE FROM deckbox_users WHERE rowid NOT IN (
    SELECT rid FROM (
        SELECT d.rowid AS rid, ROW_NUMBER() OVER (
            PARTITION BY lower(d.deckboxLogin)
            ORDER BY EXISTS (
                         SELECT 1 FROM users u
                         WHERE u.platform = 'telegram' AND u.deckboxLogin = d.deckboxLogin
                     ) DESC,
                     d.updated_at IS NULL, d.updated_at DESC, d.deckboxLogin
        ) AS rn
        FROM deckbox_users d
    ) WHERE rn = 1
);
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE deckbox_users SET deckboxLogin = lower(deckboxLogin);
-- +goose StatementEnd

-- One claimant per (platform, login): the first claim wins, as RegisterUser's
-- plain INSERT would have decided had the case matched. A NULL created_at
-- predates the column, so it is the oldest claim.
-- +goose StatementBegin
DELETE FROM users WHERE rowid NOT IN (
    SELECT rowid FROM (
        SELECT rowid, ROW_NUMBER() OVER (
            PARTITION BY platform, lower(deckboxLogin)
            ORDER BY created_at IS NOT NULL, created_at, userId
        ) AS rn
        FROM users
    ) WHERE rn = 1
);
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE users SET deckboxLogin = lower(deckboxLogin);
-- +goose StatementEnd

-- +goose Down
-- Irreversible: the original capitalisation and the merged rows are gone.
SELECT 1;
