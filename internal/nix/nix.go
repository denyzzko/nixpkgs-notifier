package nix

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/denyzzko/nixpkgs-notifier/internal/database"
)

var (
	// package attribute not found in nixpkgs for the given ref/branch error
	ErrAttrNotFound = errors.New("nix attribute not found")

	// nix command not available on this system error
	ErrNixUnavailable = errors.New("nix unavailable")

	// all other nix eval failures (network, eval errors, timeouts, etc.) error
	ErrEvalFailed = errors.New("nix eval failed")
)

// Checks if nix --version can be executed (if not -> returns error)
// This function is executed once on server startup
func CheckNixAvailability() error {
	cmd := exec.Command("nix", "--version")
	err := cmd.Run()

	if err != nil {
		return errors.Join(ErrNixUnavailable, err)
	}

	return nil
}

func GetPackageVersionByID(ctx context.Context, db *database.Store, packageID int64) (string, error) {
	//get name and branch from id
	pckg, err := db.QueryPackage(ctx, packageID)
	if err != nil {
		return "", fmt.Errorf("nix.GetPackageVersionByID: query package(id=%d): %w", packageID, err)
	}

	// get version
	pckgVersion, err := GetPackageVersionByNameAndBranch(ctx, pckg.Name, pckg.Branch)
	if err != nil {
		return "", fmt.Errorf("nix.GetPackageVersionByID: nix eval (name=%q, branch=%q): %w", pckg.Name, pckg.Branch, err)
	}

	// return version
	return pckgVersion, nil
}

func GetPackageVersionByNameAndBranch(ctx context.Context, name string, branch string) (string, error) {
	// build expression with package name and specified git branch
	args := []string{
		"eval",
		"--raw",
		"--extra-experimental-features",
		"nix-command flakes",
		`github:NixOS/nixpkgs/` + branch + `#` + name + `.version`,
	}

	// execute nix command
	cmd := exec.Command("nix", args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()

	// return version
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	// handle timed-out/canceled context as eval failure (upstream)
	if ctx.Err() != nil {
		return "", errors.Join(ErrEvalFailed, fmt.Errorf("nix.GetPackageVersionByNameAndBranch: %v", ctx.Err()))
	}

	// Return error closer classified to distinguish between invalid request and upstream/network errors
	return "", classifyNixError(err, stderr.String(), name, branch)
}

func classifyNixError(err error, stderr, name, branch string) error {
	s := stderr
	if strings.TrimSpace(s) == "" {
		s = err.Error()
	}

	// invalid request (wrong package name or branch)
	if strings.Contains(s, "does not provide attribute") ||
		strings.Contains(s, "HTTP error 422") ||
		strings.Contains(s, "No commit found for SHA") {
		return errors.Join(ErrAttrNotFound, fmt.Errorf("nix eval invalid (name=%q, branch=%q): %s", name, branch, strings.TrimSpace(s)))
	}

	// upstream (network errors)
	if strings.Contains(s, "unable to download") ||
		strings.Contains(s, "Timeout was reached") ||
		strings.Contains(s, "Resolving timed out") ||
		strings.Contains(s, "Connection timed out") ||
		(strings.Contains(s, "HTTP error") && !strings.Contains(s, "HTTP error 422")) {
		return errors.Join(ErrEvalFailed, fmt.Errorf("nix eval upstream (name=%q, branch=%q): %s", name, branch, strings.TrimSpace(s)))
	}

	// fallback
	return errors.Join(ErrEvalFailed, fmt.Errorf("nix eval failed (name=%q, branch=%q): %s", name, branch, strings.TrimSpace(s)))
}
