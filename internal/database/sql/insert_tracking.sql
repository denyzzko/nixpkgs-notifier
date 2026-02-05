INSERT INTO tracking (user_id, package_id, last_notified_version)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, package_id)
DO UPDATE
    SET last_notified_version = EXCLUDED.last_notified_version,
        updated_at = now()
    WHERE tracking.last_notified_version IS DISTINCT FROM EXCLUDED.last_notified_version
;