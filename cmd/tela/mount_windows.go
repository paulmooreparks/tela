package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	mpr                    = windows.NewLazySystemDLL("mpr.dll")
	procWNetAddConnection2 = mpr.NewProc("WNetAddConnection2W")
	procWNetCancelConn2    = mpr.NewProc("WNetCancelConnection2W")
	procWNetGetConnection  = mpr.NewProc("WNetGetConnectionW")
)

const (
	resourceTypeDisk  = 0x1
	resourceConnected = 0x1
)

// NETRESOURCE for WNetAddConnection2W
type netResource struct {
	Scope       uint32
	Type        uint32
	DisplayType uint32
	Usage       uint32
	LocalName   *uint16
	RemoteName  *uint16
	Comment     *uint16
	Provider    *uint16
}

const (
	errorNoNetwork         = 1222 // ERROR_NO_NETWORK
	errorConnectionUnavail = 1201 // ERROR_CONNECTION_UNAVAIL (remembered but not connected)
)

// driveRemotePath returns the UNC remote path for a mapped drive letter,
// or empty string if the drive is not a network mapping.
// remembered is true if the drive has a persistent mapping (even if not currently connected).
func driveRemotePath(drive string) (remote string, remembered bool) {
	localName, err := windows.UTF16PtrFromString(strings.ToUpper(drive))
	if err != nil {
		return "", false
	}
	buf := make([]uint16, 260)
	bufLen := uint32(len(buf))
	ret, _, _ := procWNetGetConnection.Call(
		uintptr(unsafe.Pointer(localName)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&bufLen)),
	)
	if ret == 0 {
		return windows.UTF16ToString(buf[:bufLen]), false
	}
	if ret == errorConnectionUnavail {
		// Drive has a remembered mapping but is not currently connected.
		// The buffer may contain the remote name even on this error code.
		if bufLen > 0 {
			return windows.UTF16ToString(buf[:bufLen]), true
		}
		return "(remembered)", true
	}
	return "", false
}

// expectedRemotePath returns the UNC path that tela mount would map to.
func expectedRemotePath(addr string) string {
	return `\\localhost@` + strings.Split(addr, ":")[1] + `\DavWWWRoot`
}

// normalizeDriveLetter ensures a drive argument has a trailing colon.
// "T" becomes "T:", "T:" stays "T:". Anything else is returned as-is.
func normalizeDriveLetter(mountArg string) string {
	if len(mountArg) == 1 && ((mountArg[0] >= 'A' && mountArg[0] <= 'Z') || (mountArg[0] >= 'a' && mountArg[0] <= 'z')) {
		return strings.ToUpper(mountArg) + ":"
	}
	return mountArg
}

func validateMountPoint(mountArg string) error {
	mountArg = normalizeDriveLetter(mountArg)
	vol := filepath.VolumeName(mountArg)
	if vol == "" || vol != mountArg {
		return fmt.Errorf("on Windows, mount point must be a drive letter (e.g., T: or T), not a directory path")
	}
	// Drive exists -- check if it's already ours (will be verified in platformMount)
	// or something else entirely
	if _, err := os.Stat(mountArg + `\`); err == nil {
		remote, _ := driveRemotePath(mountArg)
		if remote == "" {
			// It's a local drive, not a network mapping
			return fmt.Errorf("drive %s is a local drive, not available for mapping", mountArg)
		}
		// It's a network drive -- platformMount will check if it's ours
	} else {
		// Drive doesn't exist as a filesystem path -- check for remembered mappings
		_, remembered := driveRemotePath(mountArg)
		if remembered {
			// Persistent mapping exists but isn't connected -- safe to reuse
		}
	}
	return nil
}

func platformMount(mountArg, addr string) error {
	mountArg = normalizeDriveLetter(mountArg)
	target := expectedRemotePath(addr)

	// Check if already mapped (active or remembered) to our target
	existing, remembered := driveRemotePath(mountArg)
	if strings.EqualFold(existing, target) {
		if remembered {
			log.Printf("drive %s has remembered mapping to %s, reconnecting", mountArg, target)
		} else {
			log.Printf("drive %s already mapped to %s", mountArg, target)
			return nil
		}
	} else if existing != "" {
		return fmt.Errorf("drive %s is already mapped to %s (expected %s)", mountArg, existing, target)
	}

	localName, err := windows.UTF16PtrFromString(strings.ToUpper(mountArg))
	if err != nil {
		return fmt.Errorf("invalid drive letter: %w", err)
	}

	remoteName, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return fmt.Errorf("invalid remote path: %w", err)
	}

	nr := netResource{
		Type:       resourceTypeDisk,
		LocalName:  localName,
		RemoteName: remoteName,
	}

	log.Printf("mapping drive %s via WNetAddConnection2", mountArg)

	ret, _, _ := procWNetAddConnection2.Call(
		uintptr(unsafe.Pointer(&nr)),
		0, // no password
		0, // no username
		0, // no flags
	)

	if ret != 0 {
		return fmt.Errorf("WNetAddConnection2 failed for %s -- Windows error %d (ensure the WebClient service is running: sc start WebClient)", mountArg, ret)
	}

	log.Printf("drive %s mapped", mountArg)
	return nil
}

func platformUnmount(mountArg string) {
	mountArg = normalizeDriveLetter(mountArg)
	name, err := windows.UTF16PtrFromString(strings.ToUpper(mountArg))
	if err != nil {
		log.Printf("unmount: invalid drive letter: %v", err)
		return
	}

	log.Printf("unmapping drive %s", mountArg)

	ret, _, _ := procWNetCancelConn2.Call(
		uintptr(unsafe.Pointer(name)),
		0, // no flags
		1, // force disconnect
	)

	if ret != 0 {
		log.Printf("WNetCancelConnection2 failed: Windows error %d", ret)
	}
}
