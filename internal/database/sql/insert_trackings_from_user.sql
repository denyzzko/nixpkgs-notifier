INSERT INTO trackings (user_id, package_id, last_notified_version)
SELECT $1, package_id, last_notified_version FROM trackings WHERE user_id = $2
ON CONFLICT DO NOTHING
;