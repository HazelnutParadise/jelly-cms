package plugin

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
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
		CacheDir:           dir,
		CloseOnContextDone: true, // Keep the warm cache aligned with worker compilation.
		Stdout:             io.Discard,
		Stderr:             io.Discard,
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
	var outputMu sync.Mutex
	writeOutput := func(value any) error {
		outputMu.Lock()
		defer outputMu.Unlock()
		return writeFrame(os.Stdout, value, maxFrameBytes)
	}
	sendLog := func(level, message string) {
		frame := workerFrame{Type: frameTypeLog, Level: level, Message: message}
		// A failed log frame means the parent is gone or the protocol broke.
		// The response write below reports the failure; logs never block the
		// worker beyond the bounded frame itself.
		_ = writeOutput(frame)
	}

	hostReplies := make(chan workerFrame, 1)
	stateDone := make(chan struct{})
	sendHostCall := func(requestID, callID uint64, method string, args json.RawMessage) (json.RawMessage, bool, error) {
		if err := writeOutput(workerFrame{Type: frameTypeHostCall, RequestID: requestID, CallID: callID, Method: method, Args: args}); err != nil {
			return nil, false, err
		}
		select {
		case reply := <-hostReplies:
			if reply.RequestID != requestID || reply.CallID != callID || reply.Type != frameTypeHostReply {
				return nil, false, errors.New("host reply does not match the pending call")
			}
			if !reply.OK {
				message := reply.Error
				if message == "" {
					message = "host operation failed"
				}
				return nil, false, errors.New(truncateUTF8(message, 1024))
			}
			return reply.Result, reply.Undefined, nil
		case <-stateDone:
			return nil, false, errors.New("worker stopped")
		}
	}
	state, err := newWorkerJS(sendLog, sendHostCall)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin worker initialisation failed: %v\n", err)

		return 2
	}

	if err := writeOutput(workerFrame{Type: frameTypeReady, OK: true}); err != nil {
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
			var incoming struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(payload, &incoming); err != nil {
				fmt.Fprintf(os.Stderr, "plugin worker received an invalid request: %v\n", err)
				os.Exit(4)
			}
			if incoming.Type == frameTypeHostReply {
				var frame workerFrame
				if err := json.Unmarshal(payload, &frame); err != nil {
					fmt.Fprintf(os.Stderr, "plugin worker received an invalid host reply: %v\n", err)
					os.Exit(4)
				}
				select {
				case hostReplies <- frame:
				default:
					fmt.Fprintln(os.Stderr, "plugin worker received an unexpected host reply")
					os.Exit(4)
				}
				continue
			}
			var request workerRequest
			if err := json.Unmarshal(payload, &request); err != nil || request.Op == "" {
				fmt.Fprintf(os.Stderr, "plugin worker received an invalid request\n")
				os.Exit(4)
			}
			requests <- request
		}
	}()

	for {
		request := <-requests
		state.beginRequest(request.ID)
		if request.Metadata != nil {
			state.metadata = clonePluginMetadata(*request.Metadata)
		}
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
		case opExtension:
			result, err := state.invokeExtensionJSON(request.Extension, request.Payload)
			if err != nil {
				response.Error = err.Error()
			} else {
				response.Result = result
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
		response.Extensions = state.registeredExtensions()

		if err := writeOutput(response); err != nil {
			state.release()

			return 3
		}

		if shutdown {
			close(stateDone)
			state.release()

			return 0
		}
	}
}
