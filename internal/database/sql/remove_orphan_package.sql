DELETE FROM packages
WHERE id = $1
  AND NOT EXISTS (SELECT 1 FROM trackings WHERE package_id = $1)
  AND NOT EXISTS (SELECT 1 FROM watchlist  WHERE package_id = $1)
;