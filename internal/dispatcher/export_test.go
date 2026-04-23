package dispatcher

// This file is only compiled during tests.
// It exposes internals so tests can:
//   - inject fake senders via NewWithSenders
//   - trigger dispatch cycle synchronously via Dispatcher.Dispatch

import (
	"context"

	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/notify"
)

// NewWithSenders constructs Dispatcher with given senders.
// Intended only for use in tests (for normal use always call New()).
func NewWithSenders(db *database.Store, cfg Config, emailSender notify.Sender, webhookSender notify.Sender) *Dispatcher {
	return &Dispatcher{
		db:            db,
		emailSender:   emailSender,
		webhookSender: webhookSender,
		cfg:           cfg,
	}
}

// Dispatch exposes private dispatch method for tests.
// It runs one full delivery cycle (same as loop does on each tick).
func (d *Dispatcher) Dispatch(ctx context.Context, cfg Config) {
	d.dispatch(ctx, cfg)
}
