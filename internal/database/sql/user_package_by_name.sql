SELECT t.created_at,
       t.updated_at,
       t.user_id,
       t.package_id,
       t.users_version
FROM tracking AS t
JOIN packages AS p ON p.id = t.package_id
WHERE t.user_id = $1
AND p.package_name = $2
;