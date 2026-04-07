-- +goose Up
ALTER TABLE users ADD CONSTRAINT users_username_unique UNIQUE (username);

-- +goose Down
ALTER TABLE users DROP CONSTRAINT users_username_unique;