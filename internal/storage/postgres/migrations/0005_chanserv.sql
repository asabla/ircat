CREATE TABLE registered_channels (
    channel    TEXT PRIMARY KEY,
    founder_id TEXT NOT NULL REFERENCES accounts(id),
    guard      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE channel_access (
    channel    TEXT NOT NULL REFERENCES registered_channels(channel) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    flags      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (channel, account_id)
);
