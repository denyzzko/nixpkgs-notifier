package nix

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/denyzzko/nixpkgs-notifier/internal/database"
)

func CheckNixAvailability() bool {
	cmd := exec.Command("nix", "--version")
	stdout, err := cmd.Output()

	if err != nil {
		fmt.Println(err.Error())
		return false
	}

	// print output
	fmt.Println(string(stdout))
	return true
}

func GetPackageVersionByID(ctx context.Context, db *database.Store, packageID int64) (string, error) {
	//get name and branch from id
	pckg, err := db.QueryPackage(ctx, packageID)
	if err != nil {
		if err == database.ErrNotFound {
			// this package was not found
			return "", fmt.Errorf("package not found")
		} else {
			return "", fmt.Errorf("failed to get package: %w", err)
		}
	}

	// get version
	pckgVersion, err := GetPackageVersionByNameAndBranch(pckg.Name, pckg.Branch)
	if err != nil {
		return "", fmt.Errorf("failed to get package version from Nix: %w", err)
	}

	// return version
	return pckgVersion, nil
}

func GetPackageVersionByNameAndBranch(name string, branch string) (string, error) {
	// build expression with package name and specified git branch
	args := []string{
		"eval",
		"--raw",
		"--extra-experimental-features",
		"nix-command flakes",
		`github:NixOS/nixpkgs/` + branch + `#` + name + `.version`,
	}

	// execute nix command
	out, err := exec.Command("nix", args...).Output()

	// handle error
	if err != nil {
		fmt.Println(strings.TrimSpace(string(err.Error())))
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("stderr:\n%s\nstdout:\n%s", err, out)
		}
		return "", err
	}

	// return version
	return strings.TrimSpace(string(out)), nil
}
