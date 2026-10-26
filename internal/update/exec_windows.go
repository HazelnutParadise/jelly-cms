//go:build windows

package update

import (
	"os"
	"os/exec"
)

func relaunch(exe string, args, env []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
