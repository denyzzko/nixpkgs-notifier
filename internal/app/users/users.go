package users

import (
	"context"
	"fmt"

	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
)

func GetUser(ctx context.Context, db *database.Store, provider *auth.Provider, claims auth.UserClaims) (int64, error) {
	// try to find existing account by (issuer, subject)
	var userID int64
	accountRow, err := db.QueryAccountByIssuerSub(ctx, provider.Issuer, claims.Subject)
	if err != nil {
		if err == database.ErrNotFound {
			// account was not found -> create user and account for this subject
			userID, err = createNewUser(ctx, db, provider, claims)
			if err != nil {
				return 0, fmt.Errorf("failed to create user: %w", err)
			}
			return userID, nil
		}
		return 0, fmt.Errorf("failed to query account: %w", err)
	}
	// account found -> return existing user ID
	return accountRow.UserID, nil
}

func createNewUser(ctx context.Context, db *database.Store, provider *auth.Provider, claims auth.UserClaims) (int64, error) {
	userInfo := database.UserInfo{
		Email:         &claims.Email,
		EmailVerified: claims.EmailVerified,
		Username:      &claims.PreferredUsername,
		Role:          "user", // default role for new users
		Provider:      provider.Name,
		Issuer:        provider.Issuer,
		Subject:       claims.Subject,
	}

	userID, err := db.CreateUserWithAccount(ctx, userInfo)
	if err != nil {
		return 0, fmt.Errorf("database error: %w", err)
	}

	return userID, nil
}
