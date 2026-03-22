// Nix is package that handles nix CLI for evaluating package versions from nixpkgs
//
// This package exposes a single public function, GetPackageVersionByNameAndBranch(), which spawns a
// nix eval subprocess for a given package name and branch
// Concurrent calls for the same name+branch pair are automatically coalesced via singleflight so that only one subprocess runs
// at a time and all callers share its result
//
// Errors are classified into three sentinel values:
//   - ErrAttrNotFound: the package name or branch is invalid
//   - ErrNixUnavailable: the nix binary is not present on this system
//   - ErrEvalFailed: all other failures (network, timeout, unexpected nix error)
package nix

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"golang.org/x/sync/singleflight"
)

var (
	// package attribute not found in nixpkgs for the given ref/branch
	ErrAttrNotFound = errors.New("nix attribute not found")

	// nix command not available on this system
	ErrNixUnavailable = errors.New("nix unavailable")

	// all other nix eval failures (network, eval errors, timeouts, etc.)
	ErrEvalFailed = errors.New("nix eval failed")
)

// nixEvalGroup coalesces concurrent nix eval calls for the same package+branch pair
// If N goroutines request the same key(package+branch) at the same time, only one nix subprocess is spawned
// all N callers receive the same result once it completes
var nixEvalGroup singleflight.Group

// validNixName matches allowed characters in nix package names and branch names
var validNixName = regexp.MustCompile(`^[a-zA-Z0-9._\-]+$`)

// Checks if nix binary is available by running nix --version (if not -> returns error)
// This function is executed once on server startup
func CheckNixAvailability() error {
	cmd := exec.Command("nix", "--version")
	err := cmd.Run()

	if err != nil {
		return errors.Join(ErrNixUnavailable, err)
	}

	return nil
}

// Runs nix eval to fetch the current version of a package from a specific nixpkgs branch
//
// Concurrent calls for the same name+branch are automatically coalesced using singleflight
// Only one nix subprocess runs, all waiting callers share its result
func GetPackageVersionByNameAndBranch(ctx context.Context, name string, branch string) (string, error) {
	key := name + "@" + branch

	v, err, _ := nixEvalGroup.Do(key, func() (interface{}, error) {
		return evalNix(ctx, name, branch)
	})

	if err != nil {
		return "", err
	}
	return v.(string), nil
}

// Runs actual nix eval call for the given package name and branch
// It is only called once per unique name+branch key at a time (enforced by nixEvalGroup)
func evalNix(ctx context.Context, name string, branch string) (string, error) {
	// validate name and branch to prevent undesired behavior
	if !validNixName.MatchString(name) || !validNixName.MatchString(branch) {
		return "", ErrAttrNotFound
	}

	// build expression with package name and specified git branch
	args := []string{
		"eval",
		"--raw",
		"--extra-experimental-features",
		"nix-command flakes",
		`github:NixOS/nixpkgs/` + branch + `#` + name + `.version`,
	}

	// execute nix command
	cmd := exec.CommandContext(ctx, "nix", args...)

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

// Maps a nix eval failure to one of the package sentinel errors
// It inspects stderr output and returns:
//   - ErrAttrNotFound for invalid package name or branch (HTTP 422, missing attribute, unknown commit)
//   - ErrEvalFailed for network and timeout errors ( also fallback for anything else)
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
