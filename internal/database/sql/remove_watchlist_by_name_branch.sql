DELETE FROM watchlist
WHERE name = $1 AND branch = $2
RETURNING id, user_id
;