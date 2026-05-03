DELETE FROM watchlist
WHERE package_id = $1
RETURNING id, user_id
;