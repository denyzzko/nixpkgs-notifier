-- +goose Up
ALTER TABLE system_config
    ADD COLUMN notification_retention_days INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE system_config
    DROP COLUMN notification_retention_days;