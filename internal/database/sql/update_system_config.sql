INSERT INTO system_config (
    id,
    notification_dispatch_interval,
    notification_max_retries,
    notification_disable_on_max_retries,
    notification_worker_count,
    package_check_interval,
    package_check_worker_count,
    package_check_skip_interval,
    updated_at
) VALUES (1, $1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (id) DO UPDATE SET
    notification_dispatch_interval      = EXCLUDED.notification_dispatch_interval,
    notification_max_retries            = EXCLUDED.notification_max_retries,
    notification_disable_on_max_retries = EXCLUDED.notification_disable_on_max_retries,
    notification_worker_count           = EXCLUDED.notification_worker_count,
    package_check_interval              = EXCLUDED.package_check_interval,
    package_check_worker_count          = EXCLUDED.package_check_worker_count,
    package_check_skip_interval         = EXCLUDED.package_check_skip_interval,
    updated_at                          = now();