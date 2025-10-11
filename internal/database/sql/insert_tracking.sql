INSERT INTO tracking (user_id, package_id, users_version)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, package_id)
DO UPDATE
    SET users_version = EXCLUDED.users_version,
        updated_at = now()
    WHERE tracking.users_version IS DISTINCT FROM EXCLUDED.users_version
;