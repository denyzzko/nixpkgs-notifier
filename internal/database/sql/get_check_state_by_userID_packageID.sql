SELECT user_id,
       package_id,
       status,
       old_version,
       new_version,
       error_msg,
       started_at,
       expires_at
FROM check_state
WHERE user_id   = $1
  AND package_id = $2
  AND expires_at > now()
;