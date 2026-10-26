//go:build !windows

package update

import "syscall"

func relaunch(exe string, args, env []string) error {
	argv := append([]string{exe}, args...)
	return syscall.Exec(exe, argv, env)
}
