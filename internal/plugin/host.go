package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const maxHostCallsPerRequest = 64

type hostActivationContextKey struct{}

type hostActivationContext struct{ pluginID string }

func withHostActivation(ctx context.Context, pluginID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, hostActivationContextKey{}, hostActivationContext{pluginID: pluginID})
}

// IsHostActivation reports whether this request belongs to a parent-started
// plugin load or boot operation. The worker cannot set this context value.
func IsHostActivation(ctx context.Context, pluginID string) bool {
	if ctx == nil {
		return false
	}
	scope, ok := ctx.Value(hostActivationContextKey{}).(hostActivationContext)
	return ok && scope.pluginID == pluginID
}

// HostHandler runs a permission-checked parent operation for one loaded
// plugin. Metadata is copied from its validated manifest by the runtime; it is
// never accepted from the worker's arguments.
type HostHandler func(ctx context.Context, meta PluginMetadata, method string, args json.RawMessage) (json.RawMessage, error)

// SetHostHandler atomically replaces the handler used for subsequent worker
// requests. In-flight requests keep the handler snapshot they started with.
func (m *Runtime) SetHostHandler(handler HostHandler) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.hostHandler = handler
	m.mu.Unlock()
}

func clonePluginMetadata(meta PluginMetadata) PluginMetadata {
	meta.Settings = append([]SettingSpec(nil), meta.Settings...)
	for i := range meta.Settings {
		if meta.Settings[i].Min != nil {
			value := *meta.Settings[i].Min
			meta.Settings[i].Min = &value
		}
		if meta.Settings[i].Max != nil {
			value := *meta.Settings[i].Max
			meta.Settings[i].Max = &value
		}
		meta.Settings[i].Default = cloneSettingValue(meta.Settings[i].Default)
	}
	meta.Permissions = append([]PermissionSpec(nil), meta.Permissions...)
	for i := range meta.Permissions {
		meta.Permissions[i].Hosts = append([]string(nil), meta.Permissions[i].Hosts...)
	}
	meta.Actions = append([]ActionSpec(nil), meta.Actions...)
	meta.Extensions = append([]ExtensionSpec(nil), meta.Extensions...)
	meta.WebMCPTools = append(json.RawMessage(nil), meta.WebMCPTools...)
	return meta
}

func cloneSettingValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, nested := range typed {
			cloned[key] = cloneSettingValue(nested)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for i, nested := range typed {
			cloned[i] = cloneSettingValue(nested)
		}
		return cloned
	default:
		return value
	}
}

func (m *Runtime) requestWorker(ctx context.Context, worker *workerProcess, meta PluginMetadata, request workerRequest, timeout time.Duration) (workerFrame, error) {
	m.mu.RLock()
	handler := m.hostHandler
	m.mu.RUnlock()
	if request.Op == opLoad || request.Op == opBoot {
		ctx = withHostActivation(ctx, meta.ID)
	}
	return worker.request(ctx, request, timeout, clonePluginMetadata(meta), handler)
}

func invokeHost(ctx context.Context, handler HostHandler, meta PluginMetadata, frame workerFrame) (reply workerFrame) {
	reply = workerFrame{Type: frameTypeHostReply, RequestID: frame.RequestID, CallID: frame.CallID, OK: false}
	defer func() {
		if recover() != nil {
			reply = workerFrame{Type: frameTypeHostReply, RequestID: frame.RequestID, CallID: frame.CallID, Error: "host operation failed"}
		}
	}()
	if frame.Method == "" || len(frame.Args) == 0 || len(frame.Args) > maxPayloadBytes {
		reply.Error = "invalid host request"
		return reply
	}
	if !json.Valid(frame.Args) || jsonDepth(frame.Args) > 64 {
		reply.Error = "invalid host request arguments"
		return reply
	}
	if handler == nil {
		reply.Error = "host API is unavailable"
		return reply
	}
	result, err := handler(ctx, meta, frame.Method, frame.Args)
	if err != nil {
		reply.Error = truncateUTF8(err.Error(), 1024)
		return reply
	}
	if len(result) > maxPayloadBytes || (len(result) > 0 && !json.Valid(result)) {
		reply.Error = "host API returned an invalid or oversized result"
		return reply
	}
	reply.OK = true
	if len(result) == 0 {
		reply.Undefined = true
	} else {
		reply.Result = append(json.RawMessage(nil), result...)
	}
	return reply
}

var errInvalidHostReply = errors.New("invalid host reply")

func validateHostCall(frame workerFrame, requestID uint64, lastCallID uint64, count int) error {
	if frame.RequestID != requestID || frame.CallID == 0 || frame.CallID != lastCallID+1 || count >= maxHostCallsPerRequest {
		return fmt.Errorf("%w: mismatched or excessive host call", errInvalidHostReply)
	}
	if frame.Method == "" || len(frame.Args) == 0 || len(frame.Args) > maxPayloadBytes || !json.Valid(frame.Args) || jsonDepth(frame.Args) > 64 {
		return fmt.Errorf("%w: malformed host call", errInvalidHostReply)
	}
	return nil
}

func jsonDepth(raw []byte) int {
	depth, maximum := 0, 0
	inString, escaped := false, false
	for _, b := range raw {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if b == '\\' {
				escaped = true
				continue
			}
			if b == '"' {
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > maximum {
				maximum = depth
			}
		case '}', ']':
			depth--
		}
	}
	return maximum
}
