INSERT INTO user (username, role)
VALUES ($1, $2)
RETURNING id
;