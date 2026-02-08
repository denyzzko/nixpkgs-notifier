SELECT id,
       created_at,
       updated_at,
       name,
       branch,
       current_version
FROM package
AND id = $1
;