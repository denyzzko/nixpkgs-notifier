WITH new_channel AS (
    INSERT INTO channels (user_id, is_enabled)
    VALUES ($1, TRUE)
    RETURNING id
)
INSERT INTO emails (channel_id, email_address, notify_on_manual_verify)
SELECT id, $2, $3
FROM new_channel
RETURNING channel_id
;