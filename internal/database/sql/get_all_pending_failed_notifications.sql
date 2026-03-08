SELECT n.id,
       n.channel_id,
       n.package_id,
       p.name,
       p.branch,
       n.old_version,
       n.new_version,
       n.detected_at,
       n.attempt_count,
       c.user_id,
       e.email_address,
       w.webhook_url
FROM notifications n
JOIN channels c    ON c.id = n.channel_id
JOIN packages p    ON p.id = n.package_id
LEFT JOIN emails e   ON e.channel_id = n.channel_id
LEFT JOIN webhooks w ON w.channel_id = n.channel_id
WHERE n.status = 'pending'
   OR (n.status = 'failed' AND n.attempt_count < $1)
ORDER BY n.created_at ASC
;