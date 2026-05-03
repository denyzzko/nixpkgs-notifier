-- +goose Up

-- Drop old watchlist (name+branch directly, no FK to packages).
DROP TABLE IF EXISTS watchlist;

-- New watchlist: watched packages now live in packages table with current_version=''.
CREATE TABLE watchlist (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id    BIGINT NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    package_id BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    UNIQUE(user_id, package_id)
);
CREATE INDEX IF NOT EXISTS idx_watchlist_user_id    ON watchlist(user_id);
CREATE INDEX IF NOT EXISTS idx_watchlist_package_id ON watchlist(package_id);

-- Unified check state for both tracked and watched packages.
-- old_version is NULL for watched packages (no version yet).
CREATE TABLE check_state (
    user_id     BIGINT NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    package_id  BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    status      TEXT NOT NULL CHECK (status IN ('pending','done','failed','not_found')),
    old_version TEXT,
    new_version TEXT,
    error_msg   TEXT,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL DEFAULT now() + interval '1 hour',
    PRIMARY KEY (user_id, package_id)
);
CREATE INDEX IF NOT EXISTS idx_check_state_user_id    ON check_state(user_id);
CREATE INDEX IF NOT EXISTS idx_check_state_expires_at ON check_state(expires_at);

-- +goose Down
DROP TABLE IF EXISTS check_state;
DROP TABLE IF EXISTS watchlist;