SELECT id,
       created_at,
       package_name,
       package_version
FROM packages
WHERE package_name = $1
;