// Package packages_test contains integration tests for the packages app layer.
//
// What is not tested and why:
//
//   - Track, Check, WatchCheck - These functions require Checker with workers that call nix binary
//     to evaluate package version. Testing them would require nix installation and non-deterministic goroutine timing.
//     Nix evaluation can take long time which is not suitable for tests.
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

// ----------------------------------------------------------------
// ------------------- GetTrackStatus -----------------------------
// ----------------------------------------------------------------

func TestGetTrackStatus_InvalidID(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	_, err := packages.GetTrackStatus(ctx, testStore, userID, "not-a-number")
	assertError(t, err, true, appError.Invalid)
}

func TestGetTrackStatus_NotDone(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	pkgID := addTracking(t, userID)

	// no result in map - goroutine hasn't finished yet
	status, err := packages.GetTrackStatus(ctx, testStore, userID, strconv.FormatInt(pkgID, 10))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Done {
		t.Error("expected Done=false when result not in map yet")
	}
}

func TestGetTrackStatus_DoneWithFailure(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	pkgID := addTracking(t, userID)

	// simulate goroutine finishing with failure
	packages.SetOperationResult(userID, pkgID, true, true, "Invalid package name or branch", "badpkg", "nixpkgs-unstable")

	status, err := packages.GetTrackStatus(ctx, testStore, userID, strconv.FormatInt(pkgID, 10))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Done {
		t.Error("expected Done=true")
	}
	if !status.Failed {
		t.Error("expected Failed=true")
	}
	if !status.Watchable {
		t.Error("expected Watchable=true")
	}
	if status.ErrMsg != "Invalid package name or branch" {
		t.Errorf("ErrMsg = %q, want %q", status.ErrMsg, "Invalid package name or branch")
	}
}

func TestGetTrackStatus_DoneWithSuccess(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	pkgID := addTracking(t, userID)

	// simulate goroutine finishing successfully
	packages.SetOperationResult(userID, pkgID, false, false, "", "", "")

	status, err := packages.GetTrackStatus(ctx, testStore, userID, strconv.FormatInt(pkgID, 10))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Done {
		t.Error("expected Done=true")
	}
	if status.Failed {
		t.Error("expected Failed=false")
	}
	if status.Package.PackageID != pkgID {
		t.Errorf("Package.PackageID = %d, want %d", status.Package.PackageID, pkgID)
	}
}

// ----------------------------------------------------------------
// ------------------- GetCheckStatus -----------------------------
// ----------------------------------------------------------------

func TestGetCheckStatus_InvalidID(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	_, err := packages.GetCheckStatus(ctx, testStore, userID, "not-a-number", "1.0.0")
	assertError(t, err, true, appError.Invalid)
}

func TestGetCheckStatus_NotDone(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	pkgID := addTracking(t, userID)

	// no result in map - goroutine hasn't finished yet
	status, err := packages.GetCheckStatus(ctx, testStore, userID, strconv.FormatInt(pkgID, 10), "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Done {
		t.Error("expected Done=false when result not in map yet")
	}
	if status.Prev != "1.0.0" {
		t.Errorf("Prev = %q, want %q", status.Prev, "1.0.0")
	}
}

func TestGetCheckStatus_DoneWithFailure(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	pkgID := addTracking(t, userID)

	// simulate goroutine finishing with failure
	packages.SetOperationResult(userID, pkgID, true, false, "Nix evaluation failed - try again later", "", "")

	status, err := packages.GetCheckStatus(ctx, testStore, userID, strconv.FormatInt(pkgID, 10), "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Done {
		t.Error("expected Done=true")
	}
	if !status.Failed {
		t.Error("expected Failed=true")
	}
	if status.ErrMsg != "Nix evaluation failed - try again later" {
		t.Errorf("ErrMsg = %q, want %q", status.ErrMsg, "Nix evaluation failed - try again later")
	}
}

func TestGetCheckStatus_DoneVersionChanged(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	// insert package with version 2.0.0 (simulates goroutine having updated it)
	name := fmt.Sprintf("testpkg-%d", testutil.NextID())
	pkgID, err := testStore.StorePackage(ctx, name, "nixpkgs-unstable", "2.0.0")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := testStore.StoreTracking(ctx, userID, pkgID, "2.0.0"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// simulate goroutine finishing successfully
	packages.SetOperationResult(userID, pkgID, false, false, "", "", "")

	// prev was 1.0.0, DB now has 2.0.0 -> version changed
	status, err := packages.GetCheckStatus(ctx, testStore, userID, strconv.FormatInt(pkgID, 10), "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Done {
		t.Error("expected Done=true")
	}
	if !status.VersionChanged {
		t.Error("expected VersionChanged=true when prev differs from current LastNotifiedVersion")
	}
}

func TestGetCheckStatus_DoneVersionUnchanged(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	pkgID := addTracking(t, userID) // version is 1.0.0

	// simulate goroutine finishing successfully
	packages.SetOperationResult(userID, pkgID, false, false, "", "", "")

	// prev matches current version -> no change
	status, err := packages.GetCheckStatus(ctx, testStore, userID, strconv.FormatInt(pkgID, 10), "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Done {
		t.Error("expected Done=true")
	}
	if status.VersionChanged {
		t.Error("expected VersionChanged=false when prev matches current LastNotifiedVersion")
	}
}

// ----------------------------------------------------------------
// ----------------- GetWatchCheckStatus --------------------------
// ----------------------------------------------------------------

func TestGetWatchCheckStatus_InvalidID(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	_, err := packages.GetWatchCheckStatus(ctx, testStore, userID, "not-a-number")
	assertError(t, err, true, appError.Invalid)
}

func TestGetWatchCheckStatus_NotDone(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	entry := addWatchlistEntry(t, userID)

	// no result in map - goroutine hasn't finished yet
	status, err := packages.GetWatchCheckStatus(ctx, testStore, userID, strconv.FormatInt(entry.ID, 10))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Done {
		t.Error("expected Done=false when result not in map yet")
	}
	if status.Entry.ID != entry.ID {
		t.Errorf("Entry.ID = %d, want %d", status.Entry.ID, entry.ID)
	}
}

func TestGetWatchCheckStatus_StillNotFound(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	entry := addWatchlistEntry(t, userID)

	// simulate goroutine: package still not in nixpkgs
	packages.SetWatchlistCheckResult(userID, entry.ID, false, false, true, "", database.TrackedPackage{})

	status, err := packages.GetWatchCheckStatus(ctx, testStore, userID, strconv.FormatInt(entry.ID, 10))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Done {
		t.Error("expected Done=true")
	}
	if !status.StillNotFound {
		t.Error("expected StillNotFound=true")
	}
}

func TestGetWatchCheckStatus_Failed(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	entry := addWatchlistEntry(t, userID)

	// simulate goroutine: nix eval failed
	packages.SetWatchlistCheckResult(userID, entry.ID, true, false, false, "Nix evaluation failed - try again later", database.TrackedPackage{})

	status, err := packages.GetWatchCheckStatus(ctx, testStore, userID, strconv.FormatInt(entry.ID, 10))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Done {
		t.Error("expected Done=true")
	}
	if !status.Failed {
		t.Error("expected Failed=true")
	}
	if status.ErrMsg != "Nix evaluation failed - try again later" {
		t.Errorf("ErrMsg = %q, want %q", status.ErrMsg, "Nix evaluation failed - try again later")
	}
}

func TestGetWatchCheckStatus_Promoted(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	entry := addWatchlistEntry(t, userID)

	promotedPkg := database.TrackedPackage{
		PackageID:           999,
		Name:                entry.Name,
		Branch:              entry.Branch,
		LastNotifiedVersion: "1.0.0",
	}

	// simulate goroutine: package appeared in nixpkgs and was promoted
	packages.SetWatchlistCheckResult(userID, entry.ID, false, true, false, "", promotedPkg)

	status, err := packages.GetWatchCheckStatus(ctx, testStore, userID, strconv.FormatInt(entry.ID, 10))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Done {
		t.Error("expected Done=true")
	}
	if !status.Promoted {
		t.Error("expected Promoted=true")
	}
	if status.PromotedPkg.LastNotifiedVersion != "1.0.0" {
		t.Errorf("PromotedPkg.LastNotifiedVersion = %q, want %q", status.PromotedPkg.LastNotifiedVersion, "1.0.0")
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
