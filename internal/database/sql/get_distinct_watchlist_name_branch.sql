SELECT DISTINCT p.id,
                p.name,
                p.branch
FROM watchlist w
JOIN packages p ON p.id = w.package_id
;