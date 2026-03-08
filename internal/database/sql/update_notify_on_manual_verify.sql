WITH upd_email AS (
    UPDATE emails 
    SET
        notify_on_manual_verify = $2 
    WHERE channel_id = $1
)
UPDATE webhooks 
SET
    notify_on_manual_verify = $2
WHERE channel_id = $1
;