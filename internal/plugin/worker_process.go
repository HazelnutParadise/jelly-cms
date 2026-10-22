package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// workerEnvAllowlist contains the system environment a worker needs to start.
// Application secrets such as database URLs and API keys are not passed.
var workerEnvAllowlist = map[string]struct{}{
	"PATH":                   {},
	"HOME":                   {},
	"USER":                   {},
	"LOGNAME":                {},
	"SHELL":                  {},
	"TMPDIR":                 {},
	"TMP":                    {},
	"TEMP":                   {},
	"TZ":                     {},
	"LANG":                   {},
	"LC_ALL":                 {},
	"LC_CTYPE":               {},
	"PWD":                    {},
	"SYSTEMROOT":             {},
	"WINDIR":                 {},
	"COMSPEC":                {},
	"PATHEXT":                {},
	"USERPROFILE":            {},
	"PROGRAMDATA":            {},
	"APPDATA":                {},
	"LOCALAPPDATA":           {},
	"NUMBER_OF_PROCESSORS":   {},
	"PROCESSOR_ARCHITECTURE": {},
}

// workerWorkDirPrefix names the dedicated empty directory each worker process
// runs in. The parent creates and removes it; the child never creates one.
const workerWorkDirPrefix = "jelly-plugin-"

// workerEnvironment builds the filtered environment for one worker process.
func workerEnvironment() []string {
	env := make([]string, 0, len(workerEnvAllowlist)+1)

	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}

		if _, allowed := workerEnvAllowlist[strings.ToUpper(key)]; allowed {
			env = append(env, entry)
		}
	}

	return append(env, workerEnvKey+"="+workerEnvMarker)
}

// workerProcess is the parent side of one plugin worker. All requests are
// serialised by the owning pluginState, so only the reader goroutine touches
// the response channel concurrently.
type workerProcess struct {
	id      string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	workDir string

	ready     chan struct{}
	readyOnce sync.Once
	responses chan workerFrame
	readDone  chan struct{}
	stop      chan struct{}

	readMu  sync.Mutex
	readErr error

	terminateOnce sync.Once
	waitOnce      sync.Once
	waitErr       error

	nextID atomic.Uint64
}

// startWorker starts the same binary as a plugin worker and waits for its
// handshake. A worker that fails to start is killed and reaped before the
// error is returned.
func startWorker(ctx context.Context, pluginID string) (*workerProcess, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve plugin worker executable: %w", err)
	}

	// The empty work directory is owned by this parent. The child is started
	// inside it, so plugin relative paths never reach the website directory.
	workDir, err := os.MkdirTemp("", workerWorkDirPrefix)
	if err != nil {
		return nil, fmt.Errorf("create plugin worker work directory: %w", err)
	}

	cmd := exec.Command(executable, workerArg)
	cmd.Dir = workDir
	cmd.Env = workerEnvironment()
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = os.RemoveAll(workDir)

		return nil, fmt.Errorf("create plugin worker stdin: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		_ = os.RemoveAll(workDir)

		return nil, fmt.Errorf("create plugin worker stdout: %w", err)
	}

	worker := &workerProcess{
		id:        pluginID,
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		workDir:   workDir,
		ready:     make(chan struct{}),
		responses: make(chan workerFrame, 1),
		readDone:  make(chan struct{}),
		stop:      make(chan struct{}),
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = os.RemoveAll(workDir)

		return nil, fmt.Errorf("start plugin worker for %s: %w", pluginID, err)
	}

	go worker.readLoop()

	timer := time.NewTimer(workerStartTimeout)
	defer timer.Stop()

	select {
	case <-worker.ready:
		return worker, nil
	case <-worker.readDone:
		worker.terminate()

		return nil, fmt.Errorf(
			"plugin worker for %s stopped before the handshake: %w",
			pluginID, worker.exitError("handshake"),
		)
	case <-ctx.Done():
		worker.terminate()

		return nil, ctx.Err()
	case <-timer.C:
		worker.terminate()

		return nil, fmt.Errorf(
			"plugin worker for %s did not complete the %s handshake",
			pluginID, workerStartTimeout,
		)
	}
}

// readLoop reads protocol frames. Logs are printed as soon as they arrive so
// the parent observes them while a request is still running.
func (w *workerProcess) readLoop() {
	defer close(w.readDone)

	for {
		payload, err := readFrame(w.stdout, maxFrameBytes)
		if err != nil {
			w.setReadError(err)

			return
		}

		// A terminating worker must not keep spending the per-log budget on
		// frames that were already buffered in the pipe.
		select {
		case <-w.stop:
			return
		default:
		}

		var frame workerFrame
		if err := json.Unmarshal(payload, &frame); err != nil {
			w.setReadError(fmt.Errorf("decode worker frame: %w", err))

			return
		}

		switch frame.Type {
		case frameTypeReady:
			w.readyOnce.Do(func() { close(w.ready) })
		case frameTypeLog:
			enqueuePluginLog(w.id, frame.Level, truncateUTF8(frame.Message, maxLogEntryBytes))
		case frameTypeResponse:
			select {
			case w.responses <- frame:
			default:
				w.setReadError(errors.New("unexpected worker response"))

				return
			}
		default:
			w.setReadError(fmt.Errorf("unknown worker frame type %q", frame.Type))

			return
		}
	}
}

// request sends one request and waits for its response. Timeouts, cancellation
// and protocol failures kill and reap the worker before returning.
func (w *workerProcess) request(ctx context.Context, request workerRequest, timeout time.Duration) (workerFrame, error) {
	request.ID = w.nextID.Add(1)
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var ctxDone <-chan struct{}
	if ctx != nil {
		ctxDone = ctx.Done()
	}
	writeDone := make(chan error, 1)
	go func() { writeDone <- writeFrame(w.stdin, request, maxFrameBytes) }()
	select {
	case err := <-writeDone:
		if err != nil {
			w.terminate()
			return workerFrame{}, fmt.Errorf("send %s request to plugin worker: %w", request.Op, err)
		}
	case <-w.readDone:
		w.terminate()
		return workerFrame{}, w.exitError(request.Op)
	case <-ctxDone:
		w.terminate()
		return workerFrame{}, ctx.Err()
	case <-timer.C:
		w.terminate()
		return workerFrame{}, fmt.Errorf("plugin worker %s request timed out after %s", request.Op, timeout)
	}

	select {
	case frame := <-w.responses:
		if frame.ID != request.ID {
			w.terminate()

			return workerFrame{}, fmt.Errorf(
				"plugin worker answered request %d while %d was pending",
				frame.ID, request.ID,
			)
		}

		if !frame.OK {
			if frame.Error == "" {
				frame.Error = "plugin worker reported an unspecified error"
			}

			return frame, errors.New(frame.Error)
		}

		return frame, nil
	case <-w.readDone:
		w.terminate()

		return workerFrame{}, w.exitError(request.Op)
	case <-ctxDone:
		w.terminate()

		return workerFrame{}, ctx.Err()
	case <-timer.C:
		w.terminate()

		return workerFrame{}, fmt.Errorf(
			"plugin worker %s request timed out after %s", request.Op, timeout,
		)
	}
}

// shutdown runs the OnShutdown hook and then reclaims the worker process. The
// response is the last protocol frame, so killing the process afterwards cannot
// lose plugin output and keeps the shutdown deadline. The child is always
// reaped, even when the hook fails.
func (w *workerProcess) shutdown(ctx context.Context, payload []byte) error {
	if !w.alive() {
		w.terminate()

		return nil
	}

	_, err := w.request(ctx, workerRequest{Op: opShutdown, Payload: payload}, workerStepTimeout)
	w.terminate()

	return err
}

// terminate kills the worker, closes the pipes, waits for the reader to finish
// and reaps the process exactly once. It is safe to call repeatedly.
func (w *workerProcess) terminate() {
	w.terminateOnce.Do(func() {
		// Stopping the reader and closing stdout first discards frames still
		// buffered in the pipe. Otherwise the reader would spend its bounded
		// per-log budget on every leftover log frame before readDone closes.
		close(w.stop)
		_ = w.stdin.Close()

		if w.cmd.Process != nil {
			_ = w.cmd.Process.Kill()
		}

		_ = w.stdout.Close()
		<-w.readDone
		_ = w.waitProcess()
		w.removeWorkDir()
	})
}

// removeWorkDir deletes the directory this parent created for the worker. It
// runs only after the child was killed and reaped, so the worker can no longer
// write into it and no arbitrary directory is ever removed.
func (w *workerProcess) removeWorkDir() {
	if w.workDir == "" {
		return
	}

	workDir := w.workDir
	w.workDir = ""

	if err := os.RemoveAll(workDir); err != nil {
		fmt.Fprintf(os.Stderr, "remove plugin work directory %s: %v\n", workDir, err)
	}
}

// waitProcess waits for the child exactly once, after its stdout is closed.
func (w *workerProcess) waitProcess() error {
	w.waitOnce.Do(func() {
		w.waitErr = w.cmd.Wait()
	})

	return w.waitErr
}

// alive reports whether the reader is still running for this worker.
func (w *workerProcess) alive() bool {
	select {
	case <-w.readDone:
		return false
	default:
		return true
	}
}

// exitError describes why a worker stopped reading.
func (w *workerProcess) exitError(operation string) error {
	w.readMu.Lock()
	readErr := w.readErr
	w.readMu.Unlock()

	if readErr == nil || errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrClosedPipe) {
		if waitErr := w.waitErr; waitErr != nil {
			return fmt.Errorf("worker exited during %s: %w", operation, waitErr)
		}

		return fmt.Errorf("worker exited during %s", operation)
	}

	return fmt.Errorf("worker protocol failed during %s: %w", operation, readErr)
}

// setReadError records the first reader failure.
func (w *workerProcess) setReadError(err error) {
	w.readMu.Lock()
	if w.readErr == nil {
		w.readErr = err
	}
	w.readMu.Unlock()
}
