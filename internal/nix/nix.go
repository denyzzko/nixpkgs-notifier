package nix

import (
	"encoding/json"
	"fmt"
	"os/exec"
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
	// build expression with package name
	expr := "(import <nixpkgs> {})."
	expr += name
	expr += ".version"

	// execute nix command
	out, err := exec.Command("nix", "eval", "--extra-experimental-features", "nix-command", "--impure", "--json", "--expr", expr).CombinedOutput()

	// handle error
	if err != nil {
		return "", fmt.Errorf("stderr:\n%s\nstdout:\n%s", err, out)
	}

	// return version
	var version string
	if err := json.Unmarshal(out, &version); err != nil {
		return "", err
	}
	return version, nil
}

func GetNixPackageVersionBatch() {

}
