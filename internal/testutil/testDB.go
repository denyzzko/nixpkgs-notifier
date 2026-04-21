// Package testutil provides shared resources for integration tests.
// Uses github.com/testcontainers/testcontainers-go/modules/postgres that required Docker to be running.
package testutil

import (
	"context"
	"fmt"

	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// StartTestDB starts PostgreSQL container, applies all migrations,
// and returns Store and cleanup function that stops the container.
func StartTestDB() (*database.Store, func()) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		panic(fmt.Sprintf("testutil.StartTestDB: start container: %v", err))
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic(fmt.Sprintf("testutil.StartTestDB: connection string: %v", err))
	}

	store, err := database.Open(ctx, dsn)
	if err != nil {
		panic(fmt.Sprintf("testutil.StartTestDB: open store: %v", err))
	}

	if err := store.RunMigrations(ctx); err != nil {
		panic(fmt.Sprintf("testutil.StartTestDB: run migrations: %v", err))
	}

	cleanup := func() {
		store.Close()
		_ = container.Terminate(ctx)
	}

	return store, cleanup
}
