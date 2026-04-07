UPDATE users
SET 
    username = $2
WHERE id = $1
;