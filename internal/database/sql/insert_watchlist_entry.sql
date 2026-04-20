INSERT INTO watchlist (user_id, name, branch)
VALUES ($1, $2, $3)
RETURNING id, created_at
;