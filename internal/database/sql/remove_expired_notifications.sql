DELETE FROM notifications
WHERE created_at < $1
;