drop table tracking;
drop table packages;
drop table users;
-- Users who track packages
CREATE TABLE IF NOT EXISTS users (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    username      VARCHAR(64)  NOT NULL,
    email_address VARCHAR(320) NOT NULL UNIQUE,
    user_role     VARCHAR(16)
);

-- Packages that users track
CREATE TABLE IF NOT EXISTS packages (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    package_name    VARCHAR(64) NOT NULL UNIQUE,
    package_version VARCHAR(32) NOT NULL--maybe will not be necessary
);

-- Which package is tracked by which user and what is users version of the package
CREATE TABLE IF NOT EXISTS tracking (
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id         BIGINT NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    package_id      BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    users_version   VARCHAR(32) NOT NULL,
    PRIMARY KEY (user_id, package_id)
);

--INDEXES
--TRIGGERS