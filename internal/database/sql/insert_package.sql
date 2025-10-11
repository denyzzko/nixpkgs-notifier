INSERT INTO packages (package_name, package_version)
VALUES ($1, $2)
ON CONFLICT (package_name)
DO UPDATE
    SET package_version = EXCLUDED.package_version
RETURNING id;
;