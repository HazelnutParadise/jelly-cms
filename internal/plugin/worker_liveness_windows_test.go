//go:build !unix

package plugin

import (
	"errors"
	"os/exec"
)

var errLivenessUnsupported = errors.New("worker liveness process checks are not supported on this platform")

// processGroupSupported reports that the Unix process-group checks are skipped
// on platforms without Unix process groups.
func processGroupSupported() bool {
	return false
}

// configureProcessGroup is a no-op without Unix process groups; the parent hard
// deadline only kills the direct child process.
func configureProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup kills the direct child process when Unix process groups are
// unavailable. Descendants are not tracked.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}

	return cmd.Process.Kill()
}

func findWorkerPIDs(parentPID int) ([]int, error) {
	return nil, errLivenessUnsupported
}

func processParentPID(pid int) (int, error) {
	return 0, errLivenessUnsupported
}

func processExists(pid int) (bool, error) {
	return false, errLivenessUnsupported
}
