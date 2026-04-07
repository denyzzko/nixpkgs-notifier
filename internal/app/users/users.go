package users

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
)

// user is not authenticated error
var ErrNotAuthenticated = errors.New("not authenticated")

// regex for username
var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

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

// Get user by his ID
func GetUserByID(ctx context.Context, db *database.Store, userID int64) (database.User, error) {
	const op = "users.GetUserByID"

	user, err := db.QueryUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return database.User{}, appError.NewAppError(op, appError.NotFound, "user not found", err)
		}
		return database.User{}, appError.NewAppError(op, appError.Internal, "failed to load user", err)
	}

	return user, nil
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

// UpdateUsername validates and applies a username change for a user.
// Returns error if username is empty, exceeds 50 characters, username is already taken or on DB failure.
func UpdateUsername(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, username string) error {
	const op = "users.UpdateUsername"

	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// trim and validate username
	username = strings.TrimSpace(username)
	if username == "" {
		return appError.NewAppError(op, appError.Invalid, "username cannot be empty", errors.New("empty username"))
	}
	if len(username) > 50 {
		return appError.NewAppError(op, appError.Invalid, "username is too long", errors.New("username longer than 50 characters"))
	}
	if !usernameRe.MatchString(username) {
		return appError.NewAppError(op, appError.Invalid, "username may only contain letters, digits, underscores and hyphens", errors.New("username contains invalid characters"))
	}

	// store username change in db
	if err := db.UpdateUserUsername(ctx, userID, username); err != nil {
		if errors.Is(err, database.ErrUsernameConflict) {
			return appError.NewAppError(op, appError.Conflict, "username is already taken", err)
		}
		return appError.NewAppError(op, appError.Internal, "failed to update username", err)
	}

	return nil
}

// UpdateUsernameAndRole validates and applies username and role changes to a user.
// Returns error if validation or DB update fails.
// Returns error if username is empty, exceeds 50 characters, username is already taken, wrong role or on DB failure.
func UpdateUsernameAndRole(ctx context.Context, db *database.Store, userID int64, username string, role string) (database.User, error) {
	const op = "users.UpdateUsernameAndRole"

	// fetch user (caller needs CreatedAt to re-render the row on error)
	u, err := db.QueryUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return database.User{}, appError.NewAppError(op, appError.NotFound, "user not found", err)
		}
		return database.User{}, appError.NewAppError(op, appError.Internal, "failed to load user", err)
	}

	// trim and validate username
	username = strings.TrimSpace(username)
	if username == "" {
		return u, appError.NewAppError(op, appError.Invalid, "username cannot be empty", errors.New("empty username"))
	}
	if len(username) > 50 {
		return u, appError.NewAppError(op, appError.Invalid, "username must be 50 characters or fewer", errors.New("username longer than 50 characters"))
	}
	if !usernameRe.MatchString(username) {
		return u, appError.NewAppError(op, appError.Invalid, "username may only contain letters, digits, underscores and hyphens", errors.New("username contains invalid characters"))
	}

	// validate role
	if role != "user" && role != "admin" {
		return u, appError.NewAppError(op, appError.Invalid, "invalid role, must be 'user' or 'admin'", errors.New("invalid role value"))
	}

	// store changes in db
	if err := db.UpdateUser(ctx, userID, username, role); err != nil {
		if errors.Is(err, database.ErrUsernameConflict) {
			return u, appError.NewAppError(op, appError.Conflict, fmt.Sprintf("username %q is already taken by another user", username), err)
		}
		return u, appError.NewAppError(op, appError.Internal, "failed to update user", err)
	}

	return u, nil
}
