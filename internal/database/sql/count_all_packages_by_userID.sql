SELECT COUNT(*) FROM (
    SELECT p.id
    FROM trackings t
    JOIN packages p ON t.package_id = p.id
    WHERE t.user_id = $1

    UNION ALL

    SELECT p.id
    FROM watchlist w
    JOIN packages p ON w.package_id = p.id
    WHERE w.user_id = $1
) combined
;