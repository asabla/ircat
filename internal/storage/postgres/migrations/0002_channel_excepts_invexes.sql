-- 0002_channel_excepts_invexes.sql
--
-- Persists the +e ban-exception and +I invite-exception lists from
-- RFC 2811 §4.3.2-§4.3.3. Mirrors channel_bans so the upsert path
-- can wipe and replace per-channel entries inside one transaction.

CREATE TABLE channel_exceptions (
    channel_name TEXT NOT NULL REFERENCES channels(name) ON DELETE CASCADE,
    mask         TEXT NOT NULL,
    set_by       TEXT NOT NULL DEFAULT '',
    set_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_name, mask)
);

CREATE TABLE channel_invexes (
    channel_name TEXT NOT NULL REFERENCES channels(name) ON DELETE CASCADE,
    mask         TEXT NOT NULL,
    set_by       TEXT NOT NULL DEFAULT '',
    set_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_name, mask)
);
