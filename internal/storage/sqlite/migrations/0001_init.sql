-- 0001_init: initial schema for ircat persistent state.
--
-- Five concerns, five tables. The schema_migrations table is created
-- by the migration runner before this file ever runs.

CREATE TABLE operators (
    name           TEXT PRIMARY KEY,
    host_mask      TEXT NOT NULL DEFAULT '',
    password_hash  TEXT NOT NULL,
    flags          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE api_tokens (
    id            TEXT PRIMARY KEY,
    label         TEXT NOT NULL DEFAULT '',
    hash          TEXT NOT NULL UNIQUE,
    scopes        TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at  TIMESTAMP
);

CREATE TABLE bots (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    source          TEXT NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 0,
    tick_interval_ns INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE bot_kv (
    bot_id  TEXT NOT NULL,
    key     TEXT NOT NULL,
    value   TEXT NOT NULL,
    PRIMARY KEY (bot_id, key),
    FOREIGN KEY (bot_id) REFERENCES bots(id) ON DELETE CASCADE
);

CREATE TABLE channels (
    name            TEXT PRIMARY KEY,
    topic           TEXT NOT NULL DEFAULT '',
    topic_set_by    TEXT NOT NULL DEFAULT '',
    topic_set_at    TIMESTAMP,
    mode_word       TEXT NOT NULL DEFAULT '+nt',
    channel_key     TEXT NOT NULL DEFAULT '',
    user_limit      INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE channel_bans (
    channel_name TEXT NOT NULL,
    mask         TEXT NOT NULL,
    set_by       TEXT NOT NULL DEFAULT '',
    set_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (channel_name, mask),
    FOREIGN KEY (channel_name) REFERENCES channels(name) ON DELETE CASCADE
);

CREATE TABLE audit_events (
    id         TEXT PRIMARY KEY,
    timestamp  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    type       TEXT NOT NULL,
    actor      TEXT NOT NULL DEFAULT '',
    target     TEXT NOT NULL DEFAULT '',
    data_json  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX audit_events_timestamp ON audit_events(timestamp);
CREATE INDEX audit_events_type ON audit_events(type);
