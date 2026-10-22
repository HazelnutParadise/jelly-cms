package plugin

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/fastschema/qjs"
)

// dispatcherRegistered records that the process called RunWorkerIfRequested.
// NewRuntime refuses to start without it, so a program cannot accidentally
// re-execute its normal main function as a worker.
var dispatcherRegistered atomic.Bool

// RunWorkerIfRequested is the explicit plugin worker entry point. The server
// and test main functions call it before any normal start-up.
//
// A process started with the exact worker argv and environment marker runs the
// worker loop and exits. Every other process only marks the dispatcher as
// registered and returns to its normal start-up.
func RunWorkerIfRequested() {
	if isWorkerInvocation(os.Args[1:], os.Getenv(workerEnvKey)) {
		os.Exit(runWorkerProcess())
	}

	dispatcherRegistered.Store(true)
	warmWorkerCache()
}

// workerCacheDir returns the persistent wazero compilation cache shared by all
// plugin workers. An empty result falls back to wazero's in-memory cache.
func workerCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		return ""
	}

	dir := filepath.Join(base, "jelly-cms", "qjs-wazero")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}

	return dir
}

// warmWorkerCache opens QuickJS once outside per-request deadlines. The runtime
// itself validates and populates the cache for the current module version and
// architecture; the presence of unrelated cache files says nothing about it.
func warmWorkerCache() {
	dir := workerCacheDir()

	runtime, err := qjs.New(qjs.Option{
		CacheDir: dir,
		Stdout:   io.Discard,
		Stderr:   io.Discard,
	})
	if err != nil {
		return
	}
	runtime.Close()
}

// isWorkerInvocation reports whether the argv and environment identify a
// worker process. Both markers are required.
func isWorkerInvocation(args []string, marker string) bool {
	return len(args) == 1 && args[0] == workerArg && marker == workerEnvMarker
}

// runWorkerProcess serves plugin requests until stdin closes or a shutdown
// request arrives. Stdout carries protocol frames only.
func runWorkerProcess() int {
	sendLog := func(level, message string) {
		frame := workerFrame{Type: frameTypeLog, Level: level, Message: message}
		// A failed log frame means the parent is gone or the protocol broke.
		// The response write below reports the failure; logs never block the
		// worker beyond the bounded frame itself.
		_ = writeFrame(os.Stdout, frame, maxFrameBytes)
	}

	state, err := newWorkerJS(sendLog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin worker initialisation failed: %v\n", err)

		return 2
	}

	if err := writeFrame(os.Stdout, workerFrame{Type: frameTypeReady, OK: true}, maxFrameBytes); err != nil {
		state.release()

		return 3
	}

	input := bufio.NewReaderSize(os.Stdin, 64<<10)
	requests := make(chan workerRequest, 1)
	go func() {
		for {
			payload, readErr := readFrame(input, maxFrameBytes)
			if readErr != nil {
				if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
					// A parent disappearing must stop even a worker whose JS call
					// never returns. Process exit also reclaims the QuickJS state.
					os.Exit(0)
				}
				fmt.Fprintf(os.Stderr, "plugin worker read failed: %v\n", readErr)
				os.Exit(4)
			}
			var request workerRequest
			if err := json.Unmarshal(payload, &request); err != nil {
				fmt.Fprintf(os.Stderr, "plugin worker received an invalid request: %v\n", err)
				os.Exit(4)
			}
			requests <- request
		}
	}()

	for {
		request := <-requests

		state.logBudget = maxRequestLogSize

		response := workerFrame{Type: frameTypeResponse, ID: request.ID}
		shutdown := false

		switch request.Op {
		case opLoad:
			if err := state.evaluate(request.Entry, request.Code); err != nil {
				response.Error = err.Error()
			}
		case opBoot:
			if err := state.invokeHookJSON(HookOnBoot, request.Payload); err != nil {
				response.Error = err.Error()
			}
		case opHook:
			if err := state.invokeHookJSON(Hook(request.Hook), request.Payload); err != nil {
				response.Error = err.Error()
			}
		case opShutdown:
			shutdown = true
			if err := state.invokeHookJSON(HookOnShutdown, request.Payload); err != nil {
				response.Error = err.Error()
			}
		default:
			response.Error = fmt.Sprintf("unknown worker operation %q", request.Op)
		}

		response.OK = response.Error == ""

		if err := writeFrame(os.Stdout, response, maxFrameBytes); err != nil {
			state.release()

			return 3
		}

		if shutdown {
			state.release()

			return 0
		}
	}
}
