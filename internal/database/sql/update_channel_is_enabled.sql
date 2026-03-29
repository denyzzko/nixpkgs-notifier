UPDATE channels
SET 
    is_enabled = $2,
    disabled_by_server = false
WHERE id = $1 
AND user_id = $3
;