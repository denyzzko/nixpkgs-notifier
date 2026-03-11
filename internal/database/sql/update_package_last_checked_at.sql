UPDATE packages
SET last_checked_at = now()
WHERE id = $1
;