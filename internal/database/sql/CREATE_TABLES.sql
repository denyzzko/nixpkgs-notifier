DROP TABLE IF EXISTS notification;
DROP TABLE IF EXISTS webhook;
DROP TABLE IF EXISTS email;
DROP TABLE IF EXISTS channel;
DROP TABLE IF EXISTS tracking;
DROP TABLE IF EXISTS package;
DROP TABLE IF EXISTS account;
DROP TABLE IF EXISTS user;

-- Users who track packages
CREATE TABLE user (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    username   TEXT,
    role       TEXT  NOT NULL DEFAULT 'user'
);

-- External identity linked to a local user (identity provider, subject -> user)
CREATE TABLE account (
  user_id        BIGINT NOT NULL REFERENCES user(id) ON DELETE CASCADE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  provider       TEXT NOT NULL,          -- "google", "authentik", "apple", ...
  issuer         TEXT NOT NULL,          -- url like: "https://accounts.google.com"
  subject        TEXT NOT NULL,          -- OIDC subject
  email  TEXT,
  email_verified BOOLEAN NOT NULL DEFAULT false,
  PRIMARY KEY (issuer, subject)
);

-- Nixpkgs packages that users track (unique by name + branch)
CREATE TABLE package (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    name            TEXT NOT NULL,
    branch          TEXT NOT NULL,
    current_version TEXT NOT NULL,
    UNIQUE(name, branch)
);

-- Which package is tracked by which user
CREATE TABLE tracking (
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id                 BIGINT NOT NULL REFERENCES user(id) ON DELETE CASCADE,
    package_id              BIGINT NOT NULL REFERENCES package(id) ON DELETE CASCADE,
    last_notified_version   TEXT NOT NULL,
    PRIMARY KEY (user_id, package_id)
);

-- Notification channels configured by users
CREATE TABLE channel (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id     BIGINT NOT NULL REFERENCES user(id) ON DELETE CASCADE
    is_enabled  BOOLEAN NOT NULL DEFAULT TRUE
);

-- Email notification channel (specialization of Channel)
CREATE TABLE email (
    channel_id      BIGINT PRIMARY KEY REFERENCES channel(id) ON DELETE CASCADE,
    email_address   TEXT NOT NULL
);

-- Webhook notification channel (specialization of Channel)
CREATE TABLE webhook (
    channel_id  BIGINT PRIMARY KEY REFERENCES channel(id) ON DELETE CASCADE,
    webhook_url TEXT NOT NULL
);

-- Notification records tracking what notification was/will be send to users
CREATE TABLE notification (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    channel_id      BIGINT NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
    package_id      BIGINT NOT NULL REFERENCES package(id) ON DELETE CASCADE,
    detected_at     TIMESTAMPTZ NOT NULL,
    old_version     TEXT NOT NULL,
    new_version     TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed')),
    attempt_count   INT NOT NULL DEFAULT 0,
    error_message   TEXT
)

-- Indexes for query performance
CREATE INDEX idx_account_user_id ON account(user_id);
CREATE INDEX idx_tracking_user_id ON tracking(user_id);
CREATE INDEX idx_tracking_package__id ON tracking(package_id);
CREATE INDEX idx_channel_user_id ON channel(user_id);
CREATE INDEX idx_notification_channel_id ON notification(channel_id);
CREATE INDEX idx_notification_package_id ON notification(package_id);
CREATE INDEX idx_notification_status ON notification(status);
CREATE INDEX idx_package_name_branch ON package(name, branch);

-- Triggers for updated_at
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER package_updated_at_trigger
    BEFORE UPDATE ON package
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER tracking_updated_at_trigger
    BEFORE UPDATE ON tracking
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER channel_updated_at_trigger
    BEFORE UPDATE ON channel
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at();