-- Users who track packages
CREATE TABLE IF NOT EXISTS users (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    username   TEXT NOT NULL,
    role       TEXT  NOT NULL DEFAULT 'user'
);

-- External identity linked to a local user (identity provider, subject -> user)
CREATE TABLE IF NOT EXISTS accounts (
  user_id        BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  provider       TEXT NOT NULL,          -- "google", "authentik", "apple", ...
  issuer         TEXT NOT NULL,          -- url like: "https://accounts.google.com"
  subject        TEXT NOT NULL,          -- OIDC subject
  email  TEXT,
  email_verified BOOLEAN NOT NULL DEFAULT false,
  PRIMARY KEY (issuer, subject)
);

-- Nixpkgs packages that users track (unique by name + branch)
CREATE TABLE IF NOT EXISTS packages (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_checked_at TIMESTAMPTZ,
    name            TEXT NOT NULL,
    branch          TEXT NOT NULL,
    current_version TEXT NOT NULL,
    UNIQUE(name, branch)
);

-- Which package is tracked by which user
CREATE TABLE IF NOT EXISTS trackings (
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id                 BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    package_id              BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    last_notified_version   TEXT NOT NULL,
    PRIMARY KEY (user_id, package_id)
);

-- Notification channels configured by users
CREATE TABLE IF NOT EXISTS channels (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    is_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
    disabled_by_server BOOLEAN NOT NULL DEFAULT FALSE
);

-- Email notification channel (specialization of Channel)
CREATE TABLE IF NOT EXISTS emails (
    channel_id      BIGINT PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    email_address   TEXT NOT NULL,
    notify_on_manual_verify BOOLEAN NOT NULL DEFAULT FALSE
);

-- Webhook notification channel (specialization of Channel)
CREATE TABLE IF NOT EXISTS webhooks (
    channel_id  BIGINT PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    webhook_url TEXT NOT NULL,
    webhook_type TEXT NOT NULL DEFAULT 'generic' CHECK (webhook_type IN ('generic', 'mattermost')),
    notify_on_manual_verify BOOLEAN NOT NULL DEFAULT FALSE,
    username    TEXT NOT NULL DEFAULT '',
    channel     TEXT NOT NULL DEFAULT '',
    priority    TEXT NOT NULL DEFAULT '',
    request_ack BOOLEAN NOT NULL DEFAULT FALSE
);

-- Notification records tracking what notification was/will be send to users
CREATE TABLE IF NOT EXISTS notifications (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    channel_id      BIGINT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    package_id      BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    detected_at     TIMESTAMPTZ NOT NULL,
    old_version     TEXT NOT NULL,
    new_version     TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed')),
    attempt_count   INT NOT NULL DEFAULT 0,
    error_message   TEXT
);

-- Single-row table that persists admin-configured runtime settings
CREATE TABLE IF NOT EXISTS system_config (
    id                                  INT PRIMARY KEY DEFAULT 1,
    updated_at                          TIMESTAMPTZ NOT NULL DEFAULT now(),
    notification_dispatch_interval      BIGINT NOT NULL,
    notification_max_retries            INT NOT NULL,
    notification_disable_on_max_retries BOOLEAN NOT NULL,
    notification_worker_count           INT NOT NULL,
    package_check_interval              BIGINT NOT NULL,
    package_check_worker_count          INT NOT NULL,
    package_check_skip_interval         BIGINT NOT NULL,
    CONSTRAINT system_config_single_row CHECK (id = 1)
);

-- Indexes for query performance
CREATE INDEX IF NOT EXISTS idx_accounts_user_id ON accounts(user_id);
CREATE INDEX IF NOT EXISTS idx_trackings_user_id ON trackings(user_id);
CREATE INDEX IF NOT EXISTS idx_trackings_package__id ON trackings(package_id);
CREATE INDEX IF NOT EXISTS idx_channels_user_id ON channels(user_id);
CREATE INDEX IF NOT EXISTS idx_notifications_channel_id ON notifications(channel_id);
CREATE INDEX IF NOT EXISTS idx_notifications_package_id ON notifications(package_id);
CREATE INDEX IF NOT EXISTS idx_notifications_status ON notifications(status);
CREATE INDEX IF NOT EXISTS idx_packages_name_branch ON packages(name, branch);

-- Triggers for updated_at
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS packages_updated_at_trigger ON packages;
CREATE TRIGGER packages_updated_at_trigger
    BEFORE UPDATE ON packages
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at();

DROP TRIGGER IF EXISTS trackings_updated_at_trigger ON trackings;
CREATE TRIGGER trackings_updated_at_trigger
    BEFORE UPDATE ON trackings
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at();

DROP TRIGGER IF EXISTS channels_updated_at_trigger ON channels;
CREATE TRIGGER channels_updated_at_trigger
    BEFORE UPDATE ON channels
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at();

-- Trigger for default username
CREATE OR REPLACE FUNCTION set_default_username()
RETURNS trigger AS $$
BEGIN
  IF NEW.id IS NULL THEN
    NEW.id := nextval(pg_get_serial_sequence('users','id'));
  END IF;

  IF NEW.username IS NULL OR NEW.username = '' THEN
    NEW.username := 'user' || NEW.id::text;
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS users_set_default_username_trigger ON users;
CREATE TRIGGER users_set_default_username_trigger
    BEFORE INSERT ON users
    FOR EACH ROW
    EXECUTE FUNCTION set_default_username();