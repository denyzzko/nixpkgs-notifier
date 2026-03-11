WITH upd_email AS (
    UPDATE emails 
    SET
        notify_on_manual_verify = $2 
    WHERE channel_id = $1
        AND channel_id IN (SELECT id FROM channels WHERE user_id = $3)
)
UPDATE webhooks 
SET
    notify_on_manual_verify = $2
WHERE channel_id = $1
    AND channel_id IN (SELECT id FROM channels WHERE user_id = $3)
;