// Package cleaner implements the background notification cleanup loop.
//
// On server startup goroutine deletes all notifications already past the configured
// retention window, then it finds the oldest notification and sleeps until
// that notification is due for deletion. It wakes up, deletes everything that is
// now expired and repeats. Goroutine only wakes up when actual work needs to be done.
//
// When retention setting is changed in admin config while the goroutine is
// sleeping, UpdateConfig stores new config and does a non-blocking send on
// buffered wake channel for this cleaner.This interrupts the sleep and causes immediate
// re-evaluation with new settings.
//
// When retention value is set to 0 (disabled) goroutine will only react to
// config changes or server shutdown.
package cleaner

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/database"
)

// Config holds the retention setting managed by the admin via UI.
type Config struct {
	RetentionDays int // 0 = disabled, 30 = 1 month, 90 = 3 months, 180 = 6 months
}

// Cleaner holds all needed resources for the cleanup loop.
// It is created once in main.go on startup.
type Cleaner struct {
	db    *database.Store
	cfg   Config
	cfgMu sync.RWMutex
	wake  chan struct{} // buffered(1) - signals to re-evaluate config immediately
}

// New constructs a Cleaner with given initial configuration.
func New(db *database.Store, cfg Config) *Cleaner {
	return &Cleaner{
		db:   db,
		cfg:  cfg,
		wake: make(chan struct{}, 1),
	}
}

// UpdateConfig replaces current config and wakes the goroutine
// so it re-evaluates the new retention window without waiting for the next timer.
func (c *Cleaner) UpdateConfig(cfg Config) {
	c.cfgMu.Lock()
	c.cfg = cfg
	c.cfgMu.Unlock()

	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// GetConfig returns current config.
// Used to expose config state to the admin UI via config.GetRuntimeConfig.
func (c *Cleaner) GetConfig() Config {
	c.cfgMu.RLock()
	defer c.cfgMu.RUnlock()
	return c.cfg
}

// config is internal helper that returns current config.
func (c *Cleaner) config() Config {
	c.cfgMu.RLock()
	defer c.cfgMu.RUnlock()
	return c.cfg
}

// Start launches the cleanup loop in a background goroutine.
// The loop runs until ctx is cancelled (SIGTERM/SIGINT).
func (c *Cleaner) Start(ctx context.Context) {
	go c.loop(ctx)
	log.Println("[INFO] cleaner: started")
}

// loop is the core background goroutine.
func (c *Cleaner) loop(ctx context.Context) {
	for {
		cfg := c.config()

		// if disabled, park goroutine until a config changes or server shutdowns.
		if cfg.RetentionDays == 0 {
			select {
			case <-ctx.Done():
				log.Println("[INFO] cleaner: stopped")
				return
			case <-c.wake:
				// config changed - re-evaluate it at the top
				continue
			}
		}

		retention := time.Duration(cfg.RetentionDays) * 24 * time.Hour
		cutoff := time.Now().UTC().Add(-retention)

		// delete all notifications that are past the retention window
		deleted, err := c.db.RemoveExpiredNotifications(ctx, cutoff)
		if err != nil {
			log.Printf("[ERROR] cleaner: delete expired notifications: %v", err)
		} else if deleted > 0 {
			log.Printf("[INFO] cleaner: deleted %d expired notification(s) (retention: %d days)", deleted, cfg.RetentionDays)
		}

		// find the oldest remaining notification to know when to wake up
		oldest, err := c.db.QueryOldestNotificationCreatedAt(ctx)
		var sleepDur time.Duration
		if err != nil || oldest.IsZero() {
			// table is empty - wake up in one full retention period from now
			sleepDur = retention
		} else {
			sleepDur = time.Until(oldest.Add(retention))
			if sleepDur <= 0 {
				// guard: avoid negative or zero sleepDur which would result in busy loop
				sleepDur = time.Second
			}
		}

		log.Printf("[INFO] cleaner: next cleanup in %s", sleepDur.Round(time.Second))
		timer := time.NewTimer(sleepDur)

		select {
		case <-ctx.Done():
			// context cancell (graceful shutdown)
			timer.Stop()
			log.Println("[INFO] cleaner: stopped")
			return
		case <-c.wake:
			// admin changed the retention config
			timer.Stop()
			continue
		case <-timer.C:
			// timer fired
			continue
		}
	}
}
