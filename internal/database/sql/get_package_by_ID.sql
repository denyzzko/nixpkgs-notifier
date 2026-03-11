SELECT id,
       created_at,
       updated_at,
       last_checked_at,
       name,
       branch,
       current_version
FROM packages
WHERE id = $1
;