// Package checker implements the background package version checking system
//
// It manages two priority queues of nix eval jobs:
// - High priority queue (highQ - capacity 128): checks triggered by users from UI (Track, Check, CheckAll) - processed first
// - Low priority queue   (lowQ - capacity 512): automatic periodic system checks for all tracked packages  - processed only when highQ is empty
//
// A fixed pool of worker goroutines takes periodically from both queues
// Each worker calls nix eval for one job (acts as a rate limiter -> at most WorkerCount nix evals run concurrently, preventing overload)
//
// Two protections of nix eval abuse:
//   - Singleflight (in nix package): if multiple workers call nix for same package at the same moment, only one subprocess runs evaluation with nix eval and result is shared
//   - SkipInterval treshold: nix eval is skipped if last_checked_at for that package is within configured SkipInterval (package's current_version is returned instead), this protects from sequential abuse requests
//
// User-triggered jobs carry a reply channel (CheckJob.Result) so the caller can create a buffered channel, place in job, enqueue job and block on the channel waiting for result (necessary for the SSR request->response flow)
// Worker sends nix result back when finished, which unblocks caller
//
// System-triggered jobs carry no reply channel (worker handles full flow itself - version comparison and notification creation)
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

// Checker variables (config) that can be altered by admin of the system
// Loaded from env on startup, replaceable at runtime through the admin interface using UpdateConfig
type Config struct {
	Interval     time.Duration // how often the schedule loop enqueues all packages in the system for automatic background version check
	WorkerCount  int           // number of workers evaluating nix eval (max concurrent nix evals)
	SkipInterval time.Duration // minimum time between nix evals for the same package (within this interval nix evals are skipped and the stored version is returned)
}

// Outcome of a single nix eval job
// It is sent back to the caller through a reply channel (for user-triggered jobs)
type NixResult struct {
	Version string
	Err     error
	Skipped bool
}

// One unit of work placed into a priority queue
//
// For user-triggered jobs (Result != nil): caller creates a buffered channel, assigns it to Result, enqueues job, and blocks on the channel
// The worker sends the nix eval result back when done, unblocking the caller
// This turns async worker pool into a synchronous call from the HTTP handler perspective, which is required for the SSR request->response flow
// The caller is responsible for all DB operations after receiving the result
//
// For system-triggered jobs (Result == nil): worker handles the full flow internally
// It compares versions and calls CreatePendingNotifications if a change is detected
type CheckJob struct {
	Name           string
	Branch         string
	PackageID      int64            // 0 if package does not exist yet (e.g. when called from package.Track())
	CurrentVersion string           // currently stored version of package in database
	LastCheckedAt  *time.Time       // last time nix eval was executed for this package (nil means never)
	Result         chan<- NixResult // reply channel - nil for system (low-priority) jobs
}

// Checker with all resources it needs
// It is created once in main.go on startup
// It manages worker pool and priority queues for nix eval jobs
type Checker struct {
	db    *database.Store
	cfg   Config
	cfgMu sync.RWMutex // config guard mutex

	highQ chan CheckJob // user-triggered checks -> high priority
	lowQ  chan CheckJob // periodic background checks -> low priority
}

// Constructs a Checker
// highQ (128 slots) - user requests are few, when full they are not dropped silently (error is returned ->  if this happens in practice this value should be altered accordingly)
// lowQ  (512 slots) - periodic bulk enqueue of all packages, can produce many jobs at once (logs if queue gets full -> that means this capacity should also be increased)
func New(db *database.Store, cfg Config) *Checker {
	return &Checker{
		db:    db,
		cfg:   cfg,
		highQ: make(chan CheckJob, 128),
		lowQ:  make(chan CheckJob, 512),
	}
}

// Config helper that replaces config at runtime
func (ch *Checker) UpdateConfig(cfg Config) {
	ch.cfgMu.Lock()
	defer ch.cfgMu.Unlock()
	ch.cfg = cfg
}

// Config helper that returns current config
func (ch *Checker) config() Config {
	ch.cfgMu.RLock()
	defer ch.cfgMu.RUnlock()
	return ch.cfg
}

// Launches N worker goroutines (where N is WorkerCount) and the schedule loop
// All goroutines run until ctx is cancelled (SIGTERM/SIGINT)
func (ch *Checker) Start(ctx context.Context) {
	cfg := ch.config()
	for i := 0; i < cfg.WorkerCount; i++ {
		go ch.worker(ctx)
	}
	go ch.scheduleLoop(ctx)
	log.Println("[INFO] checker: started")
}

// Background goroutine responsible for periodic scheduling
// Uses time.Ticker to wake up at the configured Interval to enqueue all
// tracked packages into lowQ for a background version check
func (ch *Checker) scheduleLoop(ctx context.Context) {
	cfg := ch.config()
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// context cancelled (graceful shutdown)
			log.Println("[INFO] checker: schedule loop stopped")
			return
		case <-ticker.C:
			// re-read config (interval may have been updated at runtime) and enqueue all packages
			cfg = ch.config()
			ticker.Reset(cfg.Interval)
			ch.enqueueAll(ctx)
		}
	}
}

// Queries all packages from the database and enqueues each one as a low-priority system job
// During enqueuing it skips any package whose last_checked_at is within configured SkipInterval
// Called by scheduleLoop on every tick
//
// Each job carries a snapshot of the package's ID and current version at the time of enqueue,
// so process() can compare against nix eval result and detect version change
//
// If lowQ is full, jobs are dropped and warning is logged
func (ch *Checker) enqueueAll(ctx context.Context) {
	// get all packages
	packages, err := ch.db.QueryAllPackages(ctx)
	if err != nil {
		log.Printf("[ERROR] checker: query all packages: %v", err)
		return
	}

	cfg := ch.config()
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

// Places a user-triggered job into the high-priority queue
// Enqueue is non-blocking, if all 128 slots are taken, job is dropped and error is returned through reply channel so caller does not hang
// Plain EnqueueHigh is used for Track because that always needs a real eval to set last_notified_version baseline correctly
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

// Like EnqueueHigh but it also applies the SkipInterval threshold before enqueueing
// If last_checked_at for this package is within SkipInterval, the nix eval is skipped
// entirely and stored CurrentVersion is returned immediately through the reply channel (no job is enqueued)
// Returns true if the job was skipped, false if it was enqueued for real evaluation
func (ch *Checker) EnqueueHighOrSkip(job CheckJob) bool {
	cfg := ch.config()
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

// Places a periodic system job into the low-priority queue
// Returns true if the job was enqueued, false if the queue was full and the job was dropped
// job.Result must be nil - system jobs do not reply to any caller
func (ch *Checker) EnqueueLow(job CheckJob) bool {
	select {
	case ch.lowQ <- job:
		return true
	default:
		return false // drop - next tick will re-enqueue it
	}
}

// worker is the core goroutine that processes jobs from both high and low queues
// Each call to Start spins up WorkerCount of these, all running concurrently
//
// --BIASED PRIORITY SELECT PATTERN--
// idea: https://groups.google.com/g/golang-nuts/c/SXsgdpRK-mE
// idea: https://stackoverflow.com/questions/11117382/priority-in-go-select-statement-workaround
//
// Go's select picks a ready case at random when multiple cases are
// ready simultaneously, so a plain two-case select would not guarantee that
// highQ is always preferred over lowQ
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

// dispatch routes a job to the appropriate handler based on whether it carries a reply channel
func (ch *Checker) dispatch(ctx context.Context, job CheckJob) {
	if job.Result != nil {
		ch.processUserJob(ctx, job)
	} else {
		ch.processSystemJob(ctx, job)
	}
}

// processUserJob handles a user-triggered job (job.Result != nil)
// Runs nix eval and sends the raw result (possibly including error) back through the reply channel
// All DB operations (including last_checked_at update) are the responsibility of the caller's goroutine (packages layer)
func (ch *Checker) processUserJob(ctx context.Context, job CheckJob) {
	version, err := nix.GetPackageVersionByNameAndBranch(ctx, job.Name, job.Branch)
	job.Result <- NixResult{Version: version, Err: err}
}

// processSystemJob handles a system-triggered (periodic background) job (job.Result == nil)
// Runs nix eval, compares fetched version against the stored one, updates last_checked_at on success
// and calls CreatePendingNotifications if a version change is detected
func (ch *Checker) processSystemJob(ctx context.Context, job CheckJob) {
	version, err := nix.GetPackageVersionByNameAndBranch(ctx, job.Name, job.Branch)

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

	log.Printf("[INFO] checker: version change detected %q/%q: %s → %s",
		job.Name, job.Branch, job.CurrentVersion, version)

	// all users tracking this package are notified about version change
	// triggerUserID=0 signals a system-triggered check
	notifications.CreatePendingNotifications(ctx, ch.db, job.PackageID, job.Name, job.Branch, version, 0)
}
