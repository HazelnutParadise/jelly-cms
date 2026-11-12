package plugin

import (
	"bytes"
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

	// Limits are conservative defaults from ticket 2.
	maxManifestBytes  = 64 << 10
	maxEntryBytes     = 1 << 20
	maxPayloadBytes   = 1 << 20
	maxExtensionBytes = 1 << 20
	maxFrameBytes     = 2 << 20
	maxLogEntryBytes  = 16 << 10
	maxRequestLogSize = 1 << 20

	workerStartTimeout = 5 * time.Second
	workerStepTimeout  = 1 * time.Second
)

const (
	frameTypeReady     = "ready"
	frameTypeLog       = "log"
	frameTypeResponse  = "response"
	frameTypeHostCall  = "hostcall"
	frameTypeHostReply = "hostreply"
)

const (
	opLoad      = "load"
	opBoot      = "boot"
	opHook      = "hook"
	opExtension = "extension"
	opShutdown  = "shutdown"
)

// workerFrame is one length-prefixed JSON message read from a worker. Log
// frames may arrive between a request and its response, so logs are never
// buffered until the request finishes.
type workerFrame struct {
	Type       string          `json:"type"`
	ID         uint64          `json:"id,omitempty"`
	RequestID  uint64          `json:"request_id,omitempty"`
	CallID     uint64          `json:"call_id,omitempty"`
	OK         bool            `json:"ok,omitempty"`
	Error      string          `json:"error,omitempty"`
	Method     string          `json:"method,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Undefined  bool            `json:"undefined,omitempty"`
	Extensions []string        `json:"extensions,omitempty"`
	// Hooks lists the hook names the worker has registered, deduplicated in
	// first-registration order. It is always sent, so an empty list means
	// "none" and a missing (nil) list means "no snapshot in this frame".
	Hooks   []string `json:"hooks"`
	Level   string   `json:"level,omitempty"`
	Message string   `json:"message,omitempty"`
}

// workerRequest is one length-prefixed JSON message sent to a worker.
type workerRequest struct {
	ID        uint64          `json:"id,omitempty"`
	Op        string          `json:"op"`
	Metadata  *PluginMetadata `json:"metadata,omitempty"`
	Entry     string          `json:"entry,omitempty"`
	Code      string          `json:"code,omitempty"`
	Hook      string          `json:"hook,omitempty"`
	Extension string          `json:"extension,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// writeFrame writes a frame with a four byte big-endian length prefix. HTML
// is not escaped: both sides parse JSON, and escaping would triple the size
// of tag-dense payloads (content.render inputs) against the frame limit.
func writeFrame(w io.Writer, value any, limit int) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}
	payload := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))

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
