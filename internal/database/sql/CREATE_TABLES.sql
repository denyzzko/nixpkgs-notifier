drop table tracking;
drop table packages;
drop table accounts;
drop table users;
-- Users who track packages
CREATE TABLE IF NOT EXISTS users (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    username   TEXT,
    user_role  TEXT  NOT NULL
);

-- External identity linked to a local user (identity provider, subject -> user)
CREATE TABLE IF NOT EXISTS accounts (
  user_id        BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  provider       TEXT NOT NULL,          -- "google", "authentik", "apple", ...
  issuer         TEXT NOT NULL,          -- url like: "https://accounts.google.com"
  subject        TEXT NOT NULL,          -- OIDC subject
  email_address  TEXT,
  email_verified BOOLEAN NOT NULL DEFAULT false,
  PRIMARY KEY (issuer, subject)
);

-- Packages that users track
CREATE TABLE IF NOT EXISTS packages (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    package_name    TEXT NOT NULL UNIQUE,
    package_version TEXT NOT NULL--maybe will not be necessary
);

-- Which package is tracked by which user and what is users version of the package
CREATE TABLE IF NOT EXISTS tracking (
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id         BIGINT NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    package_id      BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    users_version   TEXT NOT NULL,
    PRIMARY KEY (user_id, package_id)
);

-- TODO:
--  INDEXES
--  TRIGGERS