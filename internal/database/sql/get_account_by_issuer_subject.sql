SELECT user_id,
       created_at,
       provider,
       issuer,
       subject,
       email_address,
       email_verified
       
FROM accounts
WHERE issuer = $1
AND subject = $2