SELECT p.id,
       p.name,
       p.branch,
       t.last_notified_version
FROM trackings t
JOIN packages p ON t.package_id = p.id
WHERE t.user_id = $1
;