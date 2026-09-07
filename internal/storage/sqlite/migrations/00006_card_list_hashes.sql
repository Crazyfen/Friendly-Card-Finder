-- +goose Up
-- The BodyHash of the export each Card List was last saved from. It previously
-- lived in a sync.Map on the Deckbox receiver, which meant every path that
-- deleted a list had to invalidate the map by hand, and every restart rewrote
-- every list once. Keyed by listId so the hash is deleted by the same statement
-- that deletes the rows it describes.
CREATE TABLE IF NOT EXISTS card_list_hashes (
    listId   INTEGER PRIMARY KEY,
    bodyHash INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS card_list_hashes;
