SELECT c.id,
       c.is_enabled,
       c.disabled_by_server,
       e.email_address,
       w.webhook_url,
       w.webhook_type,
       w.username,
       w.channel,
       w.priority,
       w.request_ack,
       COALESCE(e.notify_on_manual_verify, w.notify_on_manual_verify) AS notify_on_manual_verify
FROM channels c
LEFT JOIN emails e   ON e.channel_id = c.id
LEFT JOIN webhooks w ON w.channel_id = c.id
WHERE c.user_id = $1
    AND (e.channel_id IS NOT NULL OR w.channel_id IS NOT NULL)
ORDER BY c.created_at ASC
;