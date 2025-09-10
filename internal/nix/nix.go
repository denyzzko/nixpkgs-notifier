package nix

import (
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

func GetNixPackageByName(name string) {
	fmt.Printf("returning nix package with name %s", name)
}

func GetNixPackageBatch() {

}
