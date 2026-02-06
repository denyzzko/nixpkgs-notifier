SELECT id,
       created_at,
       username,
       role
FROM user
WHERE id = $1
;