//go:build unix

package config

import (
	"os/signal"
	"syscall"
)

// limitFileSize caps the size of the files this process may write and returns
// a function that restores the previous limit and signal handling. Writing
// past the limit fails the way a full disk fails: the destination file is
// already truncated when the write is rejected.
func limitFileSize(limit int64) (func(), error) {
	var previous syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &previous); err != nil {
		return nil, err
	}

	// Without this the process would be killed by SIGXFSZ instead of getting
	// the write error.
	signal.Ignore(syscall.SIGXFSZ)

	limited := previous
	limited.Cur = uint64(limit)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limited); err != nil {
		return nil, err
	}

	return func() {
		_ = syscall.Setrlimit(syscall.RLIMIT_FSIZE, &previous)
		signal.Reset(syscall.SIGXFSZ)
	}, nil
}
