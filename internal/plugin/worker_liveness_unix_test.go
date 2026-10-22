//go:build unix

package plugin

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// processGroupSupported reports whether this platform can isolate and kill the
// child test process together with the worker processes it starts.
func processGroupSupported() bool {
	return true
}

// configureProcessGroup puts the child in its own process group so a hard
// deadline can clean up the helper and its descendants without touching
// unrelated processes.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup kills only the process group created for cmd.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}

	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("kill process group %d: %w", cmd.Process.Pid, err)
	}

	return nil
}

// findWorkerPIDs returns the plugin worker children of parentPID. It matches the
// dedicated worker argv so unrelated children are never returned.
func findWorkerPIDs(parentPID int) ([]int, error) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("run ps: %w", err)
	}

	var pids []int

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}

		ppid, err := strconv.Atoi(fields[1])
		if err != nil || ppid != parentPID {
			continue
		}

		command := strings.Join(fields[2:], " ")
		if !strings.Contains(command, workerArg) {
			continue
		}

		pids = append(pids, pid)
	}

	return pids, nil
}

// processParentPID returns the parent pid reported by ps for pid.
func processParentPID(pid int) (int, error) {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, fmt.Errorf("run ps for pid %d: %w", pid, err)
	}

	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, fmt.Errorf("process %d is not running", pid)
	}

	ppid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, fmt.Errorf("parse parent pid %q: %w", fields[0], err)
	}

	return ppid, nil
}

// processExists reports whether ps still lists pid.
func processExists(pid int) (bool, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "pid=").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && strings.TrimSpace(string(out)) == "" {
			return false, nil
		}

		return false, fmt.Errorf("run ps for pid %d: %w", pid, err)
	}

	return strings.TrimSpace(string(out)) != "", nil
}
