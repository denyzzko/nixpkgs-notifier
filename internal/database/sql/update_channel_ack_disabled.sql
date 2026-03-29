UPDATE channels
SET disabled_by_server = false
WHERE id = $1 
    AND user_id = $2
;
