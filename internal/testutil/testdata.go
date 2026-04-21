// Package testutil provides shared resources for integration tests.
// Uses github.com/testcontainers/testcontainers-go/modules/postgres that required Docker to be running.
package testutil

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/denyzzko/nixpkgs-notifier/internal/database"
)

// rowCounter generates unique values for tests so they will never
// collide on uniqueness constraints.
var rowCounter atomic.Int64

// NextID returns a unique integer for use in test data generation.
func NextID() int64 {
	return rowCounter.Add(1)
}

// CreateTestUser inserts user into database and returns their details.
// Each call generates unique username and OIDC subject.
func CreateTestUser(t *testing.T, store *database.Store, role string) (userID int64, issuer string, subject string) {
	t.Helper()

	n := NextID()
	issuer = "https://test.issuer"
	subject = fmt.Sprintf("sub-%d", n)
	username := fmt.Sprintf("testuser%d", n)

	info := database.UserInfo{
		Username: &username,
		Role:     role,
		Provider: "test",
		Issuer:   issuer,
		Subject:  subject,
	}

	id, err := store.CreateUserWithAccount(context.Background(), info)
	if err != nil {
		t.Fatalf("testutil.CreateTestUser: %v", err)
	}

	return id, issuer, subject
}
