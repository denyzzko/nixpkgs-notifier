SELECT id,
       created_at,
       updated_at,
       name,
       branch,
       current_version
FROM packages
AND id = $1
;