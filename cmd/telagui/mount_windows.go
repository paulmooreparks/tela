package main

import (
	"log"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	tvMpr                   = windows.NewLazySystemDLL("mpr.dll")
	tvProcWNetCancelConn2   = tvMpr.NewProc("WNetCancelConnection2W")
)

// platformUnmountDrive unmaps a Windows drive letter using the WNet API.
// Called before killing the mount process to prevent orphaned mappings.
func platformUnmountDrive(mountPoint string) {
	if mountPoint == "" {
		return
	}
	// Normalize: single letter -> letter + colon
	if len(mountPoint) == 1 {
		mountPoint = strings.ToUpper(mountPoint) + ":"
	}

	name, err := windows.UTF16PtrFromString(strings.ToUpper(mountPoint))
	if err != nil {
		return
	}

	log.Printf("[telavisor] unmapping drive %s", mountPoint)
	ret, _, _ := tvProcWNetCancelConn2.Call(
		uintptr(unsafe.Pointer(name)),
		0, // no flags
		1, // force
	)
	if ret != 0 {
		log.Printf("[telavisor] WNetCancelConnection2 %s: error %d", mountPoint, ret)
	}
}
