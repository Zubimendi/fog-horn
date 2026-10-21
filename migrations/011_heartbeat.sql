-- 011_heartbeat.sql
CREATE TABLE heartbeat_tokens (
    target_id            UUID PRIMARY KEY REFERENCES targets(id) ON DELETE CASCADE,
    token_hash           TEXT NOT NULL UNIQUE,  -- SHA-256 of raw token; raw never stored
    previous_token_hash  TEXT,
    previous_expires_at  TIMESTAMPTZ,
    rotated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
