SELECT c.id,
       c.is_enabled,
       e.email_address,
       w.webhook_url,
       COALESCE(e.notify_on_manual_verify, w.notify_on_manual_verify) AS notify_on_manual_verify
FROM channels c
LEFT JOIN emails e   ON e.channel_id = c.id
LEFT JOIN webhooks w ON w.channel_id = c.id
WHERE c.id      = $1
AND c.user_id = $2
;