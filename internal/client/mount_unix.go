//go:build !windows

package client

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func validateMountPoint(mountArg string) error {
	absPath, err := filepath.Abs(mountArg)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}

	parent := filepath.Dir(absPath)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("parent directory %s does not exist", parent)
	}
	if !info.IsDir() {
		return fmt.Errorf("parent path %s is not a directory", parent)
	}

	if info, err := os.Stat(absPath); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", absPath)
		}
		entries, err := os.ReadDir(absPath)
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", absPath, err)
		}
		if len(entries) > 0 {
			return fmt.Errorf("%s is not empty (%d items)", absPath, len(entries))
		}
	}

	return nil
}

func platformMount(mountArg, addr string) error {
	absPath, err := filepath.Abs(mountArg)
	if err != nil {
		return fmt.Errorf("invalid mount path: %w", err)
	}

	if err := os.MkdirAll(absPath, 0755); err != nil {
		return fmt.Errorf("cannot create mount point %s: %w", absPath, err)
	}

	davURL := "http://" + addr + "/"

	if runtime.GOOS == "darwin" {
		log.Printf("mounting to %s", absPath)
		cmd := exec.Command("mount_webdav", davURL, absPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("mount_webdav %s failed: %w", absPath, err)
		}
		log.Printf("mounted at %s", absPath)
		return nil
	}

	// Linux: try gio mount first (no root needed), fall back to mount -t davfs
	log.Printf("mounting to %s", absPath)
	gioPath, _ := exec.LookPath("gio")
	if gioPath != "" {
		cmd := exec.Command("gio", "mount", "dav://"+addr+"/")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			log.Printf("mounted via gio at dav://%s/", addr)
			return nil
		}
		log.Printf("gio mount failed, trying mount -t davfs")
	}

	cmd := exec.Command("mount", "-t", "davfs", davURL, absPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mount -t davfs %s failed: %w (you may need root or davfs2 installed)", absPath, err)
	}
	log.Printf("mounted at %s", absPath)
	return nil
}

func platformUnmount(mountArg string) {
	absPath, _ := filepath.Abs(mountArg)
	log.Printf("unmounting %s", absPath)
	exec.Command("umount", absPath).Run()
}
