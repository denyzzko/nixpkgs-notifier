SELECT id, 
       created_at,
       user_id,
       name,
       branch
FROM watchlist
WHERE user_id = $1
ORDER BY created_at DESC
;