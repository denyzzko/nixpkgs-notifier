package checker

// This file is only compiled during tests.
// It exposes internals so tests can:
//  - inject fake nix evaluation via NewWithNixEval
//  - trigger dispatch cycle synchronously via Checker.Dispatch
// 	- read queue lengths without starting workers via Checker.HighQLen and Checker.LowQLen

import (
	"context"

	"github.com/denyzzko/nixpkgs-notifier/internal/database"
)

// NewWithNixEval constructs Checker with given nix eval function.
// Intended only for use in tests (for normal use always call New()).
func NewWithNixEval(db *database.Store, cfg Config, eval func(ctx context.Context, name, branch string) (string, error)) *Checker {
	c := New(db, cfg)
	c.nixEval = eval
	return c
}

// Dispatch exposes private dispatch method for tests.
// It routes one job through appropriate handler (same as worker does for each job it takes from queue).
func (ch *Checker) Dispatch(ctx context.Context, job CheckJob) {
	ch.dispatch(ctx, job)
}

// HighQLen returns number of jobs currently in the high-priority queue.
// Used in tests to validate EnqueueHigh routing without starting workers.
func (ch *Checker) HighQLen() int {
	return len(ch.highQ)
}

// LowQLen returns number of jobs currently in the low-priority queue.
// Used in tests to validate EnqueueLow routing without starting workers.
func (ch *Checker) LowQLen() int {
	return len(ch.lowQ)
}
