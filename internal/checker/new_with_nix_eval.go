package checker

// Thios files exists  only to

import (
	"context"

	"github.com/denyzzko/nixpkgs-notifier/internal/database"
)

// NewWithNixEval constructs Checker with a given nix eval function.
// Intended for use in tests only (for production use always call New()).
func NewWithNixEval(db *database.Store, cfg Config, eval func(ctx context.Context, name, branch string) (string, error)) *Checker {
	c := New(db, cfg)
	c.nixEval = eval
	return c
}
