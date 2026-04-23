package checker

// This file is only compiled during tests.
// It exposes internals so tests can:
//  - trigger dispatch cycle synchronously via Checker.Dispatch
// 	- read queue lengths without starting workers via Checker.HighQLen and Checker.LowQLen

import (
	"context"
)

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
