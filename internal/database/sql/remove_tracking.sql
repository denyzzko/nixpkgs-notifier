DELETE FROM trackings
WHERE package_id = $1 AND user_id = $2
;