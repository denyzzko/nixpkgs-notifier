SELECT COUNT(*)
FROM notifications n
JOIN channels c ON c.id = n.channel_id
WHERE c.user_id = $1
;