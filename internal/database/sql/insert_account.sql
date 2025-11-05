INSERT INTO accounts (user_id, email_address, email_verified, provider, issuer, subject)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (issuer, subject) DO UPDATE
  SET user_id = EXCLUDED.user_id
RETURNING user_id;