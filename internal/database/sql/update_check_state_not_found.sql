UPDATE check_state
SET status = 'not_found'
WHERE user_id   = $1
  AND package_id = $2
;