package main

import (
	"fmt"
	"os/exec"
	"syscall"
)

func hideConsoleWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}

func killProcessTree(pid int) {
	// taskkill /T kills the process and all child processes
	exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid), "/T", "/F").Run()
}
