-- 0002_channel_excepts_invexes.sql
--
-- Persists the +e ban-exception and +I invite-exception lists from
-- RFC 2811 §4.3.2-§4.3.3. The two tables mirror channel_bans
-- (channel_name, mask, set_by, set_at) so the upsert path can wipe
-- and replace per-channel entries inside the same transaction.

CREATE TABLE channel_exceptions (
    channel_name TEXT NOT NULL,
    mask         TEXT NOT NULL,
    set_by       TEXT NOT NULL DEFAULT '',
    set_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (channel_name, mask),
    FOREIGN KEY (channel_name) REFERENCES channels(name) ON DELETE CASCADE
);

CREATE TABLE channel_invexes (
    channel_name TEXT NOT NULL,
    mask         TEXT NOT NULL,
    set_by       TEXT NOT NULL DEFAULT '',
    set_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (channel_name, mask),
    FOREIGN KEY (channel_name) REFERENCES channels(name) ON DELETE CASCADE
);
