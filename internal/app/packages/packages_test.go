// Package packages_test contains integration tests for the packages app layer.
//
// What is not tested and why:
//
//   - Track, Check, WatchCheck - These functions require Checker and they launch goroutine that calls nix binary
//     to evaluate package version. Testing them would require nix installation and non-deterministic goroutine timing.
//
//   - GetTrackStatus, GetCheckStatus, GetWatchCheckStatus - These poll in-memory sync.Map that is populated by
//     goroutines launched by Track, Check and WatchCheck.
//
//   - StartResultCleanup - Background goroutine with 60 minute ticker - not meaningful for test...
package packages_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/packages"
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

// addTracking sets up package + tracking row.
// Used as test setup for Untrack and GetTrackedPackages tests.
func addTracking(t *testing.T, userID int64) (packageID int64) {
	t.Helper()
	name := fmt.Sprintf("testpkg-%d", testutil.NextID())
	id, err := testStore.StorePackage(context.Background(), name, "nixpkgs-unstable", "1.0.0")
	if err != nil {
		t.Fatalf("addTracking: StorePackage: %v", err)
	}
	err = testStore.StoreTracking(context.Background(), userID, id, "1.0.0")
	if err != nil {
		t.Fatalf("addTracking: StoreTracking: %v", err)
	}
	return id
}

// addWatchlistEntry adds watchlist entry row.
// Used as test setup for Unwatch and GetWatchedPackages tests.
func addWatchlistEntry(t *testing.T, userID int64) database.WatchlistEntry {
	t.Helper()
	name := fmt.Sprintf("watchpkg-%d", testutil.NextID())
	entry, err := testStore.CreateWatchlistEntry(context.Background(), userID, name, "nixpkgs-unstable")
	if err != nil {
		t.Fatalf("addWatchlistEntry: %v", err)
	}
	return entry
}

// ----------------------------------------------------------------
// ----------------------- Watch ----------------------------------
// ----------------------------------------------------------------

func TestWatch_HappyPath(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	entry, err := packages.Watch(ctx, testStore, userID, "firefox", "nixpkgs-unstable")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Name != "firefox" {
		t.Errorf("Name = %q, want %q", entry.Name, "firefox")
	}
	if entry.Branch != "nixpkgs-unstable" {
		t.Errorf("Branch = %q, want %q", entry.Branch, "nixpkgs-unstable")
	}
	if entry.ID <= 0 {
		t.Error("expected positive watchlist entry ID")
	}
}

func TestWatch_Duplicate(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	_, err := packages.Watch(ctx, testStore, userID, "vim", "nixpkgs-unstable")
	if err != nil {
		t.Fatalf("first watch: %v", err)
	}

	// watching the same package+branch again
	_, err = packages.Watch(ctx, testStore, userID, "vim", "nixpkgs-unstable")
	assertError(t, err, true, appError.Conflict)
}

// ----------------------------------------------------------------
// --------------------- Unwatch ----------------------------------
// ----------------------------------------------------------------

func TestUnwatch_Success(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	entry := addWatchlistEntry(t, userID)

	err := packages.Unwatch(ctx, testStore, userID, strconv.FormatInt(entry.ID, 10))
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestUnwatch_InvalidID(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	err := packages.Unwatch(ctx, testStore, userID, "not-a-number")
	assertError(t, err, true, appError.Invalid)
}

func TestUnwatch_NotFound(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	err := packages.Unwatch(ctx, testStore, userID, "999999999")
	assertError(t, err, true, appError.NotFound)
}

func TestUnwatch_CannotUnwatchOtherUsersEntry(t *testing.T) {
	ctx := context.Background()
	owner, _, _ := testutil.CreateTestUser(t, testStore, "user")
	other, _, _ := testutil.CreateTestUser(t, testStore, "user")
	entry := addWatchlistEntry(t, owner)

	// other user tries to unwatch owner's entry - DB scopes by userID so it should return NotFound
	err := packages.Unwatch(ctx, testStore, other, strconv.FormatInt(entry.ID, 10))
	assertError(t, err, true, appError.NotFound)
}

// ----------------------------------------------------------------
// ----------------- GetWatchedPackages ---------------------------
// ----------------------------------------------------------------

func TestGetWatchedPackages_Empty(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	entries, err := packages.GetWatchedPackages(ctx, testStore, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty watchlist, got %d entries", len(entries))
	}
}

func TestGetWatchedPackages_ReturnsUsersEntries(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	addWatchlistEntry(t, userID)
	addWatchlistEntry(t, userID)

	entries, err := packages.GetWatchedPackages(ctx, testStore, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 watchlist entries, got %d", len(entries))
	}
}

func TestGetWatchedPackages_IsolatedPerUser(t *testing.T) {
	ctx := context.Background()
	user1, _, _ := testutil.CreateTestUser(t, testStore, "user")
	user2, _, _ := testutil.CreateTestUser(t, testStore, "user")
	addWatchlistEntry(t, user1)

	// user2 should see their own empty watchlist and not user1's entries
	entries, err := packages.GetWatchedPackages(ctx, testStore, user2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected user2 to have empty watchlist, got %d entries", len(entries))
	}
}

// ----------------------------------------------------------------
// ------------------------ Untrack -------------------------------
// ----------------------------------------------------------------

func TestUntrack_Success(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	pkgID := addTracking(t, userID)

	err := packages.Untrack(ctx, testStore, userID, strconv.FormatInt(pkgID, 10))
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestUntrack_InvalidID(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	err := packages.Untrack(ctx, testStore, userID, "not-a-number")
	assertError(t, err, true, appError.Invalid)
}

func TestUntrack_NotFound(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	err := packages.Untrack(ctx, testStore, userID, "999999999")
	assertError(t, err, true, appError.NotFound)
}

func TestUntrack_CannotUntrackOtherUsersPackage(t *testing.T) {
	ctx := context.Background()
	owner, _, _ := testutil.CreateTestUser(t, testStore, "user")
	other, _, _ := testutil.CreateTestUser(t, testStore, "user")
	pkgID := addTracking(t, owner)

	err := packages.Untrack(ctx, testStore, other, strconv.FormatInt(pkgID, 10))
	assertError(t, err, true, appError.NotFound)
}

// ----------------------------------------------------------------
// ------------------- GetTrackedPackages -------------------------
// ----------------------------------------------------------------

func TestGetTrackedPackages_Empty(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	tracked, err := packages.GetTrackedPackages(ctx, testStore, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracked) != 0 {
		t.Errorf("expected empty list, got %d packages", len(tracked))
	}
}

func TestGetTrackedPackages_ReturnsUsersPackages(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	addTracking(t, userID)
	addTracking(t, userID)

	tracked, err := packages.GetTrackedPackages(ctx, testStore, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracked) != 2 {
		t.Errorf("expected 2 tracked packages, got %d", len(tracked))
	}
}

func TestGetTrackedPackages_IsolatedPerUser(t *testing.T) {
	ctx := context.Background()
	user1, _, _ := testutil.CreateTestUser(t, testStore, "user")
	user2, _, _ := testutil.CreateTestUser(t, testStore, "user")
	addTracking(t, user1)

	// user2 should see their own empty list and not user1's packages
	tracked, err := packages.GetTrackedPackages(ctx, testStore, user2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracked) != 0 {
		t.Errorf("expected user2 to have no tracked packages, got %d", len(tracked))
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
