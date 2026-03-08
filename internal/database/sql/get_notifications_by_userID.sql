SELECT
    n.id,
    n.detected_at,
    n.old_version,
    n.new_version,
    n.status,
    n.attempt_count,
    n.error_message,
    p.name      AS package_name,
    p.branch    AS package_branch,
    e.email_address,
    w.webhook_url
FROM notifications n
JOIN channels c      ON c.id = n.channel_id
JOIN packages p      ON p.id = n.package_id
LEFT JOIN emails e   ON e.channel_id = n.channel_id
LEFT JOIN webhooks w ON w.channel_id = n.channel_id
WHERE c.user_id = $1
ORDER BY n.detected_at DESC
;