-- 0003_channel_quiets.sql
--
-- Persists the +q quiet list (charybdis convention). Mirrors the
-- ban / exception / invex tables.

CREATE TABLE channel_quiets (
    channel_name TEXT NOT NULL REFERENCES channels(name) ON DELETE CASCADE,
    mask         TEXT NOT NULL,
    set_by       TEXT NOT NULL DEFAULT '',
    set_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_name, mask)
);
