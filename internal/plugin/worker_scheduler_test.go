package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const workerSchedulerExitBudget = 3 * time.Second

type workerSchedulerResult struct {
	frame workerFrame
	err   error
}

type workerSchedulerProcess struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	frames     chan workerSchedulerResult
	done       chan struct{}
	readerDone chan struct{}
	waitErr    error
}

func startWorkerSchedulerProcess(t *testing.T) *workerSchedulerProcess {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("find test executable: %v", err)
	}
	cmd := exec.Command(executable, workerArg)
	cmd.Env = workerSchedulerEnvironment()
	cmd.Dir = t.TempDir()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("open worker stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		t.Fatalf("open worker stdout: %v", err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		t.Fatalf("start worker process: %v", err)
	}

	process := &workerSchedulerProcess{
		cmd: cmd, stdin: stdin, stdout: stdout,
		frames: make(chan workerSchedulerResult, 8), done: make(chan struct{}), readerDone: make(chan struct{}),
	}
	go func() {
		defer close(process.readerDone)
		publish := func(result workerSchedulerResult) bool {
			select {
			case process.frames <- result:
				return true
			case <-process.done:
				return false
			}
		}
		for {
			payload, err := readFrame(stdout, maxFrameBytes)
			if err != nil {
				publish(workerSchedulerResult{err: err})
				return
			}
			var frame workerFrame
			if err := json.Unmarshal(payload, &frame); err != nil {
				publish(workerSchedulerResult{err: fmt.Errorf("decode worker protocol frame: %w", err)})
				return
			}
			if !publish(workerSchedulerResult{frame: frame}) {
				return
			}
		}
	}()
	go func() {
		process.waitErr = cmd.Wait()
		close(process.done)
	}()

	t.Cleanup(func() {
		_ = process.stdin.Close()
		select {
		case <-process.done:
			// The wait goroutine already reaped this worker.
		default:
			if process.cmd.Process != nil {
				_ = process.cmd.Process.Kill()
			}
			select {
			case <-process.done:
			case <-time.After(5 * time.Second):
				t.Errorf("worker cleanup did not reap process")
			}
		}
		_ = process.stdout.Close()
		select {
		case <-process.readerDone:
		case <-time.After(time.Second):
			t.Errorf("worker stdout reader did not stop during cleanup")
		}
	})
	return process
}

func workerSchedulerEnvironment() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok && (key == "GOMAXPROCS" || key == workerEnvKey) {
			continue
		}
		env = append(env, item)
	}
	return append(env, "GOMAXPROCS=1", workerEnvKey+"="+workerEnvMarker)
}

func (p *workerSchedulerProcess) nextFrame(t *testing.T, timeout time.Duration) workerFrame {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result, ok := <-p.frames:
		if !ok {
			t.Fatal("worker stdout closed before expected protocol frame")
		}
		if result.err != nil {
			t.Fatalf("read worker protocol frame: %v", result.err)
		}
		return result.frame
	case <-timer.C:
		t.Fatalf("timed out waiting %s for worker protocol frame", timeout)
		return workerFrame{}
	}
}

func (p *workerSchedulerProcess) waitForLog(t *testing.T, marker string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Until(deadline) > 0 {
		frame := p.nextFrame(t, time.Until(deadline))
		if frame.Type == frameTypeLog && frame.Message == marker {
			return
		}
	}
	t.Fatalf("timed out waiting for worker log marker %q", marker)
}

func (p *workerSchedulerProcess) send(t *testing.T, request workerRequest) {
	t.Helper()
	if err := writeFrame(p.stdin, request, maxFrameBytes); err != nil {
		t.Fatalf("write worker request %q: %v", request.Op, err)
	}
}

func (p *workerSchedulerProcess) waitForResponse(t *testing.T, id uint64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Until(deadline) > 0 {
		frame := p.nextFrame(t, time.Until(deadline))
		if frame.Type != frameTypeResponse {
			continue
		}
		if frame.ID != id || !frame.OK {
			t.Fatalf("worker response = %+v, want successful response id %d", frame, id)
		}
		return
	}
	t.Fatalf("timed out waiting for worker response id %d", id)
}

func (p *workerSchedulerProcess) waitForCleanExit(t *testing.T, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-p.done:
		if p.waitErr != nil {
			t.Fatalf("worker did not exit cleanly: %v", p.waitErr)
		}
	case <-timer.C:
		t.Fatalf("worker did not exit within %s after stdin closed", timeout)
	}
}

func TestWorkerSchedulerIdleWorkerExitsOnEOF(t *testing.T) {
	process := startWorkerSchedulerProcess(t)
	if frame := process.nextFrame(t, 10*time.Second); frame.Type != frameTypeReady || !frame.OK {
		t.Fatalf("worker ready frame = %+v", frame)
	}
	process.send(t, workerRequest{ID: 1, Op: opLoad, Code: ""})
	process.waitForResponse(t, 1)
	if err := process.stdin.Close(); err != nil {
		t.Fatalf("close idle worker stdin: %v", err)
	}
	process.waitForCleanExit(t, workerSchedulerExitBudget)
}

func TestWorkerSchedulerBusyEntryWorkerExitsOnEOF(t *testing.T) {
	const marker = "worker-scheduler-entry-busy"
	process := startWorkerSchedulerProcess(t)
	if frame := process.nextFrame(t, 10*time.Second); frame.Type != frameTypeReady || !frame.OK {
		t.Fatalf("worker ready frame = %+v", frame)
	}
	process.send(t, workerRequest{ID: 1, Op: opLoad, Code: fmt.Sprintf("JellyCMS.log.info(%q); while (true) {}", marker)})
	process.waitForLog(t, marker, 10*time.Second)
	if err := process.stdin.Close(); err != nil {
		t.Fatalf("close busy entry worker stdin: %v", err)
	}
	process.waitForCleanExit(t, workerSchedulerExitBudget)
}

func TestWorkerSchedulerBusyHookWorkerExitsOnEOF(t *testing.T) {
	const marker = "worker-scheduler-hook-busy"
	process := startWorkerSchedulerProcess(t)
	if frame := process.nextFrame(t, 10*time.Second); frame.Type != frameTypeReady || !frame.OK {
		t.Fatalf("worker ready frame = %+v", frame)
	}
	process.send(t, workerRequest{ID: 1, Op: opLoad, Code: fmt.Sprintf(`JellyCMS.registerHook("SchedulerBusy", function () { JellyCMS.log.info(%q); while (true) {} });`, marker)})
	process.waitForResponse(t, 1)
	process.send(t, workerRequest{ID: 2, Op: opHook, Hook: "SchedulerBusy"})
	process.waitForLog(t, marker, 10*time.Second)
	if err := process.stdin.Close(); err != nil {
		t.Fatalf("close busy hook worker stdin: %v", err)
	}
	process.waitForCleanExit(t, workerSchedulerExitBudget)
}
