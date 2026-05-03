INSERT INTO check_state (user_id, package_id, status, old_version, expires_at)
VALUES ($1, $2, 'pending', $3, now() + interval '1 hour')
ON CONFLICT (user_id, package_id) DO UPDATE SET
    status      = 'pending',
    old_version = EXCLUDED.old_version,
    new_version = NULL,
    error_msg   = NULL,
    started_at  = now(),
    expires_at  = now() + interval '1 hour'
;