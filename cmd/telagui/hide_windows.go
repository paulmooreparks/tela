package main

import (
	"fmt"
	"log"
	"os/exec"
	"syscall"
)

// createNoWindow prevents a console window from flashing when launching
// a child process. Combined with CREATE_NEW_PROCESS_GROUP so we can
// send CTRL_BREAK to just this process tree.
const createNoWindow = 0x08000000

func hideConsoleWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

func killProcessTree(pid int) {
	// Send CTRL_BREAK_EVENT for graceful shutdown.
	dll, err := syscall.LoadDLL("kernel32.dll")
	if err != nil {
		log.Printf("[telagui] LoadDLL kernel32: %v", err)
	} else {
		proc, err := dll.FindProc("GenerateConsoleCtrlEvent")
		if err != nil {
			log.Printf("[telagui] FindProc GenerateConsoleCtrlEvent: %v", err)
		} else {
			proc.Call(syscall.CTRL_BREAK_EVENT, uintptr(pid))
		}
	}

	// Force kill with taskkill (hidden window) as fallback.
	cmd := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid), "/T", "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	if err := cmd.Run(); err != nil {
		log.Printf("[telagui] taskkill PID %d: %v", pid, err)
	}
}
