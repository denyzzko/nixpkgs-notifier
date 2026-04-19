-- +goose Up
ALTER TABLE system_config
    ADD COLUMN max_emails_per_user INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE system_config
    DROP COLUMN max_emails_per_user;