SELECT COUNT(*)
FROM accounts
WHERE user_id = $1
;