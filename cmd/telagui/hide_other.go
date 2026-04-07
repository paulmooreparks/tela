//go:build !windows

package main

import (
	"fmt"
	"os/exec"
)

func hideConsoleWindow(cmd *exec.Cmd) {
	// No-op on non-Windows platforms
}

func killProcessTree(pid int) {
	// Kill the process group
	exec.Command("kill", "--", fmt.Sprintf("-%d", pid)).Run()
}
