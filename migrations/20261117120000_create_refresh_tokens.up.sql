-- One row per refresh token. Rotation creates a new row each time; all rows of one
-- login session share family_id, so reuse detection can revoke the whole session.
-- Only the SHA-256 hash of the token is stored, never the token itself.
-- TIMESTAMPTZ (not TIMESTAMP) so expiry never depends on a time zone. Expiry is
-- always computed and compared with the database clock (NOW()).
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id          TEXT PRIMARY KEY,                                   -- uuid of this token row
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id   TEXT NOT NULL,                                      -- shared by all tokens of one login
    token_hash  TEXT NOT NULL UNIQUE,                               -- SHA-256 hex of the raw token
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,                                        -- set when the token is rotated
    revoked_at  TIMESTAMPTZ                                         -- set by logout or reuse detection
);

-- Revoking a family looks rows up by family_id.
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_family ON refresh_tokens (family_id);
-- Per-user cleanup of expired rows looks up by (user_id, expires_at).
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_expires ON refresh_tokens (user_id, expires_at);
