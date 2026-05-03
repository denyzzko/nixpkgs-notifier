// Package packages_test contains integration tests for the packages app layer.
// StartBackgroundCleanup is not tested because it is background goroutine with a ticker
// that cleans stale operationResults entries and expired check_state rows - not meaningful for integration tests.
package packages_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/packages"
	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
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

// addWatchlistEntry adds watchlist entry via Watch business function.
// Used as test setup for Unwatch and GetWatchedPackages tests.
func addWatchlistEntry(t *testing.T, userID int64) database.WatchedPackage {
	t.Helper()
	name := fmt.Sprintf("watchpkg-%d", testutil.NextID())
	wp, err := packages.Watch(context.Background(), testStore, userID, name, "nixpkgs-unstable")
	if err != nil {
		t.Fatalf("addWatchlistEntry: %v", err)
	}
	return wp
}

// fakeNix returns nix eval function that always returns specified version or error.
func fakeNix(version string, err error) func(ctx context.Context, name, branch string) (string, error) {
	return func(_ context.Context, _, _ string) (string, error) {
		return version, err
	}
}

// startChecker creates and starts a Checker with fakeNixFn.
// Checker is automatically stopped when the test ends.
func startChecker(t *testing.T, version string, nixErr error) *checker.Checker {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := checker.NewWithNixEval(testStore, checker.Config{WorkerCount: 1, Interval: time.Minute}, fakeNix(version, nixErr))
	c.Start(ctx)
	return c
}

// pollTrackStatus polls GetTrackStatus until Done=true or 1s timeout.
func pollTrackStatus(t *testing.T, userID, pkgID int64) packages.TrackStatus {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, err := packages.GetTrackStatus(context.Background(), testStore, userID, strconv.FormatInt(pkgID, 10))
		if err != nil {
			t.Fatalf("pollTrackStatus: %v", err)
		}
		if status.Done {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pollTrackStatus: timed out waiting for Done=true")
	return packages.TrackStatus{}
}

// pollCheckStatus polls GetCheckStatus until Done=true or 1s timeout.
func pollCheckStatus(t *testing.T, userID, pkgID int64, prev string) packages.TrackingCheckStatus {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, err := packages.GetCheckStatus(context.Background(), testStore, userID, strconv.FormatInt(pkgID, 10), prev)
		if err != nil {
			t.Fatalf("pollCheckStatus: %v", err)
		}
		if status.Done {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pollCheckStatus: timed out waiting for Done=true")
	return packages.TrackingCheckStatus{}
}

// pollWatchCheckStatus polls GetWatchCheckStatus until Done=true or 1s timeout.
func pollWatchCheckStatus(t *testing.T, userID, watchlistID int64) packages.WatchlistCheckStatus {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, err := packages.GetWatchCheckStatus(context.Background(), testStore, userID, strconv.FormatInt(watchlistID, 10))
		if err != nil {
			t.Fatalf("pollWatchCheckStatus: %v", err)
		}
		if status.Done {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pollWatchCheckStatus: timed out waiting for Done=true")
	return packages.WatchlistCheckStatus{}
}

// ----------------------------------------------------------------
// ----------------------- Watch ----------------------------------
// ----------------------------------------------------------------

func TestWatch_HappyPath(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	wp, err := packages.Watch(ctx, testStore, userID, "firefox", "nixpkgs-unstable")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wp.Name != "firefox" {
		t.Errorf("Name = %q, want %q", wp.Name, "firefox")
	}
	if wp.Branch != "nixpkgs-unstable" {
		t.Errorf("Branch = %q, want %q", wp.Branch, "nixpkgs-unstable")
	}
	if wp.WatchlistID <= 0 {
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

	err := packages.Unwatch(ctx, testStore, userID, strconv.FormatInt(entry.WatchlistID, 10))
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
	err := packages.Unwatch(ctx, testStore, other, strconv.FormatInt(entry.WatchlistID, 10))
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

	// no check_state row - goroutine hasn't finished yet
	status, err := packages.GetCheckStatus(ctx, testStore, userID, strconv.FormatInt(pkgID, 10), "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Done {
		t.Error("expected Done=false when no check_state row exists")
	}
	if status.Prev != "1.0.0" {
		t.Errorf("Prev = %q, want %q", status.Prev, "1.0.0")
	}
}

func TestGetCheckStatus_DoneWithFailure(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	pkgID := addTracking(t, userID)

	// simulate goroutine finishing with failure via DB check_state
	oldVer := "1.0.0"
	err := testStore.InsertCheckState(ctx, userID, pkgID, &oldVer)
	if err != nil {
		t.Fatalf("InsertCheckState: %v", err)
	}
	err = testStore.UpdateCheckStateFailed(ctx, userID, pkgID, "Nix evaluation failed - try again later")
	if err != nil {
		t.Fatalf("UpdateCheckStateFailed: %v", err)
	}

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
	err = testStore.StoreTracking(ctx, userID, pkgID, "2.0.0")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	// simulate goroutine finishing successfully via DB check_state
	// old_version=1.0.0 (prev), new_version=2.0.0 (changed)
	oldVer := "1.0.0"
	newVer := "2.0.0"
	err = testStore.InsertCheckState(ctx, userID, pkgID, &oldVer)
	if err != nil {
		t.Fatalf("InsertCheckState: %v", err)
	}
	err = testStore.UpdateCheckStateDone(ctx, userID, pkgID, &newVer)
	if err != nil {
		t.Fatalf("UpdateCheckStateDone: %v", err)
	}

	status, err := packages.GetCheckStatus(ctx, testStore, userID, strconv.FormatInt(pkgID, 10), "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Done {
		t.Error("expected Done=true")
	}
	if !status.VersionChanged {
		t.Error("expected VersionChanged=true when new_version set in check_state")
	}
}

func TestGetCheckStatus_DoneVersionUnchanged(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	pkgID := addTracking(t, userID) // version is 1.0.0

	// simulate goroutine finishing successfully via DB check_state
	// new_version nil = no change
	oldVer := "1.0.0"
	err := testStore.InsertCheckState(ctx, userID, pkgID, &oldVer)
	if err != nil {
		t.Fatalf("InsertCheckState: %v", err)
	}
	err = testStore.UpdateCheckStateDone(ctx, userID, pkgID, nil)
	if err != nil {
		t.Fatalf("UpdateCheckStateDone: %v", err)
	}

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

	// no check_state row - goroutine hasn't finished yet
	status, err := packages.GetWatchCheckStatus(ctx, testStore, userID, strconv.FormatInt(entry.WatchlistID, 10))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Done {
		t.Error("expected Done=false when no check_state row exists")
	}
	if status.Entry.WatchlistID != entry.WatchlistID {
		t.Errorf("Entry.WatchlistID = %d, want %d", status.Entry.WatchlistID, entry.WatchlistID)
	}
}

func TestGetWatchCheckStatus_StillNotFound(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	entry := addWatchlistEntry(t, userID)

	// simulate goroutine: write not_found to check_state
	err := testStore.InsertCheckState(ctx, userID, entry.PackageID, nil)
	if err != nil {
		t.Fatalf("InsertCheckState: %v", err)
	}
	err = testStore.UpdateCheckStateNotFound(ctx, userID, entry.PackageID)
	if err != nil {
		t.Fatalf("UpdateCheckStateNotFound: %v", err)
	}

	status, err := packages.GetWatchCheckStatus(ctx, testStore, userID, strconv.FormatInt(entry.WatchlistID, 10))
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

	// simulate goroutine: write failed to check_state
	err := testStore.InsertCheckState(ctx, userID, entry.PackageID, nil)
	if err != nil {
		t.Fatalf("InsertCheckState: %v", err)
	}
	err = testStore.UpdateCheckStateFailed(ctx, userID, entry.PackageID, "Nix evaluation failed - try again later")
	if err != nil {
		t.Fatalf("UpdateCheckStateFailed: %v", err)
	}

	status, err := packages.GetWatchCheckStatus(ctx, testStore, userID, strconv.FormatInt(entry.WatchlistID, 10))
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

	// simulate promotion: delete the watchlist entry (PromoteWatchlistEntries removes it)
	// GetWatchCheckStatus detects promotion by finding ErrNotFound on the watchlist row
	_, err := testStore.DeleteWatchlistEntry(ctx, entry.WatchlistID, userID)
	if err != nil {
		t.Fatalf("DeleteWatchlistEntry: %v", err)
	}

	status, err := packages.GetWatchCheckStatus(ctx, testStore, userID, strconv.FormatInt(entry.WatchlistID, 10))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Done {
		t.Error("expected Done=true")
	}
	if !status.Promoted {
		t.Error("expected Promoted=true")
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

// ----------------------------------------------------------------
// -------------------------- Track -------------------------------
// ----------------------------------------------------------------

// TestTrack_HappyPath verifies that Track creates a tracking row, launches the
// goroutine that evaluates the version baseline, and GetTrackStatus eventually
// returns Done with LastNotifiedVersion set.
func TestTrack_HappyPath(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	chk := startChecker(t, "1.0.0", nil)

	pkgName := fmt.Sprintf("testpkg-%d", testutil.NextID())
	pkg, err := packages.Track(ctx, testStore, userID, chk, pkgName, "nixpkgs-unstable")
	if err != nil {
		t.Fatalf("Track: unexpected error: %v", err)
	}

	status := pollTrackStatus(t, userID, pkg.PackageID)
	if status.Failed {
		t.Fatalf("expected success, got failure: %s", status.ErrMsg)
	}
	if status.Package.LastNotifiedVersion != "1.0.0" {
		t.Errorf("LastNotifiedVersion = %q, want %q", status.Package.LastNotifiedVersion, "1.0.0")
	}
}

// TestTrack_AlreadyTracking verifies that tracking the same package twice
// returns Invalid error.
func TestTrack_AlreadyTracking(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	chk := startChecker(t, "1.0.0", nil)

	pkgName := fmt.Sprintf("testpkg-%d", testutil.NextID())
	pkgID, err := testStore.StorePackage(ctx, pkgName, "nixpkgs-unstable", "1.0.0")
	if err != nil {
		t.Fatalf("setup StorePackage: %v", err)
	}
	err = testStore.StoreTracking(ctx, userID, pkgID, "1.0.0")
	if err != nil {
		t.Fatalf("setup StoreTracking: %v", err)
	}

	_, err = packages.Track(ctx, testStore, userID, chk, pkgName, "nixpkgs-unstable")
	assertError(t, err, true, appError.Invalid)
}

// TestTrack_NixAttrNotFound verifies that when nix returns ErrAttrNotFound,
// the goroutine rolls back tracking and GetTrackStatus reports Failed+Watchable.
func TestTrack_NixAttrNotFound(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	chk := startChecker(t, "", nix.ErrAttrNotFound)

	pkgName := fmt.Sprintf("testpkg-%d", testutil.NextID())
	pkg, err := packages.Track(ctx, testStore, userID, chk, pkgName, "nixpkgs-unstable")
	if err != nil {
		t.Fatalf("Track: unexpected error: %v", err)
	}

	status := pollTrackStatus(t, userID, pkg.PackageID)
	if !status.Failed {
		t.Error("expected Failed=true")
	}
	if !status.Watchable {
		t.Error("expected Watchable=true for ErrAttrNotFound")
	}

	// tracking should be rolled back
	tracked, err := testStore.QueryUsersTrackedPackages(ctx, userID)
	if err != nil {
		t.Fatalf("QueryUsersTrackedPackages: %v", err)
	}
	if len(tracked) != 0 {
		t.Errorf("expected tracking to be rolled back, got %d", len(tracked))
	}
}

// TestTrack_NixEvalFailed verifies that when nix returns ErrEvalFailed,
// the goroutine rolls back tracking and GetTrackStatus reports Failed but not Watchable.
func TestTrack_NixEvalFailed(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	chk := startChecker(t, "", errors.Join(nix.ErrEvalFailed, errors.New("timeout")))

	pkgName := fmt.Sprintf("testpkg-%d", testutil.NextID())
	pkg, err := packages.Track(ctx, testStore, userID, chk, pkgName, "nixpkgs-unstable")
	if err != nil {
		t.Fatalf("Track: unexpected error: %v", err)
	}

	status := pollTrackStatus(t, userID, pkg.PackageID)
	if !status.Failed {
		t.Error("expected Failed=true")
	}
	if status.Watchable {
		t.Error("expected Watchable=false for ErrEvalFailed")
	}

	// tracking should be rolled back
	tracked, err := testStore.QueryUsersTrackedPackages(ctx, userID)
	if err != nil {
		t.Fatalf("QueryUsersTrackedPackages: %v", err)
	}
	if len(tracked) != 0 {
		t.Errorf("expected tracking to be rolled back, got %d", len(tracked))
	}
}

// ----------------------------------------------------------------
// -------------------------- Check -------------------------------
// ----------------------------------------------------------------

// TestCheck_VersionUnchanged verifies that when nix returns the same version,
// GetCheckStatus reports Done with VersionChanged=false.
func TestCheck_VersionUnchanged(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	pkgID := addTracking(t, userID) // version 1.0.0
	chk := startChecker(t, "1.0.0", nil)

	outcome, err := packages.Check(ctx, testStore, userID, chk, strconv.FormatInt(pkgID, 10))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if outcome.Skipped {
		t.Fatal("expected Skipped=false")
	}

	status := pollCheckStatus(t, userID, pkgID, "1.0.0")
	if status.Failed {
		t.Errorf("expected success, got failure: %s", status.ErrMsg)
	}
	if status.VersionChanged {
		t.Error("expected VersionChanged=false")
	}
}

// TestCheck_VersionChanged verifies that when nix returns new version,
// GetCheckStatus reports Done with VersionChanged=true and notification is created.
func TestCheck_VersionChanged(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	pkgID := addTracking(t, userID) // version 1.0.0

	addr := fmt.Sprintf("user%d@example.com", testutil.NextID())
	_, err := testStore.CreateEmailChannel(ctx, userID, addr, true)
	if err != nil {
		t.Fatalf("CreateEmailChannel: %v", err)
	}

	chk := startChecker(t, "2.0.0", nil)

	outcome, err := packages.Check(ctx, testStore, userID, chk, strconv.FormatInt(pkgID, 10))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if outcome.Skipped {
		t.Fatal("expected Skipped=false")
	}

	status := pollCheckStatus(t, userID, pkgID, "1.0.0")
	if status.Failed {
		t.Errorf("expected success, got failure: %s", status.ErrMsg)
	}
	if !status.VersionChanged {
		t.Error("expected VersionChanged=true")
	}

	// notification should be created
	rows, err := testStore.QueryNotificationsByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("QueryNotificationsByUserID: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 notification, got %d", len(rows))
	}
}

// TestCheck_Skipped verifies that when LastCheckedAt is within SkipInterval
// and Check returns Skipped=true.
func TestCheck_Skipped(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	pkgID := addTracking(t, userID)

	err := testStore.UpdatePackageLastCheckedAt(ctx, pkgID)
	if err != nil {
		t.Fatalf("UpdatePackageLastCheckedAt: %v", err)
	}

	// start checker with SkipInterval = 1 hour
	ctxChk, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	chk := checker.NewWithNixEval(testStore,
		checker.Config{WorkerCount: 1, Interval: time.Minute, SkipInterval: time.Hour},
		fakeNix("1.0.0", nil),
	)
	chk.Start(ctxChk)

	outcome, err := packages.Check(ctx, testStore, userID, chk, strconv.FormatInt(pkgID, 10))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !outcome.Skipped {
		t.Error("expected Skipped=true when recently checked")
	}
}

// TestCheck_NixFailed verifies that when nix returns an error,
// GetCheckStatus reports Done with Failed=true.
func TestCheck_NixFailed(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	pkgID := addTracking(t, userID)
	chk := startChecker(t, "", errors.Join(nix.ErrEvalFailed, errors.New("timeout")))

	outcome, err := packages.Check(ctx, testStore, userID, chk, strconv.FormatInt(pkgID, 10))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if outcome.Skipped {
		t.Fatal("expected Skipped=false")
	}

	status := pollCheckStatus(t, userID, pkgID, "1.0.0")
	if !status.Failed {
		t.Error("expected Failed=true on nix error")
	}
}

// TestCheck_UninitializedPackage verifies that Check returns early without
// enqueueing when LastNotifiedVersion is empty (package not yet initialized).
func TestCheck_UninitializedPackage(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")

	pkgName := fmt.Sprintf("testpkg-%d", testutil.NextID())
	pkgID, err := testStore.StorePackage(ctx, pkgName, "nixpkgs-unstable", "")
	if err != nil {
		t.Fatalf("StorePackage: %v", err)
	}
	err = testStore.StoreTracking(ctx, userID, pkgID, "")
	if err != nil {
		t.Fatalf("StoreTracking: %v", err)
	}

	chk := startChecker(t, "1.0.0", nil)
	outcome, err := packages.Check(ctx, testStore, userID, chk, strconv.FormatInt(pkgID, 10))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if outcome.Skipped {
		t.Error("expected Skipped=false for uninitialized package")
	}
	if outcome.Package.PackageID != pkgID {
		t.Errorf("Package.PackageID = %d, want %d", outcome.Package.PackageID, pkgID)
	}
}

// ----------------------------------------------------------------
// ----------------------- WatchCheck -----------------------------
// ----------------------------------------------------------------

// TestWatchCheck_StillNotFound verifies that when nix returns ErrAttrNotFound,
// GetWatchCheckStatus reports Done with StillNotFound=true.
func TestWatchCheck_StillNotFound(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	entry := addWatchlistEntry(t, userID)
	chk := startChecker(t, "", nix.ErrAttrNotFound)

	_, err := packages.WatchCheck(ctx, testStore, userID, chk, strconv.FormatInt(entry.WatchlistID, 10))
	if err != nil {
		t.Fatalf("WatchCheck: %v", err)
	}

	status := pollWatchCheckStatus(t, userID, entry.WatchlistID)
	if !status.StillNotFound {
		t.Error("expected StillNotFound=true")
	}
	if status.Failed {
		t.Error("expected Failed=false")
	}
}

// TestWatchCheck_Promoted verifies that when the watched package appears in nixpkgs,
// GetWatchCheckStatus reports Done with Promoted=true and tracking row is created.
func TestWatchCheck_Promoted(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	entry := addWatchlistEntry(t, userID)

	addr := fmt.Sprintf("user%d@example.com", testutil.NextID())
	_, err := testStore.CreateEmailChannel(ctx, userID, addr, true)
	if err != nil {
		t.Fatalf("CreateEmailChannel: %v", err)
	}

	chk := startChecker(t, "1.0.0", nil)

	_, err = packages.WatchCheck(ctx, testStore, userID, chk, strconv.FormatInt(entry.WatchlistID, 10))
	if err != nil {
		t.Fatalf("WatchCheck: %v", err)
	}

	status := pollWatchCheckStatus(t, userID, entry.WatchlistID)
	if !status.Promoted {
		t.Error("expected Promoted=true")
	}

	// tracking row should be created
	tracked, err := testStore.QueryUsersTrackedPackages(ctx, userID)
	if err != nil {
		t.Fatalf("QueryUsersTrackedPackages: %v", err)
	}
	if len(tracked) != 1 {
		t.Fatalf("expected 1 tracked package after promotion, got %d", len(tracked))
	}
	if tracked[0].Name != entry.Name {
		t.Errorf("tracked package name = %q, want %q", tracked[0].Name, entry.Name)
	}

	// notification should be created
	rows, err := testStore.QueryNotificationsByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("QueryNotificationsByUserID: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 notification, got %d", len(rows))
	}
}

// TestWatchCheck_Failed verifies that when nix returns a non AttrNotFound error,
// GetWatchCheckStatus reports Done with Failed=true.
func TestWatchCheck_Failed(t *testing.T) {
	ctx := context.Background()
	userID, _, _ := testutil.CreateTestUser(t, testStore, "user")
	entry := addWatchlistEntry(t, userID)
	chk := startChecker(t, "", errors.Join(nix.ErrEvalFailed, errors.New("timeout")))

	_, err := packages.WatchCheck(ctx, testStore, userID, chk, strconv.FormatInt(entry.WatchlistID, 10))
	if err != nil {
		t.Fatalf("WatchCheck: %v", err)
	}

	status := pollWatchCheckStatus(t, userID, entry.WatchlistID)
	if !status.Failed {
		t.Error("expected Failed=true")
	}
}
