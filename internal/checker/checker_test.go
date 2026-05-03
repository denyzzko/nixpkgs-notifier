package checker_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
	"github.com/denyzzko/nixpkgs-notifier/internal/testutil"
)

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

func defaultCfg() checker.Config {
	return checker.Config{
		WorkerCount:  1,
		SkipInterval: 0,
	}
}

// fakeNix returns nix eval function that always returns specified version or error.
func fakeNix(version string, err error) func(ctx context.Context, name string, branch string) (string, error) {
	return func(_ context.Context, _ string, _ string) (string, error) {
		return version, err
	}
}

// notificationsForUser returns all notification records for given user.
func notificationsForUser(t *testing.T, userID int64) []database.UserNotification {
	t.Helper()
	rows, err := testStore.QueryNotificationsByUserID(context.Background(), userID)
	if err != nil {
		t.Fatalf("notificationsForUser: %v", err)
	}
	return rows
}

// trackingsForUser returns all tracking records for given user.
func trackingsForUser(t *testing.T, userID int64) []database.TrackedPackage {
	t.Helper()
	rows, err := testStore.QueryUsersTrackedPackages(context.Background(), userID)
	if err != nil {
		t.Fatalf("trackingsForUser: %v", err)
	}

	return rows
}

// setupTrackedPackage creates user, package, tracking and email channel.
// Returns userID, packageID and CheckJob ready to pass to Dispatch.
func setupTrackedPackage(t *testing.T, currentVersion string) (userID, packageID int64, job checker.CheckJob) {
	t.Helper()
	ctx := context.Background()

	userID, _, _ = testutil.CreateTestUser(t, testStore, "user")

	pkgName := fmt.Sprintf("testpkg-%d", testutil.NextID())
	pkgID, err := testStore.StorePackage(ctx, pkgName, "nixpkgs-unstable", currentVersion)
	if err != nil {
		t.Fatalf("setupTrackedPackage: StorePackage: %v", err)
	}

	err = testStore.StoreTracking(ctx, userID, pkgID, currentVersion)
	if err != nil {
		t.Fatalf("setupTrackedPackage: StoreTracking: %v", err)
	}

	addr := fmt.Sprintf("user%d@example.com", testutil.NextID())
	_, err = testStore.CreateEmailChannel(ctx, userID, addr, false)
	if err != nil {
		t.Fatalf("setupTrackedPackage: CreateEmailChannel: %v", err)
	}

	job = checker.CheckJob{
		Name:           pkgName,
		Branch:         "nixpkgs-unstable",
		PackageID:      pkgID,
		CurrentVersion: currentVersion,
	}

	return userID, pkgID, job
}

// setupWatchedPackage creates user, watchlist entry and email channel.
// Returns userID and CheckJob ready to pass to Dispatch.
func setupWatchedPackage(t *testing.T) (userID int64, job checker.CheckJob) {
	t.Helper()
	ctx := context.Background()

	userID, _, _ = testutil.CreateTestUser(t, testStore, "user")

	pkgName := fmt.Sprintf("watchpkg-%d", testutil.NextID())

	pkgID, err := testStore.StorePackage(ctx, pkgName, "nixpkgs-unstable", "")
	if err != nil {
		t.Fatalf("setupWatchedPackage: StorePackage: %v", err)
	}

	_, err = testStore.CreateWatchlistEntry(ctx, userID, pkgID)
	if err != nil {
		t.Fatalf("setupWatchedPackage: CreateWatchlistEntry: %v", err)
	}

	addr := fmt.Sprintf("user%d@example.com", testutil.NextID())
	_, err = testStore.CreateEmailChannel(ctx, userID, addr, false)
	if err != nil {
		t.Fatalf("setupWatchedPackage: CreateEmailChannel: %v", err)
	}

	job = checker.CheckJob{
		Name:             pkgName,
		Branch:           "nixpkgs-unstable",
		PackageID:        pkgID,
		IsWatchlistCheck: true,
	}

	return userID, job
}

// ----------------------------------------------------------------
// -------------- processSystemTrackedJob -------------------------
// ----------------------------------------------------------------

// TestSystemTrackedJob_VersionUnchanged verifies that no notification is created
// when nix returns same version that is already stored.
func TestSystemTrackedJob_VersionUnchanged(t *testing.T) {
	userID, _, job := setupTrackedPackage(t, "1.0.0")
	c := checker.NewWithNixEval(testStore, defaultCfg(), fakeNix("1.0.0", nil))

	c.Dispatch(context.Background(), job)

	got := notificationsForUser(t, userID)
	if len(got) != 0 {
		t.Errorf("expected no notifications, got %d", len(got))
	}
}

// TestSystemTrackedJob_VersionChanged verifies that notification is created
// when nix returns new version.
func TestSystemTrackedJob_VersionChanged(t *testing.T) {
	userID, _, job := setupTrackedPackage(t, "1.0.0")
	c := checker.NewWithNixEval(testStore, defaultCfg(), fakeNix("2.0.0", nil))

	c.Dispatch(context.Background(), job)

	notifications := notificationsForUser(t, userID)
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifications))
	}
	if notifications[0].NewVersion != "2.0.0" {
		t.Errorf("new version: got %q, want %q", notifications[0].NewVersion, "2.0.0")
	}
}

// TestSystemTrackedJob_EmptyCurrentVersion verifies that no notification is created
// when CurrentVersion is empty (package not yet initialized).
func TestSystemTrackedJob_EmptyCurrentVersion(t *testing.T) {
	userID, _, job := setupTrackedPackage(t, "")
	job.CurrentVersion = ""
	c := checker.NewWithNixEval(testStore, defaultCfg(), fakeNix("1.0.0", nil))

	c.Dispatch(context.Background(), job)

	got := notificationsForUser(t, userID)
	if len(got) != 0 {
		t.Errorf("expected no notifications for uninitialized package, got %d", len(got))
	}
}

// TestSystemTrackedJob_NixAttrNotFound verifies that ErrAttrNotFound is handled
// correctly with no notification created.
func TestSystemTrackedJob_NixAttrNotFound(t *testing.T) {
	userID, _, job := setupTrackedPackage(t, "1.0.0")
	c := checker.NewWithNixEval(testStore, defaultCfg(), fakeNix("", nix.ErrAttrNotFound))

	c.Dispatch(context.Background(), job)

	got := notificationsForUser(t, userID)
	if len(got) != 0 {
		t.Errorf("expected no notifications on nix error, got %d", len(got))
	}
}

// TestSystemTrackedJob_NixEvalFailed verifies that ErrEvalFailed is handled
// correctly with no notification created.
func TestSystemTrackedJob_NixEvalFailed(t *testing.T) {
	userID, _, job := setupTrackedPackage(t, "1.0.0")
	c := checker.NewWithNixEval(testStore, defaultCfg(), fakeNix("", errors.Join(nix.ErrEvalFailed, errors.New("network timeout"))))

	c.Dispatch(context.Background(), job)

	got := notificationsForUser(t, userID)
	if len(got) != 0 {
		t.Errorf("expected no notifications on nix error, got %d", len(got))
	}
}

// ----------------------------------------------------------------
// -------------- processSystemWatchlistJob -----------------------
// ----------------------------------------------------------------

// TestSystemWatchlistJob_PackageNotYetInNixpkgs verifies that nothing happens
// when nix returns ErrAttrNotFound for watched package.
func TestSystemWatchlistJob_PackageNotYetInNixpkgs(t *testing.T) {
	userID, job := setupWatchedPackage(t)
	c := checker.NewWithNixEval(testStore, defaultCfg(), fakeNix("", nix.ErrAttrNotFound))

	c.Dispatch(context.Background(), job)

	got := notificationsForUser(t, userID)
	if len(got) != 0 {
		t.Errorf("expected no notifications, got %d", len(got))
	}
}

// TestSystemWatchlistJob_PackageAppears verifies that when watched package
// appears in nixpkgs, tracking rows are created and notification is queued with new version.
func TestSystemWatchlistJob_PackageAppears(t *testing.T) {
	userID, job := setupWatchedPackage(t)
	c := checker.NewWithNixEval(testStore, defaultCfg(), fakeNix("1.0.0", nil))

	c.Dispatch(context.Background(), job)

	// tracking row should be created with correct package
	tracked := trackingsForUser(t, userID)
	if len(tracked) != 1 {
		t.Fatalf("expected 1 tracked package, got %d", len(tracked))
	}
	if tracked[0].Name != job.Name {
		t.Errorf("tracked package name: got %q, want %q", tracked[0].Name, job.Name)
	}

	// notification should be created with correct version
	notifications := notificationsForUser(t, userID)
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification after package appeared, got %d", len(notifications))
	}
	if notifications[0].NewVersion != "1.0.0" {
		t.Errorf("new version: got %q, want %q", notifications[0].NewVersion, "1.0.0")
	}
}

// ----------------------------------------------------------------
// 							 UNIT TESTS
// ----------------------------------------------------------------

// ----------------------------------------------------------------
// ----------------------- processUserJob -------------------------
// ----------------------------------------------------------------

// TestUserJob_ResultSentOnChannel verifies that nix result is sent back
// on the reply channel.
func TestUserJob_ResultSentOnChannel(t *testing.T) {
	c := checker.NewWithNixEval(nil, defaultCfg(), fakeNix("3.0.0", nil))
	result := make(chan checker.NixResult, 1)

	c.Dispatch(context.Background(), checker.CheckJob{
		Name:   "somepkg",
		Branch: "nixpkgs-unstable",
		Result: result,
	})

	select {
	case r := <-result:
		if r.Err != nil {
			t.Errorf("unexpected error: %v", r.Err)
		}
		if r.Version != "3.0.0" {
			t.Errorf("version: got %q, want %q", r.Version, "3.0.0")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for result on reply channel")
	}
}

// TestUserJob_ErrorSentOnChannel verifies that nix errors are forwarded
// through reply channel.
func TestUserJob_ErrorSentOnChannel(t *testing.T) {
	c := checker.NewWithNixEval(nil, defaultCfg(), fakeNix("", nix.ErrAttrNotFound))
	result := make(chan checker.NixResult, 1)

	c.Dispatch(context.Background(), checker.CheckJob{
		Name:   "nosuchpkg",
		Branch: "nixpkgs-unstable",
		Result: result,
	})

	select {
	case r := <-result:
		if !errors.Is(r.Err, nix.ErrAttrNotFound) {
			t.Errorf("expected ErrAttrNotFound, got %v", r.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for result on reply channel")
	}
}

// ----------------------------------------------------------------
// -------------------- EnqueueHighOrSkip -------------------------
// ----------------------------------------------------------------

// TestEnqueueHighOrSkip_SkipsRecentlyChecked verifies that when LastCheckedAt
// is within SkipInterval, nix eval is skipped and stored version is returned.
func TestEnqueueHighOrSkip_SkipsRecentlyChecked(t *testing.T) {
	cfg := defaultCfg()
	cfg.SkipInterval = time.Hour
	c := checker.NewWithNixEval(nil, cfg, fakeNix("999.0.0", nil))

	result := make(chan checker.NixResult, 1)
	now := time.Now()
	skipped := c.EnqueueHighOrSkip(checker.CheckJob{
		Name:           "somepkg",
		Branch:         "nixpkgs-unstable",
		CurrentVersion: "1.0.0",
		LastCheckedAt:  &now,
		Result:         result,
	})

	if !skipped {
		t.Fatal("expected job to be skipped")
	}
	r := <-result
	if r.Version != "1.0.0" {
		t.Errorf("version: got %q, want stored %q", r.Version, "1.0.0")
	}
	if !r.Skipped {
		t.Error("expected NixResult.Skipped to be true")
	}
}

// TestEnqueueHighOrSkip_EvaluatesPackage verifies that when LastCheckedAt
// is outside SkipInterval, the job is enqueued for nix eval.
func TestEnqueueHighOrSkip_EvaluatesPackage(t *testing.T) {
	cfg := defaultCfg()
	cfg.SkipInterval = time.Hour
	cfg.Interval = time.Minute
	c := checker.NewWithNixEval(nil, cfg, fakeNix("2.0.0", nil))

	result := make(chan checker.NixResult, 1)
	old := time.Now().Add(-2 * time.Hour)
	skipped := c.EnqueueHighOrSkip(checker.CheckJob{
		Name:          "somepkg",
		Branch:        "nixpkgs-unstable",
		LastCheckedAt: &old,
		Result:        result,
	})

	if skipped {
		t.Fatal("expected job to be enqueued, not skipped")
	}

	// start a worker to drain queue
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c.Start(ctx)

	select {
	case r := <-result:
		if r.Version != "2.0.0" {
			t.Errorf("version: got %q, want %q", r.Version, "2.0.0")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for result")
	}
}

// ----------------------------------------------------------------
// ------------------------- EnqueueHigh --------------------------
// ----------------------------------------------------------------

// TestEnqueueHigh_PlacesJobInHighQueue verifies that EnqueueHigh places
// job into the high-priority queue.
func TestEnqueueHigh_PlacesJobInHighQueue(t *testing.T) {
	c := checker.NewWithNixEval(nil, defaultCfg(), fakeNix("1.0.0", nil))

	c.EnqueueHigh(checker.CheckJob{Name: "pkg", Branch: "nixpkgs-unstable"})

	if got := c.HighQLen(); got != 1 {
		t.Errorf("high queue len: got %d, want 1", got)
	}
	if got := c.LowQLen(); got != 0 {
		t.Errorf("low queue len: got %d, want 0", got)
	}
}

// ----------------------------------------------------------------
// ------------------------- EnqueueLow --------------------------
// ----------------------------------------------------------------

// TestEnqueueLow_PlacesJobInLowQueue verifies that EnqueueLow places
// job into the low-priority queue.
func TestEnqueueLow_PlacesJobInLowQueue(t *testing.T) {
	c := checker.NewWithNixEval(nil, defaultCfg(), fakeNix("1.0.0", nil))

	c.EnqueueLow(checker.CheckJob{Name: "pkg", Branch: "nixpkgs-unstable"})

	if got := c.LowQLen(); got != 1 {
		t.Errorf("low queue len: got %d, want 1", got)
	}
	if got := c.HighQLen(); got != 0 {
		t.Errorf("high queue len: got %d, want 0", got)
	}
}
