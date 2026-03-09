// Package checker implements the background package version checking system
//
// It manages two priority queues of nix eval jobs:
// - High priority queue (highQ): checks triggered by users from UI (Track, Check, CheckAll) - processed first
// - Low priority queue   (lowQ): automatic periodic system checks for all tracked packages  - processed only when highQ is empty
//
// A fixed pool of worker goroutines takes periodically from both queues
// Each worker calls nix eval for one job (acts as a rate limiter -> at most WorkerCount nix evals run concurrently, preventing overload)
// User-triggered jobs carry a reply channel (CheckJob.Result) so the caller can block and wait for the nix eval result
// System jobs carry no reply channel (worker handles full flow itself)
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
// Loaded from env on startup, replaceable at runtime through the admin interface
type Config struct {
	Interval    time.Duration // how often the schedule loop enqueues all packages for a version check
	WorkerCount int           // number of workers evaluating nix eval (max concurrent nix evals)
}

// Outcome of a single nix eval job
// It is sent back to the caller through a reply channel (for user-triggered jobs)
type NixResult struct {
	Version string
	Err     error
}

// "One unit of work" placed into a priority queue
//
// Caller creates a buffered channel, puts it inside the job as Result, and then blocks (wait) for a value to arrive
// Worker runs the nix eval and sends result into Result when done.
// This is basically an asynchronous rate-limited worker turned into a synchronous call from the caller's point of view (necessary for SSR request->response)
//
// Result != nil  ->  user-triggered job: worker sends nix eval result back, caller handles DB logic
// Result == nil -> system-triggered job: worker handles full flow internally (version compare + notifications)
type CheckJob struct {
	Name           string
	Branch         string
	PackageID      int64            // 0 for user-triggered jobs (it does not know it yet)
	CurrentVersion string           // empty for user-triggered jobs (it does not know it yet)
	Result         chan<- NixResult // reply channel — nil for system (low-priority) jobs
}

// Checker with all resources it needs
// It is created once in main.go on startup
type Checker struct {
	db    *database.Store
	cfg   Config
	cfgMu sync.RWMutex // config guard mutex

	highQ chan CheckJob // user-triggered checks
	lowQ  chan CheckJob // periodic background checks
}

// Constructs a Checker
// highQ capacity (128) - user requests are few, when full they are not dropped silently (error is returned -> if happens this value should be altered accordingly)
// lowQ capacity  (512) - periodic bulk enqueue of all packages, can produce many jobs at once (logs if queue gets full -> that means this capacity should be increase)
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

// Launches N worker goroutines, where N is WorkerCount and the schedule loop
// All goroutines run until ctx is cancelled (SIGTERM/SIGINT)
func (ch *Checker) Start(ctx context.Context) {
	cfg := ch.config()
	for i := 0; i < cfg.WorkerCount; i++ {
		go ch.worker(ctx)
	}
	go ch.scheduleLoop(ctx)
	log.Println("[INFO] checker: started")
}

// Core background goroutine for periodic scheduling
// Uses time.Ticker to wake up at the configured Interval and enqueue all
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
// Called by scheduleLoop on every tick
//
// Each job carries a snapshot of the package's current ID and version that was fetched,
// so process() can compare versions and create notifications
//
// Counts how many jobs were dropped so it can create a log warning
func (ch *Checker) enqueueAll(ctx context.Context) {
	// get all packages
	packages, err := ch.db.QueryAllPackages(ctx)
	if err != nil {
		log.Printf("[ERROR] checker: query all packages: %v", err)
		return
	}

	// enqueue to low-priority queue (with counter so warning can be logged notifying admin to increase capacity)
	enqueued := 0
	for _, p := range packages {
		if ch.EnqueueLow(CheckJob{Name: p.Name, Branch: p.Branch, PackageID: p.ID, CurrentVersion: p.CurrentVersion}) {
			enqueued++
		}
	}

	dropped := len(packages) - enqueued
	if dropped > 0 {
		log.Printf("[WARN] checker: low-priority queue full - %d/%d packages dropped this tick. Please increase queue capacity! (or at least try to increase worker count or check interval)", dropped, len(packages))
	}

	log.Printf("[INFO] checker: enqueued %d/%d packages for background check", enqueued, len(packages))
}

// Places a user-triggered job into the high-priority queue
// Uses a non-blocking select so the caller is never blocked by a full queue
// If the queue is full (all 128 slots are taken), job is dropped and error
// is sent back through the reply channel so the caller does not hang forever (if this happens because system has grown, capacity should be increased accordingly)
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

// Places a system periodic job into the low-priority queue
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
// Go's select statement picks a ready case at random when multiple cases are
// ready simultaneously, so a plain two-case select would not guarantee that
// highQ is always preferred over lowQ. The biased priority pattern fixes this
// with two selects:
//
//  1. A non-blocking select (with default) checks highQ first. If a job is
//     waiting there, the worker takes it immediately and loops back (continue),
//     checking highQ again before ever touching lowQ. This drains the entire
//     high-priority queue before any low-priority job is processed.
//
//  2. Only when highQ is empty does the worker fall through to the second
//     select, which blocks on both queues. The highQ case is still listed first
//     here, so if a high-priority job arrives at the same moment a low-priority
//     one is ready, Go's random selection will sometimes pick highQ — but the
//     first non-blocking select already ensures highQ is fully drained on every
//     loop iteration, so starvation of user requests is not possible.
func (ch *Checker) worker(ctx context.Context) {
	for {
		// 1. Non-blocking attempt to drain high-priority queue first
		select {
		case job := <-ch.highQ:
			ch.process(ctx, job)
			continue // loop back and check highQ again before touching lowQ
		default:
			// highQ is empty - fall through to blocking select below
		}

		// 2. Block waiting for work from either queue
		select {
		case <-ctx.Done():
			// context cancelled (graceful shutdown) - stop this worker
			return
		case job := <-ch.highQ:
			ch.process(ctx, job)
		case job := <-ch.lowQ:
			ch.process(ctx, job)
		}
	}
}

// Runs nix eval for one job and handles the result
// Its behaviour differs based on whether the job carries a reply channel:
//
// -> user-triggered (job.Result != nil):
//   - sends the raw nix eval result back through the reply channel and returns
//   - all DB operations and notification logic are the responsibility of the
//     caller (packages layer) after it unblocks from <-resultCh
//
// -> system-triggered (job.Result == nil):
//   - compares the fetched version against the value stored in the database
//   - if a change is detected, calls CreatePendingNotifications to send notifications to users
func (ch *Checker) process(ctx context.Context, job CheckJob) {
	version, err := nix.GetPackageVersionByNameAndBranch(ctx, job.Name, job.Branch)

	if job.Result != nil {
		// user-triggered: caller is blocked on <-resultCh waiting for exactly one value
		// raw nix result is returned
		job.Result <- NixResult{Version: version, Err: err}
		return
	}

	// system-triggered: version compare + notifications
	if err != nil {
		if errors.Is(err, nix.ErrAttrNotFound) {
			// when this package exists in system, but nix doesn't find it anymore, it means it was probably removed
			log.Printf("[WARN] checker: package no longer in nixpkgs (%q/%q)", job.Name, job.Branch)
		} else {
			log.Printf("[WARN] checker: nix eval failed (%q/%q): %v", job.Name, job.Branch, err)
		}
		return
	}

	// compare versions
	if version == job.CurrentVersion {
		return // no change
	}

	log.Printf("[INFO] checker: version change detected %q/%q: %s → %s",
		job.Name, job.Branch, job.CurrentVersion, version)

	// all users tracking this package are notified about version change
	// triggerUserID=0 signals a system-triggered check
	notifications.CreatePendingNotifications(ctx, ch.db, job.PackageID, job.Name, job.Branch, version, 0)
}
