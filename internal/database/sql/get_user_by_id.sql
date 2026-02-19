SELECT id,
       created_at,
       username,
       role
FROM users
WHERE id = $1
;