SELECT
    notification_dispatch_interval,
    notification_max_retries,
    notification_disable_on_max_retries,
    notification_worker_count,
    package_check_interval,
    package_check_worker_count,
    package_check_skip_interval,
    notification_retention_days,
    max_webhooks_per_user,
    max_emails_per_user
FROM system_config
WHERE id = 1;