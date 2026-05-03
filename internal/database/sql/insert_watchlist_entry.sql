INSERT INTO watchlist (user_id, package_id)
VALUES ($1, $2)
RETURNING id, created_at
;