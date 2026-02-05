SELECT id,
       created_at,
       username,
       user_role
FROM user
WHERE id = $1
;