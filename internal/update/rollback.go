package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/HazelnutParadise/jelly-cms/internal/version"
)

// Rollback puts back the executable that the last apply replaced (<exe>.prev),
// together with the web tree it replaced. The version that was running moves to
// .prev, so a second rollback undoes the first.
func Rollback(ctx context.Context) error {
	unlock, err := lockOp()
	if err != nil {
		return err
	}
	defer unlock()
	_, end := beginOp(ctx)
	defer end()

	exe, err := swappableExecutable()
	if err != nil {
		return err
	}
	if _, err := os.Stat(exe + prevSuffix); err != nil {
		return ErrNoPrevious
	}
	if err := swapBack(exe); err != nil {
		return err
	}
	return mutateState(func(st *State) bool {
		st.Status = StatusRolledBack
		st.FailedBoots = 0
		st.LastError = ""
		return true
	})
}

// swapBack exchanges the executable with <exe>.prev and the web tree with its
// backup. It is used by Rollback and by the boot guard.
func swapBack(exe string) error {
	prev := exe + prevSuffix
	tmp := exe + ".swap"
	if err := os.Rename(exe, tmp); err != nil {
		return fmt.Errorf("update: move current executable aside: %w", err)
	}
	if err := os.Rename(prev, exe); err != nil {
		_ = os.Rename(tmp, exe)
		return fmt.Errorf("update: restore previous executable: %w", err)
	}
	if err := os.Rename(tmp, prev); err != nil {
		return fmt.Errorf("update: keep current executable as previous: %w", err)
	}
	if _, err := os.Stat(webBackupDir); err == nil {
		tmpWeb := webDir + ".swap"
		if err := os.RemoveAll(tmpWeb); err != nil {
			return err
		}
		if _, err := os.Stat(webDir); err == nil {
			if err := os.Rename(webDir, tmpWeb); err != nil {
				return fmt.Errorf("update: move current web aside: %w", err)
			}
		}
		if err := os.Rename(webBackupDir, webDir); err != nil {
			return fmt.Errorf("update: restore previous web: %w", err)
		}
		if _, err := os.Stat(tmpWeb); err == nil {
			if err := os.Rename(tmpWeb, webBackupDir); err != nil {
				return fmt.Errorf("update: keep current web as backup: %w", err)
			}
		}
	}
	return nil
}

// GuardStartup runs before the server starts. An apply leaves the state at
// applied until ConfirmStarted sees the new version run. Each start that finds
// applied adds one to failed_boots. At two, the previous executable comes back
// and the state becomes rolled_back. The result reports whether the caller must
// relaunch the restored executable.
func GuardStartup() (bool, error) {
	st, err := LoadState(StatePath)
	if err != nil {
		return false, err
	}
	if st.Status != StatusApplied {
		return false, nil
	}
	st.FailedBoots++
	if st.FailedBoots < 2 {
		return false, SaveState(StatePath, st)
	}
	exe, err := executablePath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(exe + prevSuffix); err != nil {
		return false, SaveState(StatePath, st)
	}
	if err := swapBack(exe); err != nil {
		return false, err
	}
	st.Status = StatusRolledBack
	st.FailedBoots = 0
	st.LastError = "rolled back after two failed starts"
	return true, SaveState(StatePath, st)
}

// ConfirmStarted marks an applied version as healthy once the process reaches
// the point of serving HTTP.
func ConfirmStarted() error {
	return mutateState(func(st *State) bool {
		if st.Status != StatusApplied {
			return false
		}
		st.Status = StatusOK
		st.FailedBoots = 0
		return true
	})
}

// WriteHealthcheck writes the report that the healthcheck subcommand prints.
// Apply runs the subcommand on the installed executable before it trusts it.
func WriteHealthcheck(w io.Writer) error {
	return json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": version.Version,
	})
}

// Relaunch starts the executable again with the same arguments and environment.
// The caller exits after it returns.
func Relaunch() error {
	exe, err := executablePath()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Start()
}
