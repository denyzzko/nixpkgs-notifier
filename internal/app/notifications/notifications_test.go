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
	count, err := testStore.CountNotificationsByUserID(context.Background(), userID)
	if err != nil {
		t.Fatalf("notificationCount: %v", err)
	}
	return int(count)
}

// ----------------------------------------------------------------
// -------------- CreatePendingNotifications ----------------------
// ----------------------------------------------------------------

func TestCreatePendingNotifications_SystemTriggered_CreatesNotification(t *testing.T) {
	ctx := context.Background()

	s := newSetup(t, false, "1.0.0")

	notifications.CreatePendingNotifications(ctx, testStore, notifications.VersionEvent{PackageID: s.packageID, PackageName: "testpkg", Branch: "nixpkgs-unstable", NewVersion: "2.0.0"}, 0)

	count := notificationCount(t, s.userID)
	if count != 1 {
		t.Errorf("expected 1 notification for system-triggered check, got %d", count)
	}
}

func TestCreatePendingNotifications_ManualTrigger_ChannelOptedOut_SkipsNotification(t *testing.T) {
	ctx := context.Background()
	// channel has notifyOnManualVerify=false and triggerUser is set to userID (manual check)
	s := newSetup(t, false, "1.0.0")

	notifications.CreatePendingNotifications(ctx, testStore, notifications.VersionEvent{PackageID: s.packageID, PackageName: "testpkg", Branch: "nixpkgs-unstable", NewVersion: "2.0.0"}, s.userID)

	count := notificationCount(t, s.userID)
	if count != 0 {
		t.Errorf("expected 0 notifications when channel opted out of manual checks, got %d", count)
	}
}

func TestCreatePendingNotifications_ManualTrigger_ChannelOptedIn_CreatesNotification(t *testing.T) {
	ctx := context.Background()
	// channel has notifyOnManualVerify=true and triggerUser is set to userID (manual check)
	s := newSetup(t, true, "1.0.0")

	notifications.CreatePendingNotifications(ctx, testStore, notifications.VersionEvent{PackageID: s.packageID, PackageName: "testpkg", Branch: "nixpkgs-unstable", NewVersion: "2.0.0"}, s.userID)

	count := notificationCount(t, s.userID)
	if count != 1 {
		t.Errorf("expected 1 notification when channel opted in to manual checks, got %d", count)
	}
}

func TestCreatePendingNotifications_AlreadyOnNewVersion_SkipsNotification(t *testing.T) {
	ctx := context.Background()
	// lastNotifiedVersion == newVersion - user already notified, no duplicate should be created
	s := newSetup(t, true, "2.0.0")

	notifications.CreatePendingNotifications(ctx, testStore, notifications.VersionEvent{PackageID: s.packageID, PackageName: "testpkg", Branch: "nixpkgs-unstable", NewVersion: "2.0.0"}, 0)

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
	notifications.CreatePendingNotifications(ctx, testStore, notifications.VersionEvent{PackageID: s1.packageID, PackageName: "testpkg", Branch: "nixpkgs-unstable", NewVersion: "2.0.0"}, s1.userID)

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

	notifications.CreatePendingNotificationsFirstAppearance(ctx, testStore, notifications.VersionEvent{PackageID: s.packageID, PackageName: "testpkg", Branch: "nixpkgs-unstable", NewVersion: "1.0.0"}, 0)

	count := notificationCount(t, s.userID)
	if count != 1 {
		t.Errorf("expected 1 notification for first appearance (no version check), got %d", count)
	}
}

func TestCreatePendingNotificationsFirstAppearance_ManualTrigger_ChannelOptedOut_SkipsNotification(t *testing.T) {
	ctx := context.Background()
	// channel has notifyOnManualVerify=false - manual trigger should skip it
	s := newSetup(t, false, "1.0.0")

	notifications.CreatePendingNotificationsFirstAppearance(ctx, testStore, notifications.VersionEvent{PackageID: s.packageID, PackageName: "testpkg", Branch: "nixpkgs-unstable", NewVersion: "1.0.0"}, s.userID)

	count := notificationCount(t, s.userID)
	if count != 0 {
		t.Errorf("expected 0 notifications for manual trigger with opted-out channel, got %d", count)
	}
}

func TestCreatePendingNotificationsFirstAppearance_OldVersionIsEmpty(t *testing.T) {
	ctx := context.Background()
	s := newSetup(t, true, "1.0.0")

	notifications.CreatePendingNotificationsFirstAppearance(ctx, testStore, notifications.VersionEvent{PackageID: s.packageID, PackageName: "testpkg", Branch: "nixpkgs-unstable", NewVersion: "1.0.0"}, 0)

	page, err := notifications.GetDeliveryLogPage(ctx, testStore, s.userID, 1, 10)
	if err != nil {
		t.Fatalf("query notifications: %v", err)
	}
	if len(page.Notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(page.Notifications))
	}
	if page.Notifications[0].OldVersion != "" {
		t.Errorf("OldVersion = %q, want empty string for first appearance notification", page.Notifications[0].OldVersion)
	}
}

// ----------------------------------------------------------------
// ------------------ GetDeliveryLogPage --------------------------
// ----------------------------------------------------------------

func TestGetDeliveryLogPage_Empty(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	page, err := notifications.GetDeliveryLogPage(ctx, testStore, userID, 1, 25)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(page.Notifications) != 0 {
		t.Errorf("expected empty notifications, got %d", len(page.Notifications))
	}
	if page.TotalPages != 1 {
		t.Errorf("expected TotalPages=1 for empty list, got %d", page.TotalPages)
	}
}

func TestGetDeliveryLogPage_ReturnsNotificationsForUser(t *testing.T) {
	ctx := context.Background()
	s := newSetup(t, true, "1.0.0")

	notifications.CreatePendingNotifications(ctx, testStore, notifications.VersionEvent{PackageID: s.packageID, PackageName: "testpkg", Branch: "nixpkgs-unstable", NewVersion: "2.0.0"}, 0)

	page, err := notifications.GetDeliveryLogPage(ctx, testStore, s.userID, 1, 25)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(page.Notifications) != 1 {
		t.Errorf("expected 1 notification, got %d", len(page.Notifications))
	}
}

func TestGetDeliveryLogPage_IsolatedPerUser(t *testing.T) {
	ctx := context.Background()
	s1 := newSetup(t, true, "1.0.0")

	user2ID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	err := testStore.StoreTracking(ctx, user2ID, s1.packageID, "1.0.0")
	if err != nil {
		t.Fatalf("setup user2 tracking: %v", err)
	}
	_, err = testStore.CreateEmailChannel(ctx, user2ID, fmt.Sprintf("user2-%d@example.com", testutil.NextID()), true)
	if err != nil {
		t.Fatalf("setup user2 channel: %v", err)
	}

	notifications.CreatePendingNotifications(ctx, testStore, notifications.VersionEvent{PackageID: s1.packageID, PackageName: "testpkg", Branch: "nixpkgs-unstable", NewVersion: "2.0.0"}, 0)

	// each user must only see their own notifications
	page1, err := notifications.GetDeliveryLogPage(ctx, testStore, s1.userID, 1, 25)
	if err != nil {
		t.Fatalf("user1 query: %v", err)
	}
	if len(page1.Notifications) != 1 {
		t.Errorf("expected user1 to have 1 notification, got %d", len(page1.Notifications))
	}

	page2, err := notifications.GetDeliveryLogPage(ctx, testStore, user2ID, 1, 25)
	if err != nil {
		t.Fatalf("user2 query: %v", err)
	}
	if len(page2.Notifications) != 1 {
		t.Errorf("expected user2 to have 1 notification, got %d", len(page2.Notifications))
	}
}

func TestGetDeliveryLogPage_Pagination(t *testing.T) {
	ctx := context.Background()
	s := newSetup(t, true, "1.0.0")

	// create 3 notifications by simulating 3 version changes
	for _, ver := range []string{"2.0.0", "3.0.0", "4.0.0"} {
		notifications.CreatePendingNotifications(ctx, testStore, notifications.VersionEvent{PackageID: s.packageID, PackageName: "testpkg", Branch: "nixpkgs-unstable", NewVersion: ver}, 0)
		// update last_notified_version so next version change is valid
		err := testStore.StoreTracking(ctx, s.userID, s.packageID, ver)
		if err != nil {
			t.Fatalf("StoreTracking %s: %v", ver, err)
		}
	}

	// fetch with page size 2
	page1, err := notifications.GetDeliveryLogPage(ctx, testStore, s.userID, 1, 2)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page1.Notifications) != 2 {
		t.Errorf("page 1: expected 2 notifications, got %d", len(page1.Notifications))
	}

	page2, err := notifications.GetDeliveryLogPage(ctx, testStore, s.userID, 2, 2)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2.Notifications) != 1 {
		t.Errorf("page 2: expected 1 notification, got %d", len(page2.Notifications))
	}

	if page1.TotalPages != 2 {
		t.Errorf("TotalPages = %d, want 2", page1.TotalPages)
	}
}

func TestGetDeliveryLogPage_PageCappedWhenTooHigh(t *testing.T) {
	ctx := context.Background()
	s := newSetup(t, true, "1.0.0")

	notifications.CreatePendingNotifications(ctx, testStore, notifications.VersionEvent{PackageID: s.packageID, PackageName: "testpkg", Branch: "nixpkgs-unstable", NewVersion: "2.0.0"}, 0)

	// request page 999 - should be capped to last valid page and return results
	page, err := notifications.GetDeliveryLogPage(ctx, testStore, s.userID, 999, 25)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.CurrentPage != 1 {
		t.Errorf("expected CurrentPage=1 after cap, got %d", page.CurrentPage)
	}
	if len(page.Notifications) == 0 {
		t.Error("expected notifications after page cap, got empty list")
	}
}
