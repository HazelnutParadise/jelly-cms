//go:build windows

package install

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

const lockPath = "data/install.lock"

// acquireDataDirLock serializes installations that share a data directory. The
// lock is an advisory OS file lock, so Windows releases it when the process
// exits and an abandoned lock file can never block future attempts.
func acquireDataDirLock() (func() error, error) {
	if err := os.MkdirAll("data", 0700); err != nil {
		return nil, fmt.Errorf("create the installation data directory: %w", err)
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open the installation lock: %w", err)
	}

	var overlapped windows.Overlapped
	handle := windows.Handle(file.Fd())
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock the installation data directory: %w", err)
	}

	return func() error {
		unlockErr := windows.UnlockFileEx(handle, 0, 1, 0, &overlapped)
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
