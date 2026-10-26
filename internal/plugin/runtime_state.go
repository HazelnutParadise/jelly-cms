package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/ext"
	"github.com/fastschema/qjs"
)

// jellyAPIScript builds the JellyCMS object every plugin sees. The helpers it
// calls are bound to Go by installAPI.
const jellyAPIScript = `
function __jellyHost(method, args) {
  if (args.some(function (value) { return typeof value === "undefined"; })) {
    throw new TypeError("host API arguments must be JSON values");
  }
  const result = __jelly_host_call(method, JSON.stringify(args));
  return result === undefined ? undefined : JSON.parse(result);
}
globalThis.JellyCMS = {
	registerHook: function (hookName, callback) {
		__jelly_register_hook(hookName, callback);
	},
	registerExtension: function (point, callback) {
		__jelly_register_extension(point, callback);
	},
	log: {
		info: function (message) { __jelly_log("info", message); },
		warn: function (message) { __jelly_log("warn", message); },
		error: function (message) { __jelly_log("error", message); },
		debug: function (message) { __jelly_log("debug", message); },
	},
	config: {
		get: function (key) { return __jelly_config_get(key); },
		set: function (key, value) { return __jelly_config_set(key, value); },
	},
	settings: {
		get: function (key) { return __jellyHost("settings.get", [key]); },
		set: function (key, value) { return __jellyHost("settings.set", [key, value]); },
	},
	store: {
		get: function (collection, key) { return __jellyHost("store.get", [collection, key]); },
		set: function (collection, key, value) { return __jellyHost("store.set", [collection, key, value]); },
		delete: function (collection, key) { return __jellyHost("store.delete", [collection, key]); },
	},
	http: {
		fetch: function (url, options) { return __jellyHost("http.fetch", [url, options || {}]); },
	},
};
globalThis.console = {
	log: function (message) { __jelly_log("info", message); },
	info: function (message) { __jelly_log("info", message); },
	warn: function (message) { __jelly_log("warn", message); },
	error: function (message) { __jelly_log("error", message); },
	debug: function (message) { __jelly_log("debug", message); },
};
`

// workerJS owns the QuickJS runtime of one plugin worker. It is only used
// inside the worker process, so callbacks and requests are already serialised
// by the worker loop and no locks are needed here.
type workerJS struct {
	runtime *qjs.Runtime
	ctx     *qjs.Context
	// jsonStringify keeps the runtime's original JSON.stringify even if a
	// plugin replaces the global function while evaluating its entry script.
	jsonStringify *qjs.Value

	hooks  map[Hook][]*qjs.Value
	config map[string]*qjs.Value

	extensions        map[string]*qjs.Value
	activeExtensions  map[string]int
	retiredExtensions map[string][]*qjs.Value

	// logBudget bounds how many log bytes one request may forward. It is reset
	// by the worker loop before each request.
	logBudget int
	sendLog   func(level, message string)
	sendHost  func(requestID, callID uint64, method string, args json.RawMessage) (json.RawMessage, bool, error)
	requestID uint64
	callID    uint64
	metadata  PluginMetadata
}

// newWorkerJS creates the QuickJS runtime of a worker and installs the
// JellyCMS API into it.
func newWorkerJS(sendLog func(level, message string), sendHost func(uint64, uint64, string, json.RawMessage) (json.RawMessage, bool, error)) (*workerJS, error) {
	// The parent starts the worker inside the dedicated empty directory, so
	// the process cwd is the sandbox root. It is never the website directory
	// and there is no fallback: a worker without an isolated cwd must not run.
	workDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve plugin work directory: %w", err)
	}

	runtime, err := qjs.New(qjs.Option{
		CWD:                workDir,
		CacheDir:           workerCacheDir(),
		CloseOnContextDone: true, // Let Wazero return to Go for scheduling and GC checks.
		Stdout:             os.Stderr,
		Stderr:             os.Stderr,
	})
	if err != nil {
		return nil, fmt.Errorf("create javascript runtime: %w", err)
	}

	state := &workerJS{
		runtime:           runtime,
		ctx:               runtime.Context(),
		hooks:             make(map[Hook][]*qjs.Value),
		config:            make(map[string]*qjs.Value),
		extensions:        make(map[string]*qjs.Value),
		activeExtensions:  make(map[string]int),
		retiredExtensions: make(map[string][]*qjs.Value),
		sendLog:           sendLog,
		sendHost:          sendHost,
	}
	jsonObject := state.ctx.Global().GetPropertyStr("JSON")
	if jsonObject == nil {
		state.release()
		return nil, errors.New("capture JSON serializer: JSON is unavailable")
	}
	state.jsonStringify = jsonObject.GetPropertyStr("stringify")
	jsonObject.Free()
	if state.jsonStringify == nil || !state.jsonStringify.IsFunction() {
		state.release()
		return nil, errors.New("capture JSON serializer: JSON.stringify is unavailable")
	}

	if err := state.installAPI(); err != nil {
		state.release()

		return nil, err
	}

	return state, nil
}

// installAPI binds the Go side of the JellyCMS API and exposes it to the
// plugin as a global object.
func (s *workerJS) installAPI() error {
	s.ctx.SetFunc("__jelly_register_hook", s.onRegisterHook)
	s.ctx.SetFunc("__jelly_register_extension", s.onRegisterExtension)
	s.ctx.SetFunc("__jelly_log", s.onLog)
	s.ctx.SetFunc("__jelly_host_call", s.onHostCall)
	s.ctx.SetFunc("__jelly_config_get", s.onConfigGet)
	s.ctx.SetFunc("__jelly_config_set", s.onConfigSet)

	result, err := s.runtime.Eval("jelly-api.js", qjs.Code(jellyAPIScript))
	if err != nil {
		return fmt.Errorf("install JellyCMS API: %w", err)
	}
	result.Free()

	return nil
}

func (s *workerJS) beginRequest(requestID uint64) {
	s.requestID = requestID
	s.callID = 0
}

// evaluate runs the entry script inside the worker runtime.
func (s *workerJS) evaluate(name, code string) error {
	if code == "" {
		return nil
	}

	if name == "" {
		name = "plugin-entry.js"
	}

	result, err := s.runtime.Eval(name, qjs.Code(code))
	if err != nil {
		return fmt.Errorf("entry script failed: %w", err)
	}
	result.Free()

	return nil
}

// invokeHookJSON hands a JSON payload to every callback registered for the
// hook. The payload is parsed inside QuickJS, so no Go value proxy is created
// and no host callback is retained by the engine.
func (s *workerJS) invokeHookJSON(hook Hook, payload []byte) error {
	callbacks := append([]*qjs.Value(nil), s.hooks[hook]...)
	if len(callbacks) == 0 {
		return nil
	}

	payloadValue, err := s.parsePayload(payload)
	if err != nil {
		return fmt.Errorf("build payload for hook %s: %w", hook, err)
	}
	defer payloadValue.Free()

	this := s.ctx.NewUndefined()
	defer this.Free()

	var failures []error

	for _, callback := range callbacks {
		result, err := s.ctx.Invoke(callback, this, payloadValue)
		if err != nil {
			failures = append(failures, fmt.Errorf("hook %s failed: %w", hook, err))

			continue
		}

		if result != nil {
			result.Free()
		}
	}

	return errors.Join(failures...)
}

// invokeExtensionJSON invokes the single callback registered for a shipped
// extension point and returns its JSON-serializable result.
func (s *workerJS) invokeExtensionJSON(point string, payload []byte) (json.RawMessage, error) {
	callback := s.extensions[point]
	if callback == nil {
		return nil, ErrExtensionUnavailable
	}
	if len(payload) == 0 || len(payload) > maxExtensionBytes || !json.Valid(payload) || jsonDepth(payload) > 64 {
		return nil, errors.New("extension input is invalid or too large")
	}

	input, err := s.parsePayload(payload)
	if err != nil {
		return nil, fmt.Errorf("parse extension input: %w", err)
	}
	defer input.Free()

	this := s.ctx.NewUndefined()
	defer this.Free()

	s.activeExtensions[point]++
	result, invokeErr := s.ctx.Invoke(callback, this, input)
	s.activeExtensions[point]--
	if s.activeExtensions[point] == 0 {
		for _, retired := range s.retiredExtensions[point] {
			retired.Free()
		}
		delete(s.retiredExtensions, point)
	}
	if invokeErr != nil {
		return nil, fmt.Errorf("extension %s failed: %w", point, invokeErr)
	}
	if result == nil {
		return nil, errors.New("extension returned no result")
	}
	defer result.Free()

	encoded, err := s.stringifyJSON(result)
	if err != nil || len(encoded) == 0 || len(encoded) > maxExtensionBytes || !json.Valid([]byte(encoded)) || jsonDepth([]byte(encoded)) > 64 {
		return nil, errors.New("extension returned an unserializable or oversized result")
	}
	return json.RawMessage(encoded), nil
}

func (s *workerJS) registeredExtensions() []string {
	points := make([]string, 0, len(s.extensions))
	for _, point := range []string{ext.PointSearchEngine, ext.PointMediaStorage} {
		if s.extensions[point] != nil {
			points = append(points, point)
		}
	}
	return points
}

// parsePayload parses a complete JSON payload. Invalid JSON is rejected before
// any callback runs.
func (s *workerJS) parsePayload(payload []byte) (*qjs.Value, error) {
	if len(payload) == 0 {
		return s.ctx.NewObject(), nil
	}

	value := s.ctx.ParseJSON(string(payload))
	if value == nil {
		return nil, errors.New("payload parser returned no value")
	}

	if s.ctx.HasException() {
		exception := s.ctx.Exception()
		value.Free()

		if exception == nil {
			exception = errors.New("invalid JSON payload")
		}

		return nil, fmt.Errorf("parse payload: %w", exception)
	}

	if value.IsError() {
		exception := value.Exception()
		value.Free()

		return nil, fmt.Errorf("parse payload: %w", exception)
	}

	return value, nil
}

// log forwards one bounded log entry to the parent. Logs are sent immediately
// rather than buffered until the request finishes.
func (s *workerJS) log(level, message string) {
	if s.logBudget <= 0 {
		return
	}

	message = truncateUTF8(message, maxLogEntryBytes)
	if len(message) > s.logBudget {
		message = truncateUTF8(message, s.logBudget)
	}
	if message == "" {
		return
	}

	if s.sendLog != nil {
		s.sendLog(level, message)
	}

	s.logBudget -= len(message)
}

// onRegisterHook stores a callback for a hook.
func (s *workerJS) onRegisterHook(this *qjs.This) (*qjs.Value, error) {
	args := this.Args()
	defer freeValues(args)

	if len(args) < 2 {
		return nil, fmt.Errorf("registerHook expects a hook name and a callback")
	}

	name := args[0].String()
	callback := args[1]

	if !callback.IsFunction() {
		return nil, fmt.Errorf("registerHook(%q) expects a function callback", name)
	}

	hook := Hook(name)
	s.hooks[hook] = append(s.hooks[hook], callback.Clone())

	return this.Context().NewUndefined(), nil
}

// onRegisterExtension accepts callbacks only for shipped points declared by
// this plugin's manifest. A second registration replaces the first callback.
func (s *workerJS) onRegisterExtension(this *qjs.This) (*qjs.Value, error) {
	args := this.Args()
	defer freeValues(args)
	if len(args) != 2 {
		return nil, errors.New("registerExtension expects a point and a callback")
	}
	if !args[0].IsString() {
		return nil, errors.New("registerExtension point must be a string")
	}
	point := args[0].String()
	if !extensionPointShipped(point) {
		return nil, fmt.Errorf("extension point %q is not shipped", point)
	}
	if !extensionDeclared(s.metadata, point) {
		return nil, fmt.Errorf("extension point %q is not declared by this plugin", point)
	}
	callback := args[1]
	if !callback.IsFunction() {
		return nil, fmt.Errorf("registerExtension(%q) expects a function callback", point)
	}
	if previous := s.extensions[point]; previous != nil {
		if s.activeExtensions[point] > 0 {
			s.retiredExtensions[point] = append(s.retiredExtensions[point], previous)
		} else {
			previous.Free()
		}
	}
	s.extensions[point] = callback.Clone()
	return this.Context().NewUndefined(), nil
}

// onLog forwards a plugin log line to the parent process.
func (s *workerJS) onLog(this *qjs.This) (*qjs.Value, error) {
	args := this.Args()
	defer freeValues(args)

	level := "info"
	message := ""

	if len(args) > 0 {
		level = args[0].String()
	}

	if len(args) > 1 {
		message = args[1].String()
	}

	s.log(level, message)

	return this.Context().NewUndefined(), nil
}

// onHostCall synchronously proxies one bounded API operation to the parent.
func (s *workerJS) onHostCall(this *qjs.This) (*qjs.Value, error) {
	args := this.Args()
	defer freeValues(args)
	if len(args) != 2 {
		return nil, fmt.Errorf("host API expects a method and JSON arguments")
	}
	method := args[0].String()
	encoded := args[1].String()
	if len(encoded) == 0 || len(encoded) > maxPayloadBytes || !json.Valid([]byte(encoded)) {
		return nil, fmt.Errorf("host API arguments are invalid or too large")
	}
	result, undefined, err := s.callHost(method, json.RawMessage(encoded))
	if err != nil {
		return nil, err
	}
	if undefined {
		return this.Context().NewUndefined(), nil
	}
	if len(result) == 0 || len(result) > maxPayloadBytes || !json.Valid(result) {
		return nil, errors.New("host returned invalid JSON")
	}
	return this.Context().NewString(string(result)), nil
}

func (s *workerJS) onConfigGet(this *qjs.This) (*qjs.Value, error) {
	args := this.Args()
	defer freeValues(args)
	if len(args) < 1 {
		return nil, errors.New("config.get expects a key")
	}
	key := args[0].String()
	if !s.hasPermission("settings") {
		value, ok := s.config[key]
		if !ok {
			return this.Context().NewUndefined(), nil
		}
		return value.Clone(), nil
	}
	encoded, err := json.Marshal([]string{key})
	if err != nil {
		return nil, errors.New("config.get key is invalid")
	}
	result, undefined, err := s.callHost("config.get", encoded)
	if err != nil {
		return nil, err
	}
	return s.hostResultValue(this.Context(), result, undefined)
}

func (s *workerJS) onConfigSet(this *qjs.This) (*qjs.Value, error) {
	args := this.Args()
	defer freeValues(args)
	if len(args) < 2 {
		return nil, errors.New("config.set expects a key and a value")
	}
	key, value := args[0].String(), args[1]
	if !s.hasPermission("settings") {
		if previous, ok := s.config[key]; ok {
			previous.Free()
		}
		s.config[key] = value.Clone()
		s.log("info", fmt.Sprintf("config.set(%q) kept in memory only, it is not persisted yet", key))
		result := this.Context().NewObject()
		result.SetPropertyStr("stored", this.Context().NewBool(true))
		result.SetPropertyStr("persisted", this.Context().NewBool(false))
		result.SetPropertyStr("message", this.Context().NewString("stored in memory for this plugin session, persistence is not implemented yet"))
		return result, nil
	}
	encodedValue, err := s.stringifyJSON(value)
	if err != nil || !json.Valid([]byte(encodedValue)) {
		return nil, errors.New("config.set value must be a JSON value")
	}
	keyJSON, err := json.Marshal(key)
	if err != nil {
		return nil, errors.New("config.set key is invalid")
	}
	hostArgs, err := json.Marshal([]json.RawMessage{keyJSON, json.RawMessage(encodedValue)})
	if err != nil {
		return nil, errors.New("config.set arguments are invalid")
	}
	result, undefined, err := s.callHost("config.set", hostArgs)
	if err != nil {
		return nil, err
	}
	return s.hostResultValue(this.Context(), result, undefined)
}

func (s *workerJS) callHost(method string, args json.RawMessage) (json.RawMessage, bool, error) {
	if s.sendHost == nil {
		return nil, false, errors.New("host API is unavailable")
	}
	if len(args) == 0 || len(args) > maxPayloadBytes || !json.Valid(args) || jsonDepth(args) > 64 {
		return nil, false, errors.New("host API arguments are invalid or too large")
	}
	s.callID++
	if s.callID > maxHostCallsPerRequest {
		return nil, false, errors.New("host request limit exceeded")
	}
	return s.sendHost(s.requestID, s.callID, method, args)
}

func (s *workerJS) hostResultValue(ctx *qjs.Context, result json.RawMessage, undefined bool) (*qjs.Value, error) {
	if undefined {
		return ctx.NewUndefined(), nil
	}
	if len(result) == 0 || len(result) > maxPayloadBytes || !json.Valid(result) {
		return nil, errors.New("host returned invalid JSON")
	}
	value := ctx.ParseJSON(string(result))
	if value == nil {
		return nil, errors.New("host returned invalid JSON")
	}
	if ctx.HasException() || value.IsError() {
		value.Free()
		return nil, errors.New("host returned invalid JSON")
	}
	return value, nil
}

func (s *workerJS) hasPermission(name string) bool {
	for _, permission := range s.metadata.Permissions {
		if permission.Name == name {
			return true
		}
	}
	return false
}

// release frees the runtime and every value the plugin owns.
func (s *workerJS) release() {
	if s.runtime == nil && s.ctx == nil {
		return
	}

	for _, callbacks := range s.hooks {
		for _, callback := range callbacks {
			callback.Free()
		}
	}
	for _, callback := range s.extensions {
		callback.Free()
	}
	for _, callbacks := range s.retiredExtensions {
		for _, callback := range callbacks {
			callback.Free()
		}
	}

	for _, value := range s.config {
		value.Free()
	}
	if s.jsonStringify != nil {
		s.jsonStringify.Free()
		s.jsonStringify = nil
	}

	s.hooks = nil
	s.extensions = nil
	s.activeExtensions = nil
	s.retiredExtensions = nil
	s.config = nil

	if s.runtime != nil {
		s.runtime.Close()
		s.runtime = nil
	}

	s.ctx = nil
}

// freeValues releases the values handed over by the JavaScript engine.
func freeValues(values []*qjs.Value) {
	for _, value := range values {
		value.Free()
	}
}

// truncateUTF8 cuts s to at most limit bytes without splitting a rune.
func truncateUTF8(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}

	b := []byte(s[:limit])
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}

	return string(b)
}
