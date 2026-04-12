SELECT user_id,
       created_at,
       provider,
       issuer,
       subject,
       email,
       email_verified
FROM accounts
WHERE user_id = $1
ORDER BY created_at ASC
;