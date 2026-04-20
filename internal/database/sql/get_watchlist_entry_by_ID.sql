SELECT id, 
       created_at,
       user_id,
       name,
       branch
FROM watchlist
WHERE id = $1 AND user_id = $2
;