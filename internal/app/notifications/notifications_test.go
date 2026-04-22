// Package notifications_test contains integration tests for the notifications app layer.
package notifications_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/notifications"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/testutil"
)

var testStore *database.Store

func TestMain(m *testing.M) {
	store, cleanup := testutil.StartTestDB()
	testStore = store
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// setup holds everything needed to call CreatePendingNotifications in test.
type setup struct {
	userID    int64
	packageID int64
	channelID int64
}

// newSetup creates user, package, tracking and one email channel.
// notifyOnManualVerify controls whether notification is sent to channel on manual check.
// lastNotifiedVersion is what the tracking row records as the version user was last notified about.
func newSetup(t *testing.T, notifyOnManualVerify bool, lastNotifiedVersion string) setup {
	t.Helper()
	ctx := context.Background()

	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	pkgName := fmt.Sprintf("testpkg-%d", testutil.NextID())
	pkgID, err := testStore.StorePackage(ctx, pkgName, "nixpkgs-unstable", lastNotifiedVersion)
	if err != nil {
		t.Fatalf("newSetup: StorePackage: %v", err)
	}

	err = testStore.StoreTracking(ctx, userID, pkgID, lastNotifiedVersion)
	if err != nil {
		t.Fatalf("newSetup: StoreTracking: %v", err)
	}

	chID, err := testStore.CreateEmailChannel(ctx, userID, fmt.Sprintf("user%d@example.com", testutil.NextID()), notifyOnManualVerify)
	if err != nil {
		t.Fatalf("newSetup: CreateEmailChannel: %v", err)
	}

	return setup{userID: userID, packageID: pkgID, channelID: chID}
}

// notificationCount queries how many notifications exist for user.
// Used to verify the result of CreatePendingNotifications which returns nothing.
func notificationCount(t *testing.T, userID int64) int {
	t.Helper()
	logs, err := testStore.QueryNotificationsByUserID(context.Background(), userID)
	if err != nil {
		t.Fatalf("notificationCount: %v", err)
	}
	return len(logs)
}

// ----------------------------------------------------------------
// -------------- CreatePendingNotifications ----------------------
// ----------------------------------------------------------------

func TestCreatePendingNotifications_SystemTriggered_CreatesNotification(t *testing.T) {
	ctx := context.Background()

	s := newSetup(t, false, "1.0.0")

	notifications.CreatePendingNotifications(ctx, testStore, s.packageID, "testpkg", "nixpkgs-unstable", "2.0.0", 0)

	count := notificationCount(t, s.userID)
	if count != 1 {
		t.Errorf("expected 1 notification for system-triggered check, got %d", count)
	}
}

func TestCreatePendingNotifications_ManualTrigger_ChannelOptedOut_SkipsNotification(t *testing.T) {
	ctx := context.Background()
	// channel has notifyOnManualVerify=false and triggerUser is set to userID (manual check)
	s := newSetup(t, false, "1.0.0")

	notifications.CreatePendingNotifications(ctx, testStore, s.packageID, "testpkg", "nixpkgs-unstable", "2.0.0", s.userID)

	count := notificationCount(t, s.userID)
	if count != 0 {
		t.Errorf("expected 0 notifications when channel opted out of manual checks, got %d", count)
	}
}

func TestCreatePendingNotifications_ManualTrigger_ChannelOptedIn_CreatesNotification(t *testing.T) {
	ctx := context.Background()
	// channel has notifyOnManualVerify=true and triggerUser is set to userID (manual check)
	s := newSetup(t, true, "1.0.0")

	notifications.CreatePendingNotifications(ctx, testStore, s.packageID, "testpkg", "nixpkgs-unstable", "2.0.0", s.userID)

	count := notificationCount(t, s.userID)
	if count != 1 {
		t.Errorf("expected 1 notification when channel opted in to manual checks, got %d", count)
	}
}

func TestCreatePendingNotifications_AlreadyOnNewVersion_SkipsNotification(t *testing.T) {
	ctx := context.Background()
	// lastNotifiedVersion == newVersion - user already notified, no duplicate should be created
	s := newSetup(t, true, "2.0.0")

	notifications.CreatePendingNotifications(ctx, testStore, s.packageID, "testpkg", "nixpkgs-unstable", "2.0.0", 0)

	count := notificationCount(t, s.userID)
	if count != 0 {
		t.Errorf("expected 0 notifications when user already on new version, got %d", count)
	}
}

func TestCreatePendingNotifications_ManualTrigger_OnlySkipsTriggeringUser(t *testing.T) {
	ctx := context.Background()

	// two users tracking the same package, both channels opted out of manual checks
	s1 := newSetup(t, false, "1.0.0")

	// reuse the same package for user2
	user2ID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	err := testStore.StoreTracking(ctx, user2ID, s1.packageID, "1.0.0")
	if err != nil {
		t.Fatalf("setup user2 tracking: %v", err)
	}
	_, err = testStore.CreateEmailChannel(ctx, user2ID, fmt.Sprintf("user2-%d@example.com", testutil.NextID()), false)
	if err != nil {
		t.Fatalf("setup user2 channel: %v", err)
	}

	// user1 manually triggers - their channel should be skipped
	// and user2's channel should get notification
	notifications.CreatePendingNotifications(ctx, testStore, s1.packageID, "testpkg", "nixpkgs-unstable", "2.0.0", s1.userID)

	count := notificationCount(t, s1.userID)
	if count != 0 {
		t.Errorf("expected 0 notifications for triggering user (opted out), got %d", count)
	}
	count = notificationCount(t, user2ID)
	if count != 1 {
		t.Errorf("expected 1 notification for other user, got %d", count)
	}
}

// ----------------------------------------------------------------
// -------- CreatePendingNotificationsFirstAppearance -------------
// ----------------------------------------------------------------

func TestCreatePendingNotificationsFirstAppearance_SkipsVersionCheck(t *testing.T) {
	ctx := context.Background()
	// lastNotifiedVersion == newVersion - normally skipped by CreatePendingNotifications
	// but FirstAppearance version creates regardless
	s := newSetup(t, true, "1.0.0")

	notifications.CreatePendingNotificationsFirstAppearance(ctx, testStore, s.packageID, "testpkg", "nixpkgs-unstable", "1.0.0", 0)

	count := notificationCount(t, s.userID)
	if count != 1 {
		t.Errorf("expected 1 notification for first appearance (no version check), got %d", count)
	}
}

func TestCreatePendingNotificationsFirstAppearance_ManualTrigger_ChannelOptedOut_SkipsNotification(t *testing.T) {
	ctx := context.Background()
	// channel has notifyOnManualVerify=false - manual trigger should skip it
	s := newSetup(t, false, "1.0.0")

	notifications.CreatePendingNotificationsFirstAppearance(ctx, testStore, s.packageID, "testpkg", "nixpkgs-unstable", "1.0.0", s.userID)

	count := notificationCount(t, s.userID)
	if count != 0 {
		t.Errorf("expected 0 notifications for manual trigger with opted-out channel, got %d", count)
	}
}

func TestCreatePendingNotificationsFirstAppearance_OldVersionIsEmpty(t *testing.T) {
	ctx := context.Background()
	s := newSetup(t, true, "1.0.0")

	notifications.CreatePendingNotificationsFirstAppearance(ctx, testStore, s.packageID, "testpkg", "nixpkgs-unstable", "1.0.0", 0)

	logs, err := testStore.QueryNotificationsByUserID(ctx, s.userID)
	if err != nil {
		t.Fatalf("query notifications: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(logs))
	}
	if logs[0].OldVersion != "" {
		t.Errorf("OldVersion = %q, want empty string for first appearance notification", logs[0].OldVersion)
	}
}

// ----------------------------------------------------------------
// -------------------- GetDeliveryLog ----------------------------
// ----------------------------------------------------------------

func TestGetDeliveryLog_Empty(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	logs, err := notifications.GetDeliveryLog(ctx, testStore, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("expected empty delivery log, got %d entries", len(logs))
	}
}

func TestGetDeliveryLog_ReturnsNotificationsForUser(t *testing.T) {
	ctx := context.Background()
	s := newSetup(t, true, "1.0.0")

	// create a notification so the log is not empty
	notifications.CreatePendingNotifications(ctx, testStore, s.packageID, "testpkg", "nixpkgs-unstable", "2.0.0", 0)

	logs, err := notifications.GetDeliveryLog(ctx, testStore, s.userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("expected 1 delivery log entry, got %d", len(logs))
	}
}

func TestGetDeliveryLog_IsolatedPerUser(t *testing.T) {
	ctx := context.Background()
	s1 := newSetup(t, true, "1.0.0")

	// user2 also tracks the same package and has a channel - so they also get notification
	user2ID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	err := testStore.StoreTracking(ctx, user2ID, s1.packageID, "1.0.0")
	if err != nil {
		t.Fatalf("setup user2 tracking: %v", err)
	}
	_, err = testStore.CreateEmailChannel(ctx, user2ID, fmt.Sprintf("user2-%d@example.com", testutil.NextID()), true)
	if err != nil {
		t.Fatalf("setup user2 channel: %v", err)
	}

	// system check creates one notification per user
	notifications.CreatePendingNotifications(ctx, testStore, s1.packageID, "testpkg", "nixpkgs-unstable", "2.0.0", 0)

	// each user must only see their own entry - not each other's
	logs1, err := notifications.GetDeliveryLog(ctx, testStore, s1.userID)
	if err != nil {
		t.Fatalf("user1 query: %v", err)
	}
	if len(logs1) != 1 {
		t.Errorf("expected user1 to have 1 delivery log entry, got %d", len(logs1))
	}

	logs2, err := notifications.GetDeliveryLog(ctx, testStore, user2ID)
	if err != nil {
		t.Fatalf("user2 query: %v", err)
	}
	if len(logs2) != 1 {
		t.Errorf("expected user2 to have 1 delivery log entry, got %d", len(logs2))
	}
}
