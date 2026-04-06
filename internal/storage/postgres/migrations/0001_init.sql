-- 0001_init: initial schema for ircat persistent state on Postgres.
--
-- Schema parity with the sqlite driver. Differences:
--   - TIMESTAMP WITH TIME ZONE everywhere (sqlite uses naive
--     TIMESTAMP because it has no native time type).
--   - BOOLEAN instead of INTEGER for the enabled column on bots.
--   - BIGINT for the nanosecond tick interval (sqlite uses INTEGER
--     which has the same range under the hood).
--
-- The schema_migrations table is created by the migration runner
-- before this file ever runs.

CREATE TABLE operators (
    name           TEXT PRIMARY KEY,
    host_mask      TEXT NOT NULL DEFAULT '',
    password_hash  TEXT NOT NULL,
    flags          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE api_tokens (
    id            TEXT PRIMARY KEY,
    label         TEXT NOT NULL DEFAULT '',
    hash          TEXT NOT NULL UNIQUE,
    scopes        TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    last_used_at  TIMESTAMP WITH TIME ZONE
);

CREATE TABLE bots (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    source          TEXT NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT FALSE,
    tick_interval_ns BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE bot_kv (
    bot_id  TEXT NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    key     TEXT NOT NULL,
    value   TEXT NOT NULL,
    PRIMARY KEY (bot_id, key)
);

CREATE TABLE channels (
    name            TEXT PRIMARY KEY,
    topic           TEXT NOT NULL DEFAULT '',
    topic_set_by    TEXT NOT NULL DEFAULT '',
    topic_set_at    TIMESTAMP WITH TIME ZONE,
    mode_word       TEXT NOT NULL DEFAULT '+nt',
    channel_key     TEXT NOT NULL DEFAULT '',
    user_limit      INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE channel_bans (
    channel_name TEXT NOT NULL REFERENCES channels(name) ON DELETE CASCADE,
    mask         TEXT NOT NULL,
    set_by       TEXT NOT NULL DEFAULT '',
    set_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_name, mask)
);

CREATE TABLE audit_events (
    id         TEXT PRIMARY KEY,
    timestamp  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    type       TEXT NOT NULL,
    actor      TEXT NOT NULL DEFAULT '',
    target     TEXT NOT NULL DEFAULT '',
    data_json  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX audit_events_timestamp ON audit_events(timestamp);
CREATE INDEX audit_events_type ON audit_events(type);
