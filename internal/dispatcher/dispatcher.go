// Package dispatcher implements the background notification delivery loop
//
// It periodically takes notifications out of database
// whose status is "pending" or "failed" (failed only if AttemptCount < MaxRetries)
// It delivers each notification using worker pool (number of workers can be configured by env var)
// On success marks notification as "sent", on failure it marks it as "failed"
// (optionally it automatically disables channel after MaxRetries is reached - configurable by env var)
//
// Dispatcher just logs errors because of fire-and-forget mechanism
package dispatcher

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/notify"
)

// Dispatcher variables (config) that can be altered by admin of the system
// Loaded from env on startup, replaceable at runtime through the admin interface
type Config struct {
	Interval            time.Duration // how often to poll for pending/failed notifications
	MaxRetries          int           // max delivery attempts before giving up and leaving notification in failed state
	WorkerCount         int           // max concurrent deliveries (number of go routines that deliver notification)
	DisableOnMaxRetries bool          // when true, notification channel will be automatically disabled after reaching MaxRetries failures
}

// Email variables (config) loaded from env on startup
type EmailConfig struct {
	Provider  string // "resend" or "smtp"
	ResendKey string
	FromAddr  string
	SMTPHost  string
	SMTPPort  string
	SMTPUser  string
	SMTPPass  string
	SMTPFrom  string
}

// Dispatcher with all resources it needs
// It is created once in main.go on startup
type Dispatcher struct {
	db            *database.Store
	emailSender   notify.Sender // resend or smtp
	webhookSender notify.Sender
	cfg           Config
	cfgMu         sync.RWMutex // config guard mutex
}

// Constructs a Dispatcher
func New(db *database.Store, cfg Config, emailCfg EmailConfig) *Dispatcher {
	var emailSender notify.Sender
	if emailCfg.Provider == "smtp" {
		emailSender = notify.NewSMTPSender(emailCfg.SMTPHost, emailCfg.SMTPPort, emailCfg.SMTPUser, emailCfg.SMTPPass, emailCfg.SMTPFrom)
	} else {
		emailSender = notify.NewResendSender(emailCfg.ResendKey, emailCfg.FromAddr)
	}

	return &Dispatcher{
		db:            db,
		emailSender:   emailSender,
		webhookSender: notify.NewWebhookSender(),
		cfg:           cfg,
	}
}

// Config helper that replaces config at runtime
func (d *Dispatcher) UpdateConfig(cfg Config) {
	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	d.cfg = cfg
}

// Config helper that returns current config
func (d *Dispatcher) config() Config {
	d.cfgMu.RLock()
	defer d.cfgMu.RUnlock()
	return d.cfg
}

// GetConfig returns the current dispatcher configuration.
// Used to get access to dispatcher config from other packages (config Manager).
func (d *Dispatcher) GetConfig() Config {
	return d.config()
}

// Config helper that returns the currently configured maximum delivery attempts
func (d *Dispatcher) MaxRetries() int {
	return d.config().MaxRetries
}

// Launches the dispatch loop in a background goroutine
// The loop runs until ctx is cancelled (SIGTERM/SIGINT)
func (d *Dispatcher) Start(ctx context.Context) {
	go d.loop(ctx)
	log.Println("[INFO] dispatcher: started")
}

// Core background goroutine
// Uses time.Ticker to wake up at configured Interval and call dispatch
func (d *Dispatcher) loop(ctx context.Context) {
	cfg := d.config()
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// context cancell (graceful shutdown)
			log.Println("[INFO] dispatcher: stopped")
			return
		case <-ticker.C:
			// re-read config and call dispatch
			cfg = d.config()
			ticker.Reset(cfg.Interval)
			d.dispatch(ctx, cfg)
		}
	}
}

// Fetches all pending/failed notifications from database and delivers concurrently
// Uses a semaphore to limit the number of goroutines running at the same time (cfg.WorkerCount)
func (d *Dispatcher) dispatch(ctx context.Context, cfg Config) {
	// query all pending/failed notifications (attempt_count < MaxRetries)
	pending, err := d.db.QueryPendingFailedNotifications(ctx, cfg.MaxRetries)
	if err != nil {
		log.Printf("[ERROR] dispatcher: query pending: %v", err)
		return
	}
	if len(pending) == 0 {
		return
	}

	log.Printf("[INFO] dispatcher: processing %d notification(s)", len(pending))

	// --GO SEMAPHORE PATTER WITH BUFFERED CHANNEL--
	// idea: https://dev.to/siffiyan_assauri_51ec6d1b/controlling-concurrency-in-go-with-the-semaphore-pattern-24in
	//
	// sem is a buffered channel used as a semaphore (capacity equals WorkerCount — max number of goroutines allowed to run at once)
	// sending into sem acquires a slot, receiving from sem releases it
	// if all slots are taken, the next send blocks until one goroutine finishes and releases its slot
	sem := make(chan struct{}, cfg.WorkerCount)
	var wg sync.WaitGroup

	for _, n := range pending {
		wg.Add(1)         // increment waitgroup
		sem <- struct{}{} // acquire a slot (blocks if WorkerCount goroutines are already running)

		go func(n database.PendingFailedNotification) {
			defer wg.Done()          // schedule a decrement of waitgroup when this goroutine finishes
			defer func() { <-sem }() // schedule a release of the slot when this goroutine finishes (wrapped in function so it actually gets executed after outer function returns)
			d.deliver(ctx, n, cfg)   // delivery of notification
		}(n) // pass n (notification) as argument to avoid the loop variable capture problem (basically creates copy/snapshot of n for that goroutine)
	}

	// wait for all goroutine deliveries to finish before returning
	// (wait for waitgroup to be 0)
	wg.Wait()
}

// Handles one notification delivery attempt
// Resolves apporpriate Sender, calls sender.Send, marks sent/failed and if configured disables channel on max retries
func (d *Dispatcher) deliver(ctx context.Context, n database.PendingFailedNotification, cfg Config) {
	// resolve correct Sender (resend/smtp/webhook)
	sender, event, err := d.resolveSender(n)
	if err != nil {
		log.Printf("[ERROR] dispatcher: resolve sender (id=%d): %v", n.ID, err)
		_ = d.db.MarkNotificationFailed(ctx, n.ID, "internal server error")
		return
	}

	// notification delivery attempt
	err = sender.Send(ctx, event)
	if err != nil {
		// send failed -> mark as failed and increment attempt_count
		log.Printf("[WARN] dispatcher: send failed (id=%d, attempt=%d): %v", n.ID, n.AttemptCount+1, err)
		// if returned error is SenderError use its public version, otherwise fallback to just "internal server error"
		dbMsg := notify.PublicMessage(err)
		_ = d.db.MarkNotificationFailed(ctx, n.ID, dbMsg)
		// if DisableOnMaxRetries is on -> disable channel (is_enabled = false, disabled_by_server = true)
		if cfg.DisableOnMaxRetries && n.AttemptCount+1 >= cfg.MaxRetries {
			log.Printf("[WARN] dispatcher: disabling channel %d after %d failed attempts", n.ChannelID, n.AttemptCount+1)
			_ = d.db.DisableChannelByServer(ctx, n.ChannelID, n.UserID)
		}
		return
	}

	// send success -> mark as sent and update tracking's last_notified_version
	err = d.db.MarkNotificationSent(ctx, n.ID, n.UserID, n.PackageID, n.NewVersion)
	if err != nil {
		log.Printf("[ERROR] dispatcher: mark sent (id=%d): %v", n.ID, err)
		return
	}

	log.Printf("[INFO] dispatcher: sent notification %d (%s %s -> %s)", n.ID, n.PackageName, n.OldVersion, n.NewVersion)
}

// Decides which delivery mechanism to use based on notification and picks appropriate notify.Sender
//
// If EmailAdress != nil -> email channel
// If WebhookUrl != nil -> webhook channel
func (d *Dispatcher) resolveSender(n database.PendingFailedNotification) (notify.Sender, notify.VersionChangeEvent, error) {
	// populate shared event payload from notification db row
	event := notify.VersionChangeEvent{
		PackageName:       n.PackageName,
		PackageBranch:     n.PackageBranch,
		OldVersion:        n.OldVersion,
		NewVersion:        n.NewVersion,
		DetectedAt:        n.DetectedAt,
		IsFirstAppearance: n.OldVersion == "",
	}

	// decide email/webhook
	switch {
	case n.Email != nil:
		// email channel
		event.RecipientAddress = n.Email.Address
		return d.emailSender, event, nil
	case n.Webhook != nil:
		// webhook channel
		event.RecipientAddress = n.Webhook.URL
		event.WebhookType = n.Webhook.Type
		event.WebhookUsername = n.Webhook.Username
		event.WebhookChannel = n.Webhook.Channel
		event.WebhookPriority = n.Webhook.Priority
		event.WebhookRequestAck = n.Webhook.RequestAck
		return d.webhookSender, event, nil
	// fallback
	default:
		return nil, event, fmt.Errorf("notification %d has no email or webhook address", n.ID)
	}
}

// Test sends a test message directly via the channel without creating a notification record
// It is called when user clicks "Test" in UI
// Returns an error if the send fails, nil on success
func (d *Dispatcher) Test(ctx context.Context, channelID int64, email *database.Email, webhook *database.Webhook) error {
	// build a fake notification record just to reuse resolveSender()
	test := database.PendingFailedNotification{
		PackageName:   "test",
		PackageBranch: "test",
		OldVersion:    "0.0.0",
		NewVersion:    "0.0.0",
		DetectedAt:    time.Now().UTC(),
		Email:         email,
		Webhook:       webhook,
	}

	// resolve correct sender (resend/smtp/webhook)
	sender, event, err := d.resolveSender(test)
	if err != nil {
		return fmt.Errorf("dispatcher.Test: resolve sender: %w", err)
	}

	// send message
	err = sender.SendTest(ctx, event)
	if err != nil {
		return fmt.Errorf("dispatcher.Test: send failed: %w", err)
	}

	log.Printf("[INFO] dispatcher: test notification sent for channel %d", channelID)
	return nil
}
