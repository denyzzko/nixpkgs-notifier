SELECT created_at,
       updated_at,
       user_id,
       package_id,
       last_notified_version
FROM tracking
WHERE user_id = $1
AND package_id = $2
;