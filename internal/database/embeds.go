package database

import (
	_ "embed"
)

//go:embed sql/get_packages_from_trackings_by_userID.sql
var qGetUsersTrackedPackages string

//go:embed sql/get_package_from_trackings_by_userID_and_packageID.sql
var qGetUsersTrackedPackage string

//go:embed sql/get_all_packages.sql
var qGetAllPackages string

//go:embed sql/get_package_by_NAME_BRANCH.sql
var qGetPackageByNameAndBranch string

//go:embed sql/get_package_by_ID.sql
var qGetPackage string

//go:embed sql/get_tracking_by_userID_packageID.sql
var qGetTracking string

//go:embed sql/get_account_by_ISSUER_SUBJECT.sql
var qGetAccountByIssuerSub string

//go:embed sql/get_user_by_ID.sql
var qGetUser string

//go:embed sql/get_trackings_by_packageID.sql
var qGetTrackingsByPackageID string

//go:embed sql/get_active_channels_by_userID.sql
var qGetActiveChannelsByUserID string

//go:embed sql/get_channels_by_userID.sql
var qGetChannelsByUserID string

//go:embed sql/get_channel_by_ID.sql
var qGetChannelByID string

//go:embed sql/get_all_pending_failed_notifications.sql
var qGetAllPendingFailedNotifications string

//go:embed sql/get_notifications_by_userID.sql
var qGetNotificationsByUserID string

//go:embed sql/get_system_config.sql
var qGetSystemConfig string

//go:embed sql/insert_tracking.sql
var sInsertTracking string

//go:embed sql/insert_package.sql
var sInsertPackage string

//go:embed sql/insert_user.sql
var sInsertUser string

//go:embed sql/insert_account.sql
var sInsertAccount string

//go:embed sql/insert_notification.sql
var sInsertNotification string

//go:embed sql/insert_email_channel.sql
var sInsertEmailChannel string

//go:embed sql/insert_webhook_channel.sql
var sInsertWebhookChannel string

//go:embed sql/remove_tracking.sql
var dRemoveTracking string

//go:embed sql/remove_channel.sql
var dRemoveChannel string

//go:embed sql/remove_package.sql
var dRemovePackage string

//go:embed sql/update_notification_status_to_sent_by_ID.sql
var sUpdateNotificationToSent string

//go:embed sql/update_notification_status_to_failed_by_ID.sql
var sUpdateNotificationToFailed string

//go:embed sql/update_channel_is_enabled.sql
var sUpdateChannelIsEnabled string

//go:embed sql/update_notify_on_manual_verify.sql
var sUpdateNotifyOnManualVerify string

//go:embed sql/update_package_last_checked_at.sql
var sUpdatePackageLastCheckedAt string

//go:embed sql/update_system_config.sql
var sUpdateSystemConfig string

//go:embed sql/update_channel_disable_by_server.sql
var sUpdateChannelDisableByServer string

//go:embed sql/update_channel_ack_disabled.sql
var sUpdateChannelAckDisabled string
