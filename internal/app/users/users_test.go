// Package users_test contains integration tests for the users app layer.
package users_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/users"
	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
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

// ----------------------------------------------------------------
// ---------------------- GetUserByID -----------------------------
// ----------------------------------------------------------------

func TestGetUserByID_Success(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	u, err := users.GetUserByID(ctx, testStore, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.ID != userID {
		t.Errorf("ID = %d, want %d", u.ID, userID)
	}
}

func TestGetUserByID_NotFound(t *testing.T) {
	ctx := context.Background()

	_, err := users.GetUserByID(ctx, testStore, 999999999)
	assertError(t, err, true, appError.NotFound)
}

// ----------------------------------------------------------------
// ------------------------- GetUser ------------------------------
// ----------------------------------------------------------------

func TestGetUser_ExistingAccount_ReturnsExistingUserID(t *testing.T) {
	ctx := context.Background()
	userID, issuer, subject := testutil.CreateTestUser(t, testStore, "user")

	provider := &auth.Provider{Issuer: issuer}
	claims := auth.UserClaims{Subject: subject}

	got, err := users.GetUser(ctx, testStore, provider, claims)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != userID {
		t.Errorf("userID = %d, want %d", got, userID)
	}
}

func TestGetUser_NewAccount_CreatesUser(t *testing.T) {
	ctx := context.Background()

	provider := &auth.Provider{Issuer: "https://test.issuer", Name: "test"}
	claims := auth.UserClaims{
		Subject:           fmt.Sprintf("new-sub-%d", testutil.NextID()),
		PreferredUsername: fmt.Sprintf("newuser%d", testutil.NextID()),
	}

	got, err := users.GetUser(ctx, testStore, provider, claims)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got <= 0 {
		t.Errorf("expected positive userID for new account, got %d", got)
	}
}

// ----------------------------------------------------------------
// --------------------- UpdateUsername ---------------------------
// ----------------------------------------------------------------
func TestUpdateUsername(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	tests := []struct {
		name      string
		username  string
		wantCause appError.Cause
		wantErr   bool
	}{
		{
			name:      "empty string",
			username:  "",
			wantErr:   true,
			wantCause: appError.Invalid,
		},
		{
			name:      "whitespace only",
			username:  "   ",
			wantErr:   true,
			wantCause: appError.Invalid,
		},
		{
			name:      "too long",
			username:  strings.Repeat("a", 51),
			wantErr:   true,
			wantCause: appError.Invalid,
		},
		{
			name:      "invalid characters",
			username:  "bad@name",
			wantErr:   true,
			wantCause: appError.Invalid,
		},
		{
			name:     "success",
			username: "validname",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := users.UpdateUsername(ctx, testStore, userID, tt.username)
			assertError(t, err, tt.wantErr, tt.wantCause)
		})
	}
}

func TestUpdateUsername_Conflict(t *testing.T) {
	ctx := context.Background()
	user1ID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	user2ID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	u2, err := testStore.QueryUserByID(ctx, user2ID)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	err = users.UpdateUsername(ctx, testStore, user1ID, u2.Username)
	assertError(t, err, true, appError.Conflict)
}

// ----------------------------------------------------------------
// ------------------ UpdateUsernameAndRole -----------------------
// ----------------------------------------------------------------
func TestUpdateUsernameAndRole(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	tests := []struct {
		name      string
		username  string
		role      string
		wantCause appError.Cause
		wantErr   bool
	}{
		{
			name:      "empty username",
			username:  "",
			role:      "user",
			wantErr:   true,
			wantCause: appError.Invalid,
		},
		{
			name:      "username too long",
			username:  strings.Repeat("a", 51),
			role:      "user",
			wantErr:   true,
			wantCause: appError.Invalid,
		},
		{
			name:      "invalid characters in username",
			username:  "bad@name",
			role:      "user",
			wantErr:   true,
			wantCause: appError.Invalid,
		},
		{
			name:      "invalid role",
			username:  fmt.Sprintf("user%d", testutil.NextID()),
			role:      "superadmin",
			wantErr:   true,
			wantCause: appError.Invalid,
		},
		{
			name:     "success",
			username: fmt.Sprintf("user%d", testutil.NextID()),
			role:     "admin",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := users.UpdateUsernameAndRole(ctx, testStore, userID, tt.username, tt.role)
			assertError(t, err, tt.wantErr, tt.wantCause)
		})
	}
}

func TestUpdateUsernameAndRole_UserNotFound(t *testing.T) {
	_, err := users.UpdateUsernameAndRole(context.Background(), testStore, 999999999, "anyname", "user")
	assertError(t, err, true, appError.NotFound)
}

func TestUpdateUsernameAndRole_Conflict(t *testing.T) {
	ctx := context.Background()
	user1ID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	user2ID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	u2, err := testStore.QueryUserByID(ctx, user2ID)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err = users.UpdateUsernameAndRole(ctx, testStore, user1ID, u2.Username, "user")
	assertError(t, err, true, appError.Conflict)
}

// ----------------------------------------------------------------
// ----------------------- GetAccounts ----------------------------
// ----------------------------------------------------------------

func TestGetAccounts_OneAccount_CannotUnlink(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	summary, err := users.GetAccounts(ctx, testStore, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Accounts) != 1 {
		t.Errorf("expected 1 account, got %d", len(summary.Accounts))
	}
	if summary.CanUnlink {
		t.Error("CanUnlink should be false when user has only one account")
	}
}

func TestGetAccounts_TwoAccounts_CanUnlink(t *testing.T) {
	ctx := context.Background()
	userID, issuer, _ := testutil.CreateTestUser(t, testStore, "user")

	secondSub := fmt.Sprintf("second-sub-%d", testutil.NextID())
	err := testStore.CreateLinkedAccount(ctx, userID, nil, false, "test", issuer, secondSub)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	summary, err := users.GetAccounts(ctx, testStore, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Accounts) != 2 {
		t.Errorf("expected 2 accounts, got %d", len(summary.Accounts))
	}
	if !summary.CanUnlink {
		t.Error("CanUnlink should be true when user has more than one account")
	}
}

// ----------------------------------------------------------------
// --------------------- UnlinkAccount ----------------------------
// ----------------------------------------------------------------
func TestUnlinkAccount_LastAccount_Refused(t *testing.T) {
	ctx := context.Background()
	userID, issuer, subject := testutil.CreateTestUser(t, testStore, "user")

	err := users.UnlinkAccount(ctx, testStore, userID, issuer, subject)
	assertError(t, err, true, appError.Conflict)
}

func TestUnlinkAccount_SecondAccount_Succeeds(t *testing.T) {
	ctx := context.Background()
	userID, issuer, _ := testutil.CreateTestUser(t, testStore, "user")

	secondSub := fmt.Sprintf("second-sub-%d", testutil.NextID())
	err := testStore.CreateLinkedAccount(ctx, userID, nil, false, "test", issuer, secondSub)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	err = users.UnlinkAccount(ctx, testStore, userID, issuer, secondSub)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// ----------------------------------------------------------------
// ---------------- LinkExistingAccount ---------------------------
// ----------------------------------------------------------------

func TestLinkExistingAccount_AccountNotFound(t *testing.T) {
	ctx := context.Background()
	targetID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	provider := &auth.Provider{Issuer: "https://test.issuer"}
	claims := auth.UserClaims{Subject: "no-such-subject"}

	_, err := users.LinkExistingAccount(ctx, testStore, provider, claims, targetID)
	assertError(t, err, true, appError.NotFound)
}

func TestLinkExistingAccount_AccountAlreadyOwnedByTarget(t *testing.T) {
	ctx := context.Background()
	userID, issuer, subject := testutil.CreateTestUser(t, testStore, "user")

	provider := &auth.Provider{Issuer: issuer}
	claims := auth.UserClaims{Subject: subject}

	_, err := users.LinkExistingAccount(ctx, testStore, provider, claims, userID)
	assertError(t, err, true, appError.Conflict)
}

func TestLinkExistingAccount_SourceAdminOrphaned_PromotesTarget(t *testing.T) {
	ctx := context.Background()
	sourceID, issuer, subject := testutil.CreateTestUser(t, testStore, "admin")
	targetID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	provider := &auth.Provider{Issuer: issuer}
	claims := auth.UserClaims{Subject: subject}

	role, err := users.LinkExistingAccount(ctx, testStore, provider, claims, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if role != "admin" {
		t.Errorf("expected role=admin after promotion, got %q", role)
	}
	_ = sourceID
}

func TestLinkExistingAccount_SourceUserOrphaned_NoPromotion(t *testing.T) {
	ctx := context.Background()
	_, issuer, subject := testutil.CreateTestUser(t, testStore, "user")
	targetID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	provider := &auth.Provider{Issuer: issuer}
	claims := auth.UserClaims{Subject: subject}

	role, err := users.LinkExistingAccount(ctx, testStore, provider, claims, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if role != "user" {
		t.Errorf("expected role=user (no promotion), got %q", role)
	}
}

func TestLinkExistingAccount_SourceHasMultipleAccounts_NoPromotion(t *testing.T) {
	ctx := context.Background()
	sourceID, issuer, _ := testutil.CreateTestUser(t, testStore, "admin")
	targetID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	// give source second account so it won't be orphaned when first account moves
	secondSub := fmt.Sprintf("second-sub-%d", testutil.NextID())
	err := testStore.CreateLinkedAccount(ctx, sourceID, nil, false, "test", issuer, secondSub)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	// move the second account to target
	provider := &auth.Provider{Issuer: issuer}
	claims := auth.UserClaims{Subject: secondSub}

	role, err := users.LinkExistingAccount(ctx, testStore, provider, claims, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if role != "user" {
		t.Errorf("expected role=user (source not orphaned), got %q", role)
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
