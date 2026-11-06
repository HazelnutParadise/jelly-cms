package plugin

// Race reproduced here (worker_process.go, request and shutdown):
//
// A plugin worker answers a shutdown (or any) request and then returns from
// main at once, so its stdout reaches EOF right after the response frame. The
// parent's readLoop buffers that response in w.responses (capacity 1), then
// closes w.readDone. request() waits in two selects that both listen on
// readDone: the first while the request is being written, the second while the
// response is awaited. When the request goroutine reaches a select after both
// w.readDone and the response (or the write completion) are ready, Go picks a
// case at random. Picking readDone reports "worker exited during <op>" even
// though the response was already delivered.
//
// The real pipe timing is hard to hit deterministically, so these tests
// simulate the worker's behaviour inside the parent's stdin. Once a complete
// request frame has been written, the fake worker places the response in
// w.responses, closes w.readDone, and only then returns from Write. Both
// selects therefore see readDone ready together with the next case they wait
// for. No real child process and no CPU stress are involved.
//
// The control test uses a worker that closes readDone without any response.
// That error path must keep reporting "worker exited during".

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	raceShutdownIterations = 200
	raceRequestIterations  = 200
	raceControlIterations  = 50
	raceCallTimeout        = 5 * time.Second
)

// raceWorkerMode selects how the fake worker ends after it reads a request.
type raceWorkerMode int

const (
	// raceRespondThenExit answers the request and exits right after.
	raceRespondThenExit raceWorkerMode = iota
	// raceExitSilently exits without answering the request.
	raceExitSilently
)

// raceFakeStdin stands in for the worker's stdin pipe. It reassembles length
// prefixed frames from the bytes writeFrame sends and calls onRequest for each
// complete request, inside Write, before Write returns.
type raceFakeStdin struct {
	buf       []byte
	lastID    uint64
	onRequest func(request workerRequest)
}

func (s *raceFakeStdin) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)

	for len(s.buf) >= 4 {
		size := int(binary.BigEndian.Uint32(s.buf[:4]))
		if len(s.buf) < 4+size {
			break
		}

		var request workerRequest
		if err := json.Unmarshal(s.buf[4:4+size], &request); err != nil {
			return 0, fmt.Errorf("fake worker decode request: %w", err)
		}
		s.buf = s.buf[4+size:]

		s.lastID = request.ID
		s.onRequest(request)
	}

	return len(p), nil
}

// Close is a no-op. terminate closes stdin, and the fake has nothing to release.
func (s *raceFakeStdin) Close() error {
	return nil
}

// newRaceFakeWorker builds a parent-side workerProcess with no child process.
// Its cmd is a zero exec.Cmd, which terminate tolerates. waitOnce is marked as
// done with a nil error, so the fake reports a clean exit the way a real
// worker that returns 0 would. Without this, the error text would carry the
// zero Cmd's "exec: not started" and differ from production. The stdout reader
// is never read, because readLoop does not run.
func newRaceFakeWorker(mode raceWorkerMode) (*workerProcess, *raceFakeStdin) {
	w := &workerProcess{
		id:        "race-fake",
		cmd:       &exec.Cmd{},
		stdout:    io.NopCloser(strings.NewReader("")),
		ready:     make(chan struct{}),
		responses: make(chan workerFrame, 1),
		hostCalls: make(chan workerFrame, 1),
		readDone:  make(chan struct{}),
		stop:      make(chan struct{}),
	}
	w.waitOnce.Do(func() {})

	stdin := &raceFakeStdin{}
	stdin.onRequest = func(request workerRequest) {
		if mode == raceRespondThenExit {
			w.responses <- workerFrame{Type: frameTypeResponse, ID: request.ID, OK: true}
		}
		close(w.readDone)
	}
	w.stdin = stdin

	return w, stdin
}

// raceCall runs fn and fails the test if it does not return in time. This
// catches a hang in terminate or request on the fake worker.
func raceCall(t *testing.T, label string, fn func()) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()

	select {
	case <-done:
	case <-time.After(raceCallTimeout):
		t.Fatalf("%s did not return within %s", label, raceCallTimeout)
	}
}

// TestWorkerProcessRaceShutdownReturnsBufferedResponse calls shutdown on fresh
// fake workers. Each one answers the shutdown and then exits. Every call must
// return nil.
func TestWorkerProcessRaceShutdownReturnsBufferedResponse(t *testing.T) {
	failures := 0

	for i := 1; i <= raceShutdownIterations; i++ {
		w, _ := newRaceFakeWorker(raceRespondThenExit)

		var err error
		raceCall(t, fmt.Sprintf("shutdown iteration %d", i), func() {
			err = w.shutdown(context.Background(), []byte("{}"))
		})

		if err != nil {
			failures++
			t.Errorf("shutdown iteration %d of %d: got error %q, want nil", i, raceShutdownIterations, err)
		}
	}

	t.Logf("shutdown: %d of %d iterations failed", failures, raceShutdownIterations)
}

// TestWorkerProcessRaceBufferedResponseReturnedThroughRequest checks the same
// race through request() for a normal hook operation. The response must come
// back with OK set and the ID of the request that was sent.
func TestWorkerProcessRaceBufferedResponseReturnedThroughRequest(t *testing.T) {
	failures := 0

	for i := 1; i <= raceRequestIterations; i++ {
		w, stdin := newRaceFakeWorker(raceRespondThenExit)

		var (
			frame workerFrame
			err   error
		)
		raceCall(t, fmt.Sprintf("hook request iteration %d", i), func() {
			frame, err = w.request(
				context.Background(),
				workerRequest{Op: opHook, Hook: "race-hook"},
				raceCallTimeout,
				PluginMetadata{},
				nil,
			)
		})

		switch {
		case err != nil:
			failures++
			t.Errorf("hook request iteration %d of %d: got error %q, want response", i, raceRequestIterations, err)
		case !frame.OK:
			failures++
			t.Errorf("hook request iteration %d of %d: response OK is false", i, raceRequestIterations)
		case frame.ID != stdin.lastID:
			failures++
			t.Errorf("hook request iteration %d of %d: response ID %d, want request ID %d", i, raceRequestIterations, frame.ID, stdin.lastID)
		}
	}

	t.Logf("hook request: %d of %d iterations failed", failures, raceRequestIterations)
}

// TestWorkerProcessExitedWithoutResponseReportsExitedDuringShutdown is the
// control. A worker that exits without answering must still report an error
// that contains "worker exited during" for both shutdown and request.
func TestWorkerProcessExitedWithoutResponseReportsExitedDuringShutdown(t *testing.T) {
	t.Run("shutdown", func(t *testing.T) {
		for i := 1; i <= raceControlIterations; i++ {
			w, _ := newRaceFakeWorker(raceExitSilently)

			var err error
			raceCall(t, fmt.Sprintf("silent shutdown iteration %d", i), func() {
				err = w.shutdown(context.Background(), []byte("{}"))
			})

			if err == nil {
				t.Fatalf("silent shutdown iteration %d: got nil, want error", i)
			}
			if !strings.Contains(err.Error(), "worker exited during") {
				t.Fatalf("silent shutdown iteration %d: got %q, want it to contain %q", i, err, "worker exited during")
			}
		}
	})

	t.Run("request", func(t *testing.T) {
		for i := 1; i <= raceControlIterations; i++ {
			w, _ := newRaceFakeWorker(raceExitSilently)

			var err error
			raceCall(t, fmt.Sprintf("silent request iteration %d", i), func() {
				_, err = w.request(
					context.Background(),
					workerRequest{Op: opHook, Hook: "race-hook"},
					raceCallTimeout,
					PluginMetadata{},
					nil,
				)
			})

			if err == nil {
				t.Fatalf("silent request iteration %d: got nil, want error", i)
			}
			if !strings.Contains(err.Error(), "worker exited during") {
				t.Fatalf("silent request iteration %d: got %q, want it to contain %q", i, err, "worker exited during")
			}
		}
	})
}
