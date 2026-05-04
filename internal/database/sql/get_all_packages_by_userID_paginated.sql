SELECT
    'tracked'              AS kind,
    p.id                   AS package_id,
    p.name,
    p.branch,
    t.last_notified_version,
    p.last_checked_at,
    p.current_version,
    NULL::bigint           AS watchlist_id
FROM trackings t
JOIN packages p ON t.package_id = p.id
WHERE t.user_id = $1

UNION ALL

SELECT
    'watching'             AS kind,
    p.id                   AS package_id,
    p.name,
    p.branch,
    NULL::text             AS last_notified_version,
    p.last_checked_at,
    p.current_version,
    w.id                   AS watchlist_id
FROM watchlist w
JOIN packages p ON w.package_id = p.id
WHERE w.user_id = $1

ORDER BY name ASC
LIMIT $2 OFFSET $3
;