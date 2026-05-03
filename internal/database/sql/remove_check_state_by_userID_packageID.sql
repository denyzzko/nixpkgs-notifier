DELETE FROM check_state
WHERE user_id   = $1
  AND package_id = $2
;