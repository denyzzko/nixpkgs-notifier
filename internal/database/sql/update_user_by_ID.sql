UPDATE users
SET
    username = $2,
    role     = $3
WHERE id = $1
;