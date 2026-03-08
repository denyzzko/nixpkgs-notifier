INSERT INTO notifications (channel_id, package_id, detected_at, old_version, new_version, status)
SELECT $1, $2, $3, $4, $5, 'pending'
WHERE NOT EXISTS (
    SELECT 1
    FROM notifications
    WHERE channel_id  = $1
      AND package_id  = $2
      AND new_version = $5
      AND status IN ('pending', 'sent')
)
;