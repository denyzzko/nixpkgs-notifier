UPDATE channels
SET is_enabled = false,
    disabled_by_server = true
WHERE id = $1 
    AND user_id = $2
;
