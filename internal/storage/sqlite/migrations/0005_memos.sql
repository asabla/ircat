CREATE TABLE memos (
    id            TEXT PRIMARY KEY,
    sender_id     TEXT NOT NULL REFERENCES accounts(id),
    recipient_id  TEXT NOT NULL REFERENCES accounts(id),
    body          TEXT NOT NULL,
    read          INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMP NOT NULL
);

CREATE INDEX idx_memos_recipient ON memos (recipient_id, read);
