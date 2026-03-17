package main

import (
	"fmt"
	"os/exec"
	"syscall"
)

func hideConsoleWindow(cmd *exec.Cmd) {
	// CREATE_NEW_PROCESS_GROUP so we can send CTRL_BREAK to just this process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000 | syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

func killProcessTree(pid int) {
	// Send CTRL_BREAK_EVENT for graceful shutdown
	dll, _ := syscall.LoadDLL("kernel32.dll")
	if dll != nil {
		proc, _ := dll.FindProc("GenerateConsoleCtrlEvent")
		if proc != nil {
			proc.Call(syscall.CTRL_BREAK_EVENT, uintptr(pid))
		}
	}

	// Also force kill with taskkill (hidden window) as fallback
	cmd := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid), "/T", "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
	cmd.Run()
}
