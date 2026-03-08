UPDATE notifications
SET 
    status        = 'failed',
    attempt_count = attempt_count + 1,
    error_message = $2
WHERE id = $1
;