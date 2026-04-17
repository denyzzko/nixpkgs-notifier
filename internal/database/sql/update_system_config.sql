INSERT INTO system_config (
    id,
    notification_dispatch_interval,
    notification_max_retries,
    notification_disable_on_max_retries,
    notification_worker_count,
    package_check_interval,
    package_check_worker_count,
    package_check_skip_interval,
    notification_retention_days,
    max_webhooks_per_user,
    updated_at
) VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT (id) DO UPDATE SET
    notification_dispatch_interval      = EXCLUDED.notification_dispatch_interval,
    notification_max_retries            = EXCLUDED.notification_max_retries,
    notification_disable_on_max_retries = EXCLUDED.notification_disable_on_max_retries,
    notification_worker_count           = EXCLUDED.notification_worker_count,
    package_check_interval              = EXCLUDED.package_check_interval,
    package_check_worker_count          = EXCLUDED.package_check_worker_count,
    package_check_skip_interval         = EXCLUDED.package_check_skip_interval,
    notification_retention_days         = EXCLUDED.notification_retention_days,
    max_webhooks_per_user               = EXCLUDED.max_webhooks_per_user,
    updated_at                          = now();