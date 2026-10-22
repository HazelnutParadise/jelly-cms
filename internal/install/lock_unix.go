//go:build !windows

package install

import (
	"fmt"
	"os"
	"syscall"
)

const lockPath = "data/install.lock"

// acquireDataDirLock serializes installations that share a data directory.
// The lock is an advisory OS file lock, so the kernel releases it when the
// process exits and an abandoned lock file can never block future attempts.
func acquireDataDirLock() (func() error, error) {
	if err := os.MkdirAll("data", 0700); err != nil {
		return nil, fmt.Errorf("create the installation data directory: %w", err)
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open the installation lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock the installation data directory: %w", err)
	}

	return func() error {
		unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		closeErr := file.Close()
		if unlockErr != nil {
			return fmt.Errorf("unlock the installation data directory: %w", unlockErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close the installation lock: %w", closeErr)
		}
		return nil
	}, nil
}
