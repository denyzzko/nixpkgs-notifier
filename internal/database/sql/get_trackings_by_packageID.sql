SELECT user_id,
       package_id,
       last_notified_version
FROM trackings
WHERE package_id = $1
;