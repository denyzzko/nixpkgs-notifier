UPDATE channels
SET 
    is_enabled = $2
WHERE id = $1 
AND user_id = $3
;