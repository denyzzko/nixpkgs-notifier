DELETE FROM check_state
WHERE expires_at < now()
;