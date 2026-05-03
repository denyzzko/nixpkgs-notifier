UPDATE check_state
SET status      = 'done',
    new_version = $3
WHERE user_id   = $1
  AND package_id = $2
;