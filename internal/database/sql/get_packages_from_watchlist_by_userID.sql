SELECT w.id,
       w.created_at,
       w.user_id,
       w.package_id,
       p.name,
       p.branch
FROM watchlist w
JOIN packages p ON p.id = w.package_id
WHERE w.user_id = $1
ORDER BY w.created_at DESC
;