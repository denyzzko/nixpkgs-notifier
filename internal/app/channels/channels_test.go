// Package channels_test contains integration tests for the channels app layer.
package channels_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/channels"
	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
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

// webhookURL is URL used in webhook tests.
// ValidateWebhookURL performs DNS resolution - this URL should always resolve to public IP.
const webhookURL = "https://example.com/webhook"

// addEmail is test helper that inserts email channel row.
// Used to set up existing channels for guard tests.
func addEmail(t *testing.T, userID int64, address string) int64 {
	t.Helper()
	id, err := testStore.CreateEmailChannel(context.Background(), userID, address, false)
	if err != nil {
		t.Fatalf("addEmail setup: %v", err)
	}
	return id
}

// addWebhook is test helper that inserts webhook channel row.
// Used to set up existing channels for guard tests.
func addWebhook(t *testing.T, userID int64, url string) int64 {
	t.Helper()
	id, err := testStore.CreateWebhookChannel(context.Background(), userID, url, "generic", false, "", "", "", false)
	if err != nil {
		t.Fatalf("addWebhook setup: %v", err)
	}
	return id
}

// ----------------------------------------------------------------
// ---------------------- AddChannel ------------------------------
// ----------------------------------------------------------------

func TestAddChannel_Email_HappyPath(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	result, err := channels.AddChannel(ctx, testStore, userID, "email", "user@example.com", "", false, "", "", "", false, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Type != "Email" {
		t.Errorf("Type = %q, want %q", result.Type, "Email")
	}
	if result.Address != "user@example.com" {
		t.Errorf("Address = %q, want %q", result.Address, "user@example.com")
	}
	if !result.IsEnabled {
		t.Error("IsEnabled should be true for a newly created channel")
	}
	if result.ID <= 0 {
		t.Error("expected positive channel ID")
	}
}

func TestAddChannel_Webhook_HappyPath(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	result, err := channels.AddChannel(ctx, testStore, userID, "webhook", webhookURL, "generic", false, "", "", "", false, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != "Webhook" {
		t.Errorf("Type = %q, want %q", result.Type, "Webhook")
	}
	if result.Address != webhookURL {
		t.Errorf("Address = %q, want %q", result.Address, webhookURL)
	}
	if result.WebhookType != "generic" {
		t.Errorf("WebhookType = %q, want %q", result.WebhookType, "generic")
	}
	if !result.IsEnabled {
		t.Error("IsEnabled should be true for a newly created channel")
	}
	if result.ID <= 0 {
		t.Error("expected positive channel ID")
	}
}

func TestAddChannel_UnknownType(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	_, err := channels.AddChannel(ctx, testStore, userID, "telegram", "someaddress", "", false, "", "", "", false, 0, 0)
	assertError(t, err, true, appError.Invalid)
}

func TestAddChannel_DuplicateEmailAddress(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	addEmail(t, userID, "dup@example.com")

	// adding same email address again should be rejected
	_, err := channels.AddChannel(ctx, testStore, userID, "email", "dup@example.com", "", false, "", "", "", false, 0, 0)
	assertError(t, err, true, appError.Conflict)
}

func TestAddChannel_DuplicateWebhookURL(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	addWebhook(t, userID, webhookURL)

	// adding same webhook URL again should be rejected
	_, err := channels.AddChannel(ctx, testStore, userID, "webhook", webhookURL, "generic", false, "", "", "", false, 0, 0)
	assertError(t, err, true, appError.Conflict)
}

func TestAddChannel_EmailLimitReached(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	// fill up limit of 1
	addEmail(t, userID, "first@example.com")

	// next one should be rejected
	_, err := channels.AddChannel(ctx, testStore, userID, "email", "second@example.com", "", false, "", "", "", false, 0, 1)
	assertError(t, err, true, appError.Conflict)
}

func TestAddChannel_WebhookLimitReached(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	// fill up limit of 1
	addWebhook(t, userID, "https://example.com/hook1")

	// next one should be rejected
	_, err := channels.AddChannel(ctx, testStore, userID, "webhook", "https://example.com/hook2", "generic", false, "", "", "", false, 1, 0)
	assertError(t, err, true, appError.Conflict)
}

func TestAddChannel_LimitZeroMeansUnlimited(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	// maxEmails=0 means no limit - should never block
	for i := range 3 {
		addr := fmt.Sprintf("user%d@example.com", i)
		_, err := channels.AddChannel(ctx, testStore, userID, "email", addr, "", false, "", "", "", false, 0, 0)
		if err != nil {
			t.Errorf("expected no error for channel %d with unlimited quota, got: %v", i, err)
		}
	}
}

// ----------------------------------------------------------------
// ------------------- DeleteChannel ------------------------------
// ----------------------------------------------------------------

func TestDeleteChannel_Success(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	chID := addEmail(t, userID, "todelete@example.com")

	err := channels.DeleteChannel(ctx, testStore, userID, strconv.FormatInt(chID, 10))
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}

	// verify it is actually gone
	_, err = testStore.QueryChannelByID(ctx, chID, userID)
	if err == nil {
		t.Error("channel should be gone after deletion but was still found")
	}
}

func TestDeleteChannel_InvalidID(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	err := channels.DeleteChannel(ctx, testStore, userID, "not-a-number")
	assertError(t, err, true, appError.Invalid)
}

func TestDeleteChannel_NotFound(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	err := channels.DeleteChannel(ctx, testStore, userID, "999999999")
	assertError(t, err, true, appError.NotFound)
}

func TestDeleteChannel_CannotDeleteOtherUsersChannel(t *testing.T) {
	ctx := context.Background()
	owner, _, _ := testutil.CreateTestUser(t, testStore, "user")
	other, _, _ := testutil.CreateTestUser(t, testStore, "user")
	chID := addEmail(t, owner, "owners@example.com")

	// other user tries to delete owner's channel - should get NotFound
	err := channels.DeleteChannel(ctx, testStore, other, strconv.FormatInt(chID, 10))
	assertError(t, err, true, appError.NotFound)
}

// ----------------------------------------------------------------
// -------------------- GetChannels -------------------------------
// ----------------------------------------------------------------

func TestGetChannels_CountsPerType(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	addEmail(t, userID, "a@example.com")
	addEmail(t, userID, "b@example.com")
	addWebhook(t, userID, "https://example.com/wh1")

	summary, err := channels.GetChannels(ctx, testStore, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.EmailCount != 2 {
		t.Errorf("EmailCount = %d, want 2", summary.EmailCount)
	}
	if summary.WebhookCount != 1 {
		t.Errorf("WebhookCount = %d, want 1", summary.WebhookCount)
	}
	if len(summary.Channels) != 3 {
		t.Errorf("total channels = %d, want 3", len(summary.Channels))
	}
}

func TestGetChannels_Empty(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	summary, err := channels.GetChannels(ctx, testStore, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Channels) != 0 || summary.EmailCount != 0 || summary.WebhookCount != 0 {
		t.Errorf("expected empty summary, got: %+v", summary)
	}
}

// ----------------------------------------------------------------
// ------------------- ToggleEnabled ------------------------------
// ----------------------------------------------------------------

func TestToggleEnabled(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	chID := addEmail(t, userID, "toggle@example.com")

	// disable
	result, err := channels.ToggleEnabled(ctx, testStore, userID, chID, false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if result.IsEnabled {
		t.Error("expected IsEnabled=false after disabling")
	}

	// re-enable
	result, err = channels.ToggleEnabled(ctx, testStore, userID, chID, true)
	if err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if !result.IsEnabled {
		t.Error("expected IsEnabled=true after re-enabling")
	}
}

// ----------------------------------------------------------------
// --------------- ToggleNotifyOnManualVerify ---------------------
// ----------------------------------------------------------------

func TestToggleNotifyOnManualVerify(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	chID := addEmail(t, userID, "notifytoggle@example.com")

	// turn on
	result, err := channels.ToggleNotifyOnManualVerify(ctx, testStore, userID, chID, true)
	if err != nil {
		t.Fatalf("toggle on: %v", err)
	}
	if !result.NotifyOnManualVerify {
		t.Error("expected NotifyOnManualVerify=true after toggling on")
	}

	// turn off
	result, err = channels.ToggleNotifyOnManualVerify(ctx, testStore, userID, chID, false)
	if err != nil {
		t.Fatalf("toggle off: %v", err)
	}
	if result.NotifyOnManualVerify {
		t.Error("expected NotifyOnManualVerify=false after toggling off")
	}
}

// assertError checks that err matches expectation.
//   - wantErr is true -> verifies an error was returned and its cause matches wantCause
//   - wantErr is false -> verifies no error was returned
func assertError(t *testing.T, err error, wantErr bool, wantCause appError.Cause) {
	t.Helper()
	if wantErr {
		if err == nil {
			t.Fatal("expected an error but got nil")
		}
		got := appError.ErrorCause(err)
		if got != wantCause {
			t.Errorf("error cause = %v, want %v  (full error: %v)", got, wantCause, err)
		}
	} else {
		if err != nil {
			t.Errorf("expected no error but got: %v", err)
		}
	}
}
