-- +goose Up
CREATE TABLE IF NOT EXISTS watchlist (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    branch     TEXT NOT NULL,
    UNIQUE(user_id, name, branch)
);

CREATE INDEX IF NOT EXISTS idx_watchlist_user_id     ON watchlist(user_id);
CREATE INDEX IF NOT EXISTS idx_watchlist_name_branch ON watchlist(name, branch);

-- +goose Down
DROP TABLE IF EXISTS watchlist;