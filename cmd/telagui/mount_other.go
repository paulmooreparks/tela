//go:build !windows

package main

// platformUnmountDrive is a no-op on non-Windows platforms.
// On macOS/Linux, mount points are directory-based and cleaned up
// by umount when the mount process exits.
func platformUnmountDrive(mountPoint string) {}
