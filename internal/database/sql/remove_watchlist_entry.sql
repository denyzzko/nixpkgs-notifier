DELETE FROM watchlist
WHERE id      = $1
  AND user_id = $2
RETURNING package_id
;