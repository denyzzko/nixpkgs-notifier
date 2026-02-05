INSERT INTO package (name, branch, current_version)
VALUES ($1, $2, $3)
ON CONFLICT (name, branch)
DO UPDATE
    SET current_version = EXCLUDED.current_version
        updated_at = now()
RETURNING id
;