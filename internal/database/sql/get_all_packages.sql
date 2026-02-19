SELECT id,
       created_at,
       updated_at,
       name,
       branch,
       current_version
FROM packages
ORDER BY name, branch
;