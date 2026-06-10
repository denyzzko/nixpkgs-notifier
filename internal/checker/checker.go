// Package checker implements the background package version checking system.
//
// It manages two priority queues of nix eval jobs:
//   - High priority queue (highQ - capacity 128): checks triggered by users from UI (Track, Check, WatchCheck) - processed first
//   - Low priority queue  (lowQ - capacity 512):  automatic periodic system checks for all tracked packages and
//     watchlist checks for not-yet-existing packages - processed only when highQ is empty
//
// A fixed pool of worker goroutines takes periodically from both queues.
// Each worker calls nix eval for one job (acts as a rate limiter -> at most WorkerCount nix evals run concurrently, preventing overload).
//
// Two protections against nix eval abuse:
//   - Singleflight (in nix package): if multiple workers call nix for same package at the same moment, only one
//     subprocess runs and result is shared.
//   - SkipInterval threshold: nix eval is skipped if last_checked_at for that package is within configured
//     SkipInterval and stored current_version is returned instead. Watchlist checks are exeption - they always run.
//
// User-triggered jobs carry a reply channel (CheckJob.Result): caller creates a buffered channel, enqueues job,
// and blocks on the channel. Worker sends nix result back when finished, unblocking caller.
//
// System-triggered package checks (Result == nil, IsWatchlistCheck == false): worker handles the full flow -
// compares versions and calls CreatePendingNotifications if change is detected.
//
// System-triggered watchlist checks (Result == nil, IsWatchlistCheck == true): worker calls nix eval and if
// package appears for the first time calls PromoteWatchlistEntries and CreateFirstAppearanceNotifications.
package checker

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/notifications"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
)

// Checker variables (config) that can be altered by admin of the system.
// Loaded from env on startup, replaceable at runtime through the admin interface using UpdateConfig.
type Config struct {
	Interval     time.Duration // how often the schedule loop enqueues all packages in the system for automatic background version check
	WorkerCount  int           // number of workers evaluating nix eval (max concurrent nix evals)
	SkipInterval time.Duration // minimum time between nix evals for the same package (within this interval nix evals are skipped and the stored version is returned)
}

// Outcome of a single nix eval job.
// It is sent back to the caller through a reply channel (for user-triggered jobs).
type NixResult struct {
	Version string
	Err     error
	Skipped bool
}

// One unit of work placed into a priority queue.
//
// For user-triggered jobs (Result != nil): caller creates a buffered channel, assigns it to Result, enqueues job, and blocks on the channel.
// The worker sends the nix eval result back when done, unblocking the caller.
// This turns async worker pool into a synchronous call from the HTTP handler perspective, which is required for the SSR request->response flow.
// The caller is responsible for all DB operations after receiving the result.
//
// For system-triggered jobs (Result == nil, IsWatchlistCheck == false): worker handles the full flow internally.
// It compares versions and calls CreatePendingNotifications if a change is detected.
//
// For system-triggered watchlist checks (Result == nil, IsWatchlistCheck == true): worker calls nix eval and
// if the package appears for the first time calls PromoteWatchlistEntries and CreateFirstAppearanceNotifications.
type CheckJob struct {
	Name             string
	Branch           string
	PackageID        int64            // 0 if package does not exist yet (e.g. when called from package.Track())
	CurrentVersion   string           // currently stored version of package in database
	LastCheckedAt    *time.Time       // last time nix eval was executed for this package (nil means never)
	Result           chan<- NixResult // reply channel - nil for system (low-priority) jobs
	IsWatchlistCheck bool             // true for periodic background checks of watched packages
}

// Checker with all resources it needs.
// It is created once in main.go on startup.
// It manages worker pool and priority queues for nix eval jobs.
type Checker struct {
	db    *database.Store
	cfg   Config
	cfgMu sync.RWMutex // config guard mutex

	highQ chan CheckJob // user-triggered checks -> high priority
	lowQ  chan CheckJob // periodic background checks -> low priority

	nixEval func(ctx context.Context, name string, branch string) (string, error)

	workerCancels []context.CancelFunc // cancel functions for each running worker
	workerMu      sync.Mutex           // guard for workerCancels
	parentCtx     context.Context      // root context from Start()
}

// Constructs a Checker
// highQ (128 slots) - user requests are few, when full job is dropped (error is returned ->  if this happens in practice this value should be altered accordingly)
// lowQ  (512 slots) - periodic bulk enqueue of all packages, can produce many jobs at once (drops job and logs if queue gets full -> that means this capacity should also be increased)
func New(db *database.Store, cfg Config) *Checker {
	return &Checker{
		db:      db,
		cfg:     cfg,
		highQ:   make(chan CheckJob, 128),
		lowQ:    make(chan CheckJob, 512),
		nixEval: nix.GetPackageVersionByNameAndBranch,
	}
}

// Config helper that replaces config at runtime.
// Adjusts number of running workers if WorkerCount changed.
func (ch *Checker) UpdateConfig(cfg Config) {
	ch.cfgMu.Lock()
	defer ch.cfgMu.Unlock()
	ch.cfg = cfg

	ch.setWorkerCount(cfg.WorkerCount)
}

// Config returns current checker configuration.
func (ch *Checker) Config() Config {
	ch.cfgMu.RLock()
	defer ch.cfgMu.RUnlock()
	return ch.cfg
}

// Launches initial N worker goroutines (where N is WorkerCount) and the schedule loop.
// All goroutines run until ctx is cancelled (SIGTERM/SIGINT).
func (ch *Checker) Start(ctx context.Context) {
	ch.parentCtx = ctx
	cfg := ch.Config()
	ch.setWorkerCount(cfg.WorkerCount)
	go ch.scheduleLoop(ctx)
	log.Println("[INFO] checker: started")
}

// Adjusts number of running worker goroutines to match target.
// Spawns additional workers if target > current or cancels workers if target < current.
// Nothing if target == current.
func (ch *Checker) setWorkerCount(target int) {
	ch.workerMu.Lock()
	defer ch.workerMu.Unlock()

	current := len(ch.workerCancels)

	if target > current {
		// spawn additional workers
		for i := current; i < target; i++ {
			ctx, cancel := context.WithCancel(ch.parentCtx)
			ch.workerCancels = append(ch.workerCancels, cancel)
			go ch.worker(ctx)
		}
		log.Printf("[INFO] checker: increased number of workers %d -> %d", current, target)

	} else if target < current {
		// cancel excess workers
		for _, cancel := range ch.workerCancels[target:] {
			cancel()
		}
		ch.workerCancels = ch.workerCancels[:target]
		log.Printf("[INFO] checker: decreased number of workers %d -> %d", current, target)
	}
}

// Background goroutine responsible for periodic scheduling.
// Uses time.Ticker to wake up at the configured Interval to enqueue all
// tracked packages into lowQ for a background version check.
func (ch *Checker) scheduleLoop(ctx context.Context) {
	cfg := ch.Config()
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// context cancelled (graceful shutdown)
			log.Println("[INFO] checker: schedule loop stopped")
			return
		case <-ticker.C:
			// re-read config (interval may have been updated at runtime)
			cfg = ch.Config()
			ticker.Reset(cfg.Interval)

			// skip this tick if previous cycle has not finished yet
			if len(ch.lowQ) > 0 {
				log.Printf("[WARN] checker: skipping periodic check, previous cycle is still in progress (%d jobs remaining in queue). Consider adjusting configuration (try increasing worker count or check interval).", len(ch.lowQ))
				continue
			}

			// enqueue all packages
			ch.enqueueAllTracked(ctx)
			ch.enqueueAllWatched(ctx)
		}
	}
}

// enqueueAllTracked queries all packages from the database and enqueues each one as a low-priority system job.
// Skips packages whose last_checked_at is within the configured SkipInterval.
// Called by scheduleLoop on every tick.
//
// Each job carries a snapshot of the package's ID and current version at the time of enqueue,
// so process() can compare against nix eval result and detect version change.
//
// If lowQ is full, jobs are dropped and warning is logged.
func (ch *Checker) enqueueAllTracked(ctx context.Context) {
	// get all packages
	packages, err := ch.db.QueryAllTrackedPackages(ctx)
	if err != nil {
		log.Printf("[ERROR] checker: query all tracked packages: %v", err)
		return
	}

	cfg := ch.Config()
	threshold := time.Now().Add(-cfg.SkipInterval)

	// enqueue to low-priority queue
	enqueued := 0
	skipped := 0
	for _, p := range packages {
		// skip if nix eval was run recently (by user or previous system check)
		if p.LastCheckedAt != nil && p.LastCheckedAt.After(threshold) {
			skipped++
			continue
		}
		if ch.EnqueueLow(CheckJob{Name: p.Name, Branch: p.Branch, PackageID: p.ID, CurrentVersion: p.CurrentVersion, LastCheckedAt: p.LastCheckedAt}) {
			enqueued++
		}
	}

	dropped := len(packages) - enqueued - skipped
	if dropped > 0 {
		log.Printf("[WARN] checker: low-priority queue full - %d/%d packages dropped this tick. Please increase queue capacity! (or at least try to increase worker count or check interval)", dropped, len(packages))
	}

	log.Printf("[INFO] checker: enqueued %d/%d packages for background check (%d skipped - recently checked)", enqueued, len(packages), skipped)
}

// enqueueAllWatched queries all distinct (name, branch) pairs from the watchlist and enqueues
// each one as a low-priority system watchlist job.
// Unlike enqueueAllTracked, SkipInterval does not apply.
// Called by scheduleLoop on every tick.
//
// If lowQ is full, jobs are dropped and a warning is logged.
func (ch *Checker) enqueueAllWatched(ctx context.Context) {
	// get all distinct (name, branch) pairs currently in the watchlist
	entries, err := ch.db.QueryDistinctWatchlistPackages(ctx)
	if err != nil {
		log.Printf("[ERROR] checker: query watchlist packages: %v", err)
		return
	}
	if len(entries) == 0 {
		return
	}

	// enqueue to low-priority queue
	enqueued := 0
	for _, e := range entries {
		if ch.EnqueueLow(CheckJob{Name: e.Name, Branch: e.Branch, PackageID: e.PackageID, IsWatchlistCheck: true}) {
			enqueued++
		}
	}

	dropped := len(entries) - enqueued
	if dropped > 0 {
		log.Printf("[WARN] checker: low-priority queue full - %d/%d watchlist entries dropped this tick. Please increase queue capacity!", dropped, len(entries))
	}

	log.Printf("[INFO] checker: enqueued %d/%d watchlist entries for background check", enqueued, len(entries))
}

// Places a user-triggered job into the high-priority queue.
// Enqueue is non-blocking, if all 128 slots are taken, job is dropped and error is returned through reply channel so caller does not hang.
// Plain EnqueueHigh is used for Track because it always needs a real eval to set the last_notified_version baseline.
// It is also used for WatchCheck because SkipInterval does not apply to watched packages.
func (ch *Checker) EnqueueHigh(job CheckJob) {
	select {
	case ch.highQ <- job:
	default:
		// queue full
		log.Printf("[WARN] checker: high-priority queue is full, dropping job (%q/%q). Please increase queue capacity!", job.Name, job.Branch)
		if job.Result != nil {
			job.Result <- NixResult{Err: errors.New("checker: high-priority queue full")}
		}
	}
}

// Like EnqueueHigh but it also applies the SkipInterval threshold before enqueueing.
// If last_checked_at for this package is within SkipInterval, the nix eval is skipped
// entirely and stored CurrentVersion is returned immediately through the reply channel (no job is enqueued).
// Returns true if the job was skipped, false if it was enqueued for real evaluation.
func (ch *Checker) EnqueueHighOrSkip(job CheckJob) bool {
	cfg := ch.Config()
	if cfg.SkipInterval > 0 && job.LastCheckedAt != nil {
		threshold := time.Now().Add(-cfg.SkipInterval)
		if job.LastCheckedAt.After(threshold) {
			// package was checked recently - return stored version immediately, skip nix eval
			if job.Result != nil {
				job.Result <- NixResult{Version: job.CurrentVersion, Skipped: true}
			}
			return true
		}
	}
	ch.EnqueueHigh(job)
	return false
}

// Places a periodic system job into the low-priority queue.
// Returns true if the job was enqueued, false if the queue was full and the job was dropped
// job.Result must be nil - system jobs do not reply to any caller.
func (ch *Checker) EnqueueLow(job CheckJob) bool {
	select {
	case ch.lowQ <- job:
		return true
	default:
		return false // drop - next tick will re-enqueue it
	}
}

// worker is the core goroutine that processes jobs from both high and low queues.
// Each call to Start spins up WorkerCount of these, all running concurrently.
//
// --BIASED PRIORITY SELECT PATTERN--
// idea: https://groups.google.com/g/golang-nuts/c/SXsgdpRK-mE
// idea: https://stackoverflow.com/questions/11117382/priority-in-go-select-statement-workaround
//
// Go's select picks a ready case at random when multiple cases are
// ready simultaneously, so a plain two-case select would not guarantee that
// highQ is always preferred over lowQ.
//
// The biased priority pattern fixes this with two selects:
//
//  1. A non-blocking select (with default) checks highQ first. If a job is
//     waiting there, the worker takes it and loops back (via continue),
//     checking highQ again before ever touching lowQ. This drains the entire
//     high-priority queue before any low-priority job is processed.
//
//  2. Only when highQ is empty does the worker fall through to the second
//     select, which blocks on both queues. The highQ case is still listed here,
//     so if high priority job arrives while worker is blocked, it will still get picked.
//     If both become ready at the same moment, Go's random selection will pick randomly from the two.
func (ch *Checker) worker(ctx context.Context) {
	for {
		// 1. Non-blocking attempt to drain high-priority queue first
		select {
		case job := <-ch.highQ:
			ch.dispatch(ctx, job)
			continue // loop back and check highQ again before touching lowQ
		default:
			// highQ is empty - fall through to blocking select below
		}

		// 2. Block until work arrives on either queue
		select {
		case <-ctx.Done():
			// context cancelled (graceful shutdown) - stop this worker
			return
		case job := <-ch.highQ:
			ch.dispatch(ctx, job)
		case job := <-ch.lowQ:
			ch.dispatch(ctx, job)
		}
	}
}

// dispatch routes a job to the appropriate handler based on job type.
// User-triggered jobs (Result != nil) are handled by processUserJob.
// System watchlist jobs (IsWatchlistCheck == true) are handled by processSystemWatchlistJob.
// System tracked package jobs are handled by processSystemTrackedJob.
func (ch *Checker) dispatch(ctx context.Context, job CheckJob) {
	if job.Result != nil {
		ch.processUserJob(ctx, job)
	} else if job.IsWatchlistCheck {
		ch.processSystemWatchlistJob(ctx, job)
	} else {
		ch.processSystemTrackedJob(ctx, job)
	}
}

// processUserJob handles a user-triggered job (job.Result != nil).
// Runs nix eval and sends the raw result (possibly including error) back through the reply channel.
// All DB operations (including last_checked_at update) are the responsibility of the caller's goroutine (packages layer).
func (ch *Checker) processUserJob(ctx context.Context, job CheckJob) {
	version, err := ch.nixEval(ctx, job.Name, job.Branch)
	job.Result <- NixResult{Version: version, Err: err}
}

// processSystemTrackedJob handles a periodic background check for a tracked package (Result == nil, IsWatchlistCheck == false).
// Runs nix eval, updates last_checked_at, and calls CreatePendingNotifications if a version change is detected.
func (ch *Checker) processSystemTrackedJob(ctx context.Context, job CheckJob) {
	version, err := ch.nixEval(ctx, job.Name, job.Branch)

	if err != nil {
		if errors.Is(err, nix.ErrAttrNotFound) {
			// when this package exists in system, but nix doesn't find it anymore, it means it was probably removed
			log.Printf("[WARN] checker: package no longer in nixpkgs (%q/%q)", job.Name, job.Branch)
		} else {
			log.Printf("[WARN] checker: nix eval failed (%q/%q): %v", job.Name, job.Branch, err)
		}
		return
	}

	// update last_checked_at
	dbErr := ch.db.UpdatePackageLastCheckedAt(ctx, job.PackageID)
	if dbErr != nil {
		log.Printf("[WARN] checker: update last_checked_at failed (%q/%q): %v", job.Name, job.Branch, dbErr)
	}

	// compare versions
	// empty CurrentVersion means that package is not fully initialized yet
	// e.g. user tracked new package and version was not evaluated yet
	if job.CurrentVersion == "" || version == job.CurrentVersion {
		return // no change
	}

	log.Printf("[INFO] checker: version change detected %q/%q: %s -> %s",
		job.Name, job.Branch, job.CurrentVersion, version)

	// all users tracking this package are notified about version change
	// triggerUserID=0 signals a system-triggered check
	notifications.CreatePendingNotifications(ctx, ch.db, notifications.VersionEvent{
		PackageID:   job.PackageID,
		PackageName: job.Name,
		Branch:      job.Branch,
		NewVersion:  version,
	}, 0)
}

// processSystemWatchlistJob handles a periodic background check for a watched package (Result == nil, IsWatchlistCheck == true).
// Runs nix eval and if package appears calls PromoteWatchlistEntries to create package and tracking rows
// for all users who had it in their watchlist, removes their watchlist entries, and queues notifications.
func (ch *Checker) processSystemWatchlistJob(ctx context.Context, job CheckJob) {
	version, err := ch.nixEval(ctx, job.Name, job.Branch)
	if err != nil {
		if errors.Is(err, nix.ErrAttrNotFound) {
			// still not in nixpkgs
			return
		}
		log.Printf("[WARN] checker: watchlist nix eval failed (%q/%q): %v", job.Name, job.Branch, err)
		return
	}

	log.Printf("[INFO] checker: watched package appeared (%q/%q) version=%s - creating tracking rows", job.Name, job.Branch, version)

	// create package row, tracking rows for all users who had it in their watchlist, remove their watchlist entries
	userIDs, err := ch.db.PromoteWatchlistEntries(ctx, job.PackageID, version)
	if err != nil {
		log.Printf("[ERROR] checker: promote watchlist entries (%q/%q): %v", job.Name, job.Branch, err)
		return
	}
	if len(userIDs) == 0 {
		// no users to notify about
		// probably another worker already promoted
		return
	}

	// notify all users - triggerUserID=0 signals system-triggered check
	notifications.CreatePendingNotificationsFirstAppearance(ctx, ch.db, notifications.VersionEvent{
		PackageID:   job.PackageID,
		PackageName: job.Name,
		Branch:      job.Branch,
		NewVersion:  version,
	}, 0)
}
