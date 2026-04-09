-- 0003_channel_quiets.sql
--
-- Persists the +q quiet list (charybdis convention). Mirrors the
-- channel_bans / channel_exceptions / channel_invexes tables so the
-- upsert path can wipe and replace per-channel entries inside one
-- transaction.

CREATE TABLE channel_quiets (
    channel_name TEXT NOT NULL,
    mask         TEXT NOT NULL,
    set_by       TEXT NOT NULL DEFAULT '',
    set_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (channel_name, mask),
    FOREIGN KEY (channel_name) REFERENCES channels(name) ON DELETE CASCADE
);
