CREATE TABLE registered_channels (
    channel    TEXT PRIMARY KEY,
    founder_id TEXT NOT NULL REFERENCES accounts(id),
    guard      INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE channel_access (
    channel    TEXT NOT NULL REFERENCES registered_channels(channel) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    flags      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (channel, account_id)
);
