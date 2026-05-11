SELECT DISTINCT p.id,
                p.created_at,
                p.updated_at,
                p.last_checked_at,
                p.name,
                p.branch,
                p.current_version
FROM packages p
WHERE EXISTS (
    SELECT 1 FROM trackings t WHERE t.package_id = p.id
)
ORDER BY p.name, p.branch
;