// Package dispatcher_test contains integration tests for the dispatcher package.
// Tests use FakeSender that records calls instead of hitting real email or webhook endpoints.
package dispatcher_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/dispatcher"
	"github.com/denyzzko/nixpkgs-notifier/internal/notify"
	"github.com/denyzzko/nixpkgs-notifier/internal/testutil"
)

// FakeSender records every Send/SendTest call made by dispatcher.
// Optional SendErr/SendTestErr can be set to simulate delivery failures.
type FakeSender struct {
	mu          sync.Mutex
	SendCalls   []notify.VersionChangeEvent
	TestCalls   []notify.VersionChangeEvent
	SendErr     error // returned by Send when not nil
	SendTestErr error // returned by SendTest when not nil
}

func (f *FakeSender) Send(_ context.Context, event notify.VersionChangeEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SendCalls = append(f.SendCalls, event)
	return f.SendErr
}

func (f *FakeSender) SendTest(_ context.Context, event notify.VersionChangeEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TestCalls = append(f.TestCalls, event)
	return f.SendTestErr
}

func (f *FakeSender) sendCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.SendCalls)
}

func (f *FakeSender) testCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.TestCalls)
}

func (f *FakeSender) lastTestCall() notify.VersionChangeEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.TestCalls[len(f.TestCalls)-1]
}

// ----------------------------------------------------------------
// 							Test helpers
// ----------------------------------------------------------------

var testStore *database.Store

func TestMain(m *testing.M) {
	store, cleanup := testutil.StartTestDB()
	testStore = store
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// defaultCfg returns dispatcher.Config suitable for tests with single worker.
func defaultCfg() dispatcher.Config {
	return dispatcher.Config{
		MaxRetries:          3,
		WorkerCount:         1,
		DisableOnMaxRetries: false,
	}
}

// setupEmailNotification creates minimal set of database rows needed so
// dispatch() finds one pending notification for email channel.
// Returns userID and channelID so callers can query their state later.
func setupEmailNotification(t *testing.T, oldVersion, newVersion string) (userID, channelID int64) {
	t.Helper()
	ctx := context.Background()

	userID, _, _ = testutil.CreateTestUser(t, testStore, "user")

	pkgName := fmt.Sprintf("testpkg-%d", testutil.NextID())
	pkgID, err := testStore.StorePackage(ctx, pkgName, "nixpkgs-unstable", oldVersion)
	if err != nil {
		t.Fatalf("setupEmailNotification: StorePackage: %v", err)
	}

	err = testStore.StoreTracking(ctx, userID, pkgID, oldVersion)
	if err != nil {
		t.Fatalf("setupEmailNotification: StoreTracking: %v", err)
	}

	addr := fmt.Sprintf("user%d@example.com", testutil.NextID())
	chID, err := testStore.CreateEmailChannel(ctx, userID, addr, false)
	if err != nil {
		t.Fatalf("setupEmailNotification: CreateEmailChannel: %v", err)
	}

	jobs := []database.ChannelNotification{
		{
			Channel: database.ActiveChannel{
				ChannelID: chID,
				UserID:    userID,
				Email:     &database.Email{Address: addr},
			},
			OldVersion: oldVersion,
		},
	}
	err = testStore.CreateNotificationsForVersionChange(ctx, pkgName, "nixpkgs-unstable", newVersion, pkgID, jobs, time.Now().UTC())
	if err != nil {
		t.Fatalf("setupEmailNotification: CreateNotificationsForVersionChange: %v", err)
	}

	return userID, chID
}

// setupWebhookNotification creates minimal set of database rows needed so
// dispatch() finds one pending notification for a webhook channel.
// Returns userID and channelID so callers can query their state later.
func setupWebhookNotification(t *testing.T, oldVersion, newVersion string) (userID, channelID int64) {
	t.Helper()
	ctx := context.Background()

	userID, _, _ = testutil.CreateTestUser(t, testStore, "user")

	pkgName := fmt.Sprintf("testpkg-%d", testutil.NextID())
	pkgID, err := testStore.StorePackage(ctx, pkgName, "nixpkgs-unstable", oldVersion)
	if err != nil {
		t.Fatalf("setupWebhookNotification: StorePackage: %v", err)
	}

	err = testStore.StoreTracking(ctx, userID, pkgID, oldVersion)
	if err != nil {
		t.Fatalf("setupWebhookNotification: StoreTracking: %v", err)
	}

	url := fmt.Sprintf("https://hooks.example.com/webhook-%d", testutil.NextID())
	chID, err := testStore.CreateWebhookChannel(ctx, userID, url, "generic", false, database.MattermostParams{})
	if err != nil {
		t.Fatalf("setupWebhookNotification: CreateWebhookChannel: %v", err)
	}

	jobs := []database.ChannelNotification{
		{
			Channel: database.ActiveChannel{
				ChannelID: chID,
				UserID:    userID,
				Webhook:   &database.Webhook{URL: url, Type: "generic"},
			},
			OldVersion: oldVersion,
		},
	}
	err = testStore.CreateNotificationsForVersionChange(ctx, pkgName, "nixpkgs-unstable", newVersion, pkgID, jobs, time.Now().UTC())
	if err != nil {
		t.Fatalf("setupWebhookNotification: CreateNotificationsForVersionChange: %v", err)
	}

	return userID, chID
}

// notificationsForUser returns all notification records for given user.
func notificationsForUser(t *testing.T, userID int64) []database.UserNotification {
	t.Helper()
	rows, err := testStore.QueryNotificationsByUserIDPaged(context.Background(), userID, 100, 0)
	if err != nil {
		t.Fatalf("notificationsForUser: %v", err)
	}
	return rows
}

// isChannelDisabledByServer returns DisabledByServer flag for the given channel.
func isChannelDisabledByServer(t *testing.T, userID, channelID int64) bool {
	t.Helper()
	channels, err := testStore.QueryChannelsByUserID(context.Background(), userID)
	if err != nil {
		t.Fatalf("isChannelDisabledByServer: %v", err)
	}
	for _, ch := range channels {
		if ch.ID == channelID {
			return ch.DisabledByServer
		}
	}
	t.Fatalf("isChannelDisabledByServer: channel %d not found", channelID)
	return false
}

// ----------------------------------------------------------------
// ------------------------ Dispatch ------------------------------
// ----------------------------------------------------------------

// TestDispatch_EmailNotification_MarkedSent verifies that pending email
// notification is delivered via FakeSender and marked as "sent".
func TestDispatch_EmailNotification_MarkedSent(t *testing.T) {
	fake := &FakeSender{}
	cfg := defaultCfg()
	d := dispatcher.NewWithSenders(testStore, cfg, fake, fake)

	userID, _ := setupEmailNotification(t, "1.0.0", "2.0.0")

	d.Dispatch(context.Background(), cfg)

	// send should have been called once
	got := fake.sendCallCount()
	if got != 1 {
		t.Errorf("Send call count: got %d, want 1", got)
	}

	// notification should now be marked sent
	notifications := notificationsForUser(t, userID)
	if len(notifications) != 1 {
		t.Fatalf("expected exactly 1 notification for user, got %d", len(notifications))
	}
	if notifications[0].Status != database.NotificationStatusSent {
		t.Errorf("notification status: got %q, want %q", notifications[0].Status, database.NotificationStatusSent)
	}
}

// TestDispatch_WebhookNotification_MarkedSent verifies that a pending webhook
// notification is delivered via FakeSender and subsequently marked as "sent".
func TestDispatch_WebhookNotification_MarkedSent(t *testing.T) {
	fake := &FakeSender{}
	cfg := defaultCfg()
	d := dispatcher.NewWithSenders(testStore, cfg, fake, fake)

	userID, _ := setupWebhookNotification(t, "1.0.0", "2.0.0")

	d.Dispatch(context.Background(), cfg)

	// send should have been called once
	got := fake.sendCallCount()
	if got != 1 {
		t.Errorf("Send call count: got %d, want 1", got)
	}

	// notification should now be marked sent
	notifications := notificationsForUser(t, userID)
	if len(notifications) != 1 {
		t.Fatalf("expected exactly 1 notification for user, got %d", len(notifications))
	}
	if notifications[0].Status != database.NotificationStatusSent {
		t.Errorf("notification status: got %q, want %q", notifications[0].Status, database.NotificationStatusSent)
	}
}

// TestDispatch_SenderFailure_MarkedFailed verifies that when FakeSender returns
// an error, notification status changes from "pending" to "failed" with
// attempt_count incremented to 1.
func TestDispatch_SenderFailure_MarkedFailed(t *testing.T) {
	fake := &FakeSender{SendErr: &notify.SenderError{
		PublicMsg: "connection refused",
		Err:       errors.New("smtp: connection refused"),
	}}
	cfg := defaultCfg()
	d := dispatcher.NewWithSenders(testStore, cfg, fake, fake)

	userID, _ := setupEmailNotification(t, "1.0.0", "3.0.0")

	d.Dispatch(context.Background(), cfg)

	// send should have been called once
	got := fake.sendCallCount()
	if got != 1 {
		t.Errorf("Send call count: got %d, want 1", got)
	}

	// notification should now be marked failed with attempt_count=1
	notifications := notificationsForUser(t, userID)
	if len(notifications) != 1 {
		t.Fatalf("expected exactly 1 notification for user, got %d", len(notifications))
	}
	n := notifications[0]
	if n.Status != database.NotificationStatusFailed {
		t.Errorf("notification status: got %q, want %q", n.Status, database.NotificationStatusFailed)
	}
	if n.AttemptCount != 1 {
		t.Errorf("attempt count: got %d, want 1", n.AttemptCount)
	}
}

// TestDispatch_MaxRetries_ChannelDisabled verifies that when DisableOnMaxRetries
// is true and notification has already failed MaxRetries-1 times, one more
// failure causes channel to be disabled by server.
func TestDispatch_MaxRetries_ChannelDisabled(t *testing.T) {
	fake := &FakeSender{SendErr: &notify.SenderError{
		PublicMsg: "delivery failed",
		Err:       errors.New("smtp: bad gateway"),
	}}
	cfg := defaultCfg()
	cfg.MaxRetries = 2
	cfg.DisableOnMaxRetries = true
	d := dispatcher.NewWithSenders(testStore, cfg, fake, fake)

	userID, chID := setupEmailNotification(t, "1.0.0", "9.0.0")

	// First attempt fail -> attempt_count becomes 1 (< MaxRetries=2)
	d.Dispatch(context.Background(), cfg)
	if isChannelDisabledByServer(t, userID, chID) {
		t.Fatal("channel should NOT be disabled after first failure")
	}

	// Second attempt fail -> attempt_count becomes 2 -> attempt_count==MaxRetries -> channel disabled
	d.Dispatch(context.Background(), cfg)
	if !isChannelDisabledByServer(t, userID, chID) {
		t.Fatal("channel should be disabled after reaching MaxRetries")
	}
}

// ----------------------------------------------------------------
// ---------------------- Dispatch - Test -------------------------
// ----------------------------------------------------------------

// TestDispatchTest_Email_CallsSendTest verifies that dispatcher.Test() routes
// to FakeSender.SendTest (not Send) for email channel.
func TestDispatchTest_Email_CallsSendTest(t *testing.T) {
	fake := &FakeSender{}
	d := dispatcher.NewWithSenders(testStore, defaultCfg(), fake, fake)

	email := &database.Email{Address: "ping@example.com"}
	err := d.Test(context.Background(), 42, email, nil)
	if err != nil {
		t.Fatalf("dispatcher.Test returned unexpected error: %v", err)
	}

	got := fake.testCallCount()
	if got != 1 {
		t.Errorf("SendTest call count: got %d, want 1", got)
	}
	got = fake.sendCallCount()
	if got != 0 {
		t.Errorf("Send should not be called during Test(), got %d calls", got)
	}
	if fake.lastTestCall().RecipientAddress != "ping@example.com" {
		t.Errorf("RecipientAddress: got %q, want %q", fake.lastTestCall().RecipientAddress, "ping@example.com")
	}
}

// TestDispatchTest_Email_CallsSendTest verifies that dispatcher.Test() routes
// to FakeSender.SendTest (not Send) for webhook channel.
func TestDispatchTest_Webhook_CallsSendTest(t *testing.T) {
	fake := &FakeSender{}
	d := dispatcher.NewWithSenders(testStore, defaultCfg(), fake, fake)

	webhook := &database.Webhook{URL: "https://hooks.example.com/notify", Type: "generic"}
	err := d.Test(context.Background(), 99, nil, webhook)
	if err != nil {
		t.Fatalf("dispatcher.Test returned unexpected error: %v", err)
	}

	got := fake.testCallCount()
	if got != 1 {
		t.Errorf("SendTest call count: got %d, want 1", got)
	}
	got = fake.sendCallCount()
	if got != 0 {
		t.Errorf("Send should not be called during Test(), got %d calls", got)
	}
	if fake.lastTestCall().RecipientAddress != "https://hooks.example.com/notify" {
		t.Errorf("RecipientAddress: got %q, want %q", fake.lastTestCall().RecipientAddress, "https://hooks.example.com/notify")
	}
}
