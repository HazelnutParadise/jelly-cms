package plugin

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	// workerArg and workerEnvMarker select the plugin worker entry point. Both
	// have to match exactly so an accidental invocation never starts a worker.
	workerArg       = "--jelly-plugin-worker-v1"
	workerEnvKey    = "JELLY_PLUGIN_WORKER"
	workerEnvMarker = "1"

	// Limits are conservative defaults from docs/tickets/02-plugin-runtime.md.
	maxManifestBytes  = 64 << 10
	maxEntryBytes     = 1 << 20
	maxPayloadBytes   = 1 << 20
	maxFrameBytes     = 2 << 20
	maxLogEntryBytes  = 16 << 10
	maxRequestLogSize = 1 << 20

	workerStartTimeout = 5 * time.Second
	workerStepTimeout  = 1 * time.Second
)

const (
	frameTypeReady    = "ready"
	frameTypeLog      = "log"
	frameTypeResponse = "response"
)

const (
	opLoad     = "load"
	opBoot     = "boot"
	opHook     = "hook"
	opShutdown = "shutdown"
)

// workerFrame is one length-prefixed JSON message read from a worker. Log
// frames may arrive between a request and its response, so logs are never
// buffered until the request finishes.
type workerFrame struct {
	Type    string `json:"type"`
	ID      uint64 `json:"id,omitempty"`
	OK      bool   `json:"ok,omitempty"`
	Error   string `json:"error,omitempty"`
	Level   string `json:"level,omitempty"`
	Message string `json:"message,omitempty"`
}

// workerRequest is one length-prefixed JSON message sent to a worker.
type workerRequest struct {
	ID      uint64          `json:"id,omitempty"`
	Op      string          `json:"op"`
	Entry   string          `json:"entry,omitempty"`
	Code    string          `json:"code,omitempty"`
	Hook    string          `json:"hook,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// writeFrame writes a frame with a four byte big-endian length prefix.
func writeFrame(w io.Writer, value any, limit int) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}

	if len(payload) > limit {
		return fmt.Errorf("frame of %d bytes exceeds limit %d", len(payload), limit)
	}

	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)

	return writeAll(w, frame)
}

// writeAll writes data completely, retrying short writes.
func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}

		data = data[n:]
	}

	return nil
}

// readFrame reads one frame and checks its declared length before allocating.
func readFrame(r io.Reader, limit int) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}

	size := binary.BigEndian.Uint32(header[:])
	if size == 0 {
		return nil, errors.New("empty frame")
	}
	if uint64(size) > uint64(limit) {
		return nil, fmt.Errorf("frame of %d bytes exceeds limit %d", size, limit)
	}

	payload := make([]byte, int(size))
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}

	return payload, nil
}
