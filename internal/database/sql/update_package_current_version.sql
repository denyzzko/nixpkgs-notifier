UPDATE packages
SET current_version = $2,
    updated_at      = now()
WHERE id = $1
;