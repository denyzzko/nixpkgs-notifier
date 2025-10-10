package nix

import (
	"fmt"
	"os/exec"
	"strings"
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

func GetNixPackageVersionByName(name string) (string, error) {
	// build expression with package name and specified git branch
	// TODO: branch parameter
	args := []string{
		"eval",
		"--raw",
		"--extra-experimental-features",
		"nix-command flakes",
		`github:NixOS/nixpkgs/nixos-25.05#` + name + `.version`,
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

func GetNixPackageVersionBatch() {

}
