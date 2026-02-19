SELECT id,
       created_at,
       updated_at,
       name,
       branch,
       current_version
FROM packages
WHERE name = $1
AND branch = $2
;