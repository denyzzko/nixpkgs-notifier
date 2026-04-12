DELETE FROM accounts
WHERE user_id = $1
  AND issuer = $2
  AND subject = $3
;