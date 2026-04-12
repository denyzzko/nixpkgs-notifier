// Package users implements all business logic related to user and account (OIDC identity) management.
//
// It handles resolving OIDC identities to internal users,
// different user operations such as creating user or updating his username or role
// and linking/unlinking accounts.
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
		if errors.Is(err, database.ErrConflict) {
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
		if errors.Is(err, database.ErrConflict) {
			return u, appError.NewAppError(op, appError.Conflict, fmt.Sprintf("username %q is already taken by another user", username), err)
		}
		return u, appError.NewAppError(op, appError.Internal, "failed to update user", err)
	}

	return u, nil
}

// LinkNewAccount links a freshly-authenticated OIDC identity to an existing internal user.
//
// Flow: the user is already logged in (linkingUserID), completes another OIDC flow,
// that new (issuer, subject) account is attached to the existing user (linkingUserID) instead of
// creating a new one.
//
// Returns appError.Conflict if the identity is already linked to any user.
func LinkNewAccount(ctx context.Context, db *database.Store, provider *auth.Provider, claims auth.UserClaims, linkingUserID int64) error {
	const op = "users.LinkNewAccount"

	// email is optional
	var emailPtr *string
	if claims.Email != "" {
		emailPtr = &claims.Email
	}

	// insert new account pointing at the existing logged-in user
	err := db.CreateLinkedAccount(ctx, linkingUserID, emailPtr, claims.EmailVerified, provider.Name, provider.Issuer, claims.Subject)
	if err != nil {
		if errors.Is(err, database.ErrConflict) {
			return appError.NewAppError(op, appError.Conflict,
				"this account is already linked to a user", fmt.Errorf("duplicate account (issuer=%q, subject=%q)", provider.Issuer, claims.Subject))
		}
		return appError.NewAppError(op, appError.Internal, "failed to link account", err)
	}
	return nil
}

// LinkExistingAccount moves OIDC account the user just signed in with
// to the currently logged-in user (targetUserID).
//
// If the source user still has other accounts after the move, only that one account
// is transferred and their data is left untouched.
// If the source user becomes orphaned, their trackings and channels are merged into
// target and the source user is deleted.
//
// Returns the final role of the target user (may have been promoted to admin).
// Returns appError.Conflict if the account already belongs to targetUserID.
// Returns appError.NotFound if the identity is not linked to any existing user.
func LinkExistingAccount(ctx context.Context, db *database.Store, provider *auth.Provider, claims auth.UserClaims, targetUserID int64) (string, error) {
	const op = "users.LinkExistingAccount"

	// resolve (issuer, subject) to source user
	acc, err := db.QueryAccountByIssuerSub(ctx, provider.Issuer, claims.Subject)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return "", appError.NewAppError(op, appError.NotFound,
				"the account you signed into is not linked to any existing user; use 'Link new account' instead",
				fmt.Errorf("no account found for (issuer=%q, subject=%q)", provider.Issuer, claims.Subject))
		}
		return "", appError.NewAppError(op, appError.Internal, "failed to look up account", err)
	}

	// guard: account already belongs to the target
	if acc.UserID == targetUserID {
		return "", appError.NewAppError(op, appError.Conflict,
			"this account is already linked to your user",
			fmt.Errorf("account (issuer=%q, subject=%q) already belongs to target user (id=%d)", provider.Issuer, claims.Subject, targetUserID))
	}

	// fetch both users to make all decisions before DB call
	sourceUser, err := db.QueryUserByID(ctx, acc.UserID)
	if err != nil {
		return "", appError.NewAppError(op, appError.Internal, "failed to load source user", err)
	}
	targetUser, err := db.QueryUserByID(ctx, targetUserID)
	if err != nil {
		return "", appError.NewAppError(op, appError.Internal, "failed to load target user", err)
	}

	// get all accounts the source has so they can be counted -> if this is the only one, source will become orphaned
	sourceAccounts, err := db.QueryAccountsByUserID(ctx, acc.UserID)
	if err != nil {
		return "", appError.NewAppError(op, appError.Internal, "failed to load source accounts", err)
	}

	// true if source becomes orphaned -> merge their data into target
	mergeData := len(sourceAccounts) == 1
	// true if mergeData, source was admin and target is not -> target will be promoted to admin
	promoteToAdmin := mergeData && sourceUser.Role == "admin" && targetUser.Role != "admin"

	// resolve final role so it can be returned to the caller
	finalRole := targetUser.Role
	if promoteToAdmin {
		finalRole = "admin"
	}

	// apply the move (and optional data merge)
	err = db.ApplyExistingAccountLink(ctx, database.AccountLinkParams{
		TargetUserID:   targetUserID,
		SourceUserID:   acc.UserID,
		Issuer:         provider.Issuer,
		Subject:        claims.Subject,
		MergeData:      mergeData,
		PromoteToAdmin: promoteToAdmin,
	})
	if err != nil {
		return "", appError.NewAppError(op, appError.Internal, "failed to merge account", err)
	}

	return finalRole, nil
}

// GetAccounts returns all OIDC accounts linked to the current user.
func GetAccounts(ctx context.Context, db *database.Store, sessionManager *session.SessionManager) ([]database.Account, error) {
	const op = "users.GetAccounts"

	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return nil, appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// fetch all accounts linked to this user
	accs, err := db.QueryAccountsByUserID(ctx, userID)
	if err != nil {
		return nil, appError.NewAppError(op, appError.Internal, "failed to load accounts", err)
	}

	return accs, nil
}

// UnlinkAccount removes a single OIDC account from the current user.
// Returns appError.Conflict if the account is user's only remaining login method.
func UnlinkAccount(ctx context.Context, db *database.Store, sessionManager *session.SessionManager, issuer, subject string) error {
	const op = "users.UnlinkAccount"

	// get user ID
	userID := sessionManager.GetUserID(ctx)
	if userID == 0 {
		return appError.NewAppError(op, appError.Unauthenticated, "not authenticated", ErrNotAuthenticated)
	}

	// remove the account identified by (issuer, subject) from this user
	if err := db.DeleteAccountByIssuerSub(ctx, userID, issuer, subject); err != nil {
		if errors.Is(err, database.ErrLastAccount) {
			return appError.NewAppError(op, appError.Conflict, "cannot remove your only login method", err)
		}
		return appError.NewAppError(op, appError.Internal, "failed to unlink account", err)
	}

	return nil
}
