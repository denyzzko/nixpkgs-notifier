// Package nix handles two concerns: evaluating package versions via nix CLI,
// and maintaining an in-memory list of common nixpkgs branches fetched from GitHub.
//
// Version evaluation: GetPackageVersionByNameAndBranch spawns a nix eval subprocess
// for a given package name and branch. Concurrent calls for the same name+branch pair
// are automatically coalesced via singleflight so that only one subprocess runs
// at a time and all callers share its result.
// Errors are classified into three sentinel values:
//   - ErrAttrNotFound: the package name or branch is invalid
//   - ErrNixUnavailable: the nix binary is not present on this system
//   - ErrEvalFailed: all other failures (network, timeout, unexpected nix error)
//
// Branch list: StartBranchFetcher launches a background goroutine that refreshes
// common nixpkgs branches from the GitHub API every 24h. GetCommonBranches returns
// current list (falls back to a hardcoded default if API is unreachable).
package nix

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Shared HTTP client for GitHub API calls (with 10s timeout).
var branchHTTPClient = &http.Client{Timeout: 10 * time.Second}

// Guards commonBranches against concurrent read/write.
var branchMu sync.RWMutex

// in-memory list of branches that is used in Packages screen when selecting a branch.
// Initialized to hardcoded fallback (replaced after first successful GitHub API fetch).
var commonBranches = []string{
	"nixos-unstable",
	"nixos-25.05", "nixos-24.11", "nixos-24.05",
	"release-25.05", "release-24.11", "release-24.05",
}

// Matches branch names like nixos-25.11 or release-24.11.
// Rejects suffixes like -small, -darwin, -aarch64, ...
var versionedBranchRe = regexp.MustCompile(`^(\w+)-(\d+)\.(\d+)$`)

// single ref from the GitHub API response
type gitRef struct {
	Ref string `json:"ref"`
}

// versionedBranch holds a branch name with parsed version.
// Used for numeric comparison in filterAndSort to sort branches descending
type versionedBranch struct {
	name  string
	major int
	minor int
}

// GetCommonBranches returns a copy of the current commonBranches list.
func GetCommonBranches() []string {
	branchMu.RLock()
	defer branchMu.RUnlock()
	result := make([]string, len(commonBranches))
	copy(result, commonBranches)
	return result
}

// StartBranchFetcher launches a background goroutine that refreshes branches every 24h.
func StartBranchFetcher(ctx context.Context) {
	go refreshLoop(ctx)
	log.Println("[INFO] branch fetcher: started")
}

// refreshLoop is background goroutine loop.
// It fetches branches on startup and then every 24h again.
// Keeps last known list on failure.
func refreshLoop(ctx context.Context) {
	// fetch immediately on startup
	branches, err := fetchBranches(ctx)
	if err != nil {
		log.Printf("[WARN] branch fetcher: initial fetch failed: %v", err)
	} else {
		branchMu.Lock()
		commonBranches = branches
		branchMu.Unlock()
	}

	// refresh list on every tick (24h)
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("[INFO] branch fetcher: stopped")
			return
		case <-ticker.C:
			branches, err := fetchBranches(ctx)
			if err != nil {
				log.Printf("[WARN] branch fetcher: refresh failed: %v", err)
			} else {
				branchMu.Lock()
				commonBranches = branches
				branchMu.Unlock()
			}
		}
	}
}

// fetchBranches builds combined list.
// First is nixos-unstable, then top 5 nixos-*, then top 5 release-*  ordered by version descending.
func fetchBranches(ctx context.Context) ([]string, error) {
	// fetch all nixos-* refs
	nixos, err := fetchMatchingRefs(ctx, "nixos-")
	if err != nil {
		return nil, err
	}

	// fetch all release-* refs
	release, err := fetchMatchingRefs(ctx, "release-")
	if err != nil {
		return nil, err
	}

	// filter names, sort by version and take top 5 from each group
	top5nixos := filterAndSort(nixos, 5)
	top5release := filterAndSort(release, 5)

	// build the list and return it
	result := []string{"nixos-unstable"}
	result = append(result, top5nixos...)
	result = append(result, top5release...)
	return result, nil
}

// fetchMatchingRefs calls GitHub matching-refs endpoint for a branch prefix and returns branch names.
func fetchMatchingRefs(ctx context.Context, prefix string) ([]string, error) {
	// build GitHub matching-refs URL for specific given prefix
	url := "https://api.github.com/repos/NixOS/nixpkgs/git/matching-refs/heads/" + prefix
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	// execute request
	resp, err := branchHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// decode JSON array of ref objects
	var refs []gitRef
	err = json.NewDecoder(resp.Body).Decode(&refs)
	if err != nil {
		return nil, err
	}

	// strip refs/heads/ prefix from each entry
	var names []string
	for _, r := range refs {
		name := strings.TrimPrefix(r.Ref, "refs/heads/")
		names = append(names, name)
	}

	return names, nil
}

// filterAndSort keeps only clean versioned branches (e.g. nixos-25.11 but not nixos-25.11-small).
// Sorts descending by version, and returns up to n branches.
func filterAndSort(branches []string, n int) []string {
	// collect only branches matching the versionedBranchRe regex
	var versioned []versionedBranch
	for _, b := range branches {
		m := versionedBranchRe.FindStringSubmatch(b)
		if m == nil {
			continue
		}
		major, _ := strconv.Atoi(m[2])
		minor, _ := strconv.Atoi(m[3])
		versioned = append(versioned, versionedBranch{b, major, minor})
	}

	// sort descending
	sort.Slice(versioned, func(i, j int) bool {
		if versioned[i].major != versioned[j].major {
			return versioned[i].major > versioned[j].major
		}
		return versioned[i].minor > versioned[j].minor
	})

	// truncate to max n results
	if len(versioned) > n {
		versioned = versioned[:n]
	}

	// extract names and return them
	result := make([]string, len(versioned))
	for i, v := range versioned {
		result[i] = v.name
	}

	return result
}
