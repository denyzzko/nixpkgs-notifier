UPDATE check_state
SET status    = 'failed',
    error_msg = $3
WHERE user_id   = $1
  AND package_id = $2
;