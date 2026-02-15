package users

import (
	"context"
	"errors"

	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
)

// Resolves (issuer, subject) -> internal user ID
// If no user account is found, it creates a new user + account mapping
func GetUser(ctx context.Context, db *database.Store, provider *auth.Provider, claims auth.UserClaims) (int64, error) {
	const op = "users.GetUser"

	// try to find existing account by (issuer, subject)
	var userID int64
	accountRow, err := db.QueryAccountByIssuerSub(ctx, provider.Issuer, claims.Subject)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			// account was not found -> create user and account for this subject
			userID, err = createNewUser(ctx, db, provider, claims)
			if err != nil {
				return 0, err
			}
			return userID, nil
		}
		return 0, appError.NewAppError(op, appError.Internal, "failed to load user account", err)
	}
	// account found -> return existing user ID
	return accountRow.UserID, nil
}

// Creates new internal user with external account mapped to it
func createNewUser(ctx context.Context, db *database.Store, provider *auth.Provider, claims auth.UserClaims) (int64, error) {
	const op = "users.createNewUser"

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
		return 0, appError.NewAppError(op, appError.Internal, "failed to create user", err)
	}

	return userID, nil
}
