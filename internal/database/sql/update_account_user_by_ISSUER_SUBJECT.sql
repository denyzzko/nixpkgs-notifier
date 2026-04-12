UPDATE accounts
SET user_id = $1
WHERE issuer = $2 
    AND subject = $3
;