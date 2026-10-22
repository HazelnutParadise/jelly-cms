package plugin

import (
	"errors"
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/fastschema/qjs"
)

// jellyAPIScript builds the JellyCMS object every plugin sees. The helpers it
// calls are bound to Go by installAPI.
const jellyAPIScript = `
globalThis.JellyCMS = {
	registerHook: function (hookName, callback) {
		__jelly_register_hook(hookName, callback);
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

	hooks  map[Hook][]*qjs.Value
	config map[string]*qjs.Value

	// logBudget bounds how many log bytes one request may forward. It is reset
	// by the worker loop before each request.
	logBudget int
	sendLog   func(level, message string)
}

// newWorkerJS creates the QuickJS runtime of a worker and installs the
// JellyCMS API into it.
func newWorkerJS(sendLog func(level, message string)) (*workerJS, error) {
	// The parent starts the worker inside the dedicated empty directory, so
	// the process cwd is the sandbox root. It is never the website directory
	// and there is no fallback: a worker without an isolated cwd must not run.
	workDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve plugin work directory: %w", err)
	}

	runtime, err := qjs.New(qjs.Option{
		CWD:      workDir,
		CacheDir: workerCacheDir(),
		Stdout:   os.Stderr,
		Stderr:   os.Stderr,
	})
	if err != nil {
		return nil, fmt.Errorf("create javascript runtime: %w", err)
	}

	state := &workerJS{
		runtime: runtime,
		ctx:     runtime.Context(),
		hooks:   make(map[Hook][]*qjs.Value),
		config:  make(map[string]*qjs.Value),
		sendLog: sendLog,
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
	s.ctx.SetFunc("__jelly_log", s.onLog)
	s.ctx.SetFunc("__jelly_config_get", s.onConfigGet)
	s.ctx.SetFunc("__jelly_config_set", s.onConfigSet)

	result, err := s.runtime.Eval("jelly-api.js", qjs.Code(jellyAPIScript))
	if err != nil {
		return fmt.Errorf("install JellyCMS API: %w", err)
	}
	result.Free()

	return nil
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

// onConfigGet returns a configuration value of this plugin.
func (s *workerJS) onConfigGet(this *qjs.This) (*qjs.Value, error) {
	args := this.Args()
	defer freeValues(args)

	if len(args) < 1 {
		return nil, fmt.Errorf("config.get expects a key")
	}

	key := args[0].String()
	value, ok := s.config[key]
	if !ok {
		return this.Context().NewUndefined(), nil
	}

	return value.Clone(), nil
}

// onConfigSet keeps a configuration value in memory. Persistence is not
// implemented yet, so the result tells the plugin that nothing was written to
// a durable store.
func (s *workerJS) onConfigSet(this *qjs.This) (*qjs.Value, error) {
	args := this.Args()
	defer freeValues(args)

	if len(args) < 2 {
		return nil, fmt.Errorf("config.set expects a key and a value")
	}

	key := args[0].String()
	value := args[1]

	if previous, ok := s.config[key]; ok {
		previous.Free()
	}
	s.config[key] = value.Clone()

	s.log("info", fmt.Sprintf(
		"config.set(%q) kept in memory only, it is not persisted yet", key,
	))

	ctx := this.Context()
	result := ctx.NewObject()
	result.SetPropertyStr("stored", ctx.NewBool(true))
	result.SetPropertyStr("persisted", ctx.NewBool(false))
	result.SetPropertyStr("message", ctx.NewString(
		"stored in memory for this plugin session, persistence is not implemented yet",
	))

	return result, nil
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

	for _, value := range s.config {
		value.Free()
	}

	s.hooks = nil
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
