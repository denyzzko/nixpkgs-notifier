INSERT INTO users (username, user_role)
VALUES ($1, $2)
RETURNING id
;