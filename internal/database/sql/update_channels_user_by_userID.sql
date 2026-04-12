UPDATE channels
SET user_id = $1
WHERE user_id = $2
AND id NOT IN (
    SELECT c.id FROM channels c
    JOIN emails e ON e.channel_id = c.id
    WHERE c.user_id = $2
    AND e.email_address IN (
        SELECT e2.email_address FROM channels c2
        JOIN emails e2 ON e2.channel_id = c2.id
        WHERE c2.user_id = $1
    )
    UNION
    SELECT c.id FROM channels c
    JOIN webhooks w ON w.channel_id = c.id
    WHERE c.user_id = $2
    AND EXISTS (
        SELECT 1 FROM channels c2
        JOIN webhooks w2 ON w2.channel_id = c2.id
        WHERE c2.user_id = $1
        AND w2.webhook_url      = w.webhook_url
        AND w2.webhook_type     = w.webhook_type
        AND w2.username         = w.username
        AND w2.channel          = w.channel
        AND w2.priority         = w.priority
        AND w2.request_ack      = w.request_ack
    )
)
;