WITH new_channel AS (
    INSERT INTO channels (user_id, is_enabled)
    VALUES ($1, TRUE)
    RETURNING id
)
INSERT INTO webhooks (
    channel_id,
    webhook_url,
    webhook_type,
    notify_on_manual_verify,
    username,
    channel,
    priority,
    request_ack
)
SELECT id, $2, $3, $4, $5, $6, $7, $8
FROM new_channel
RETURNING channel_id;