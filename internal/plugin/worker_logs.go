package plugin

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Plugin output is deliberately detached from protocol processing. A single
// bounded process-wide queue prevents a blocked stdout from stalling a worker
// reader or creating one permanently blocked goroutine per runtime.
const workerLogQueueSize = 512

// pluginLogLine is one line waiting for the log goroutine. A plugin line keeps
// its plugin id, level and message, so the goroutine can hand it to the log
// sink after the stdout write. A runtime line has forPlugin false.
type pluginLogLine struct {
	text      string
	writer    io.Writer
	done      chan struct{}
	forPlugin bool
	pluginID  string
	level     string
	message   string
	// marker lines carry no text; FlushLogs uses them to learn when every
	// line queued before it has been written and handed to the sink.
	marker bool
}

const workerLogWriteBudget = 2 * time.Millisecond

var (
	workerLogOnce  sync.Once
	workerLogQueue = make(chan pluginLogLine, workerLogQueueSize)
)

// enqueuePluginLog writes a plugin log line or a hook, extension or shutdown
// failure. The log sink receives it as well.
func enqueuePluginLog(pluginID, level, message string) {
	enqueueLogLine(pluginLogLine{
		text:      formatPluginLine(pluginID, level, message),
		writer:    os.Stdout,
		forPlugin: true,
		pluginID:  pluginID,
		level:     level,
		message:   message,
	})
}

// enqueuePluginNotice writes a lifecycle line such as "loaded version". It is
// written to stdout only; the log sink receives plugin output and failures.
func enqueuePluginNotice(pluginID, level, message string) {
	enqueueLogLine(pluginLogLine{text: formatPluginLine(pluginID, level, message), writer: os.Stdout})
}

func formatPluginLine(pluginID, level, message string) string {
	switch level {
	case "error":
		return fmt.Sprintf("[Plugin %s] [error] %s\n", pluginID, message)
	case "warn":
		return fmt.Sprintf("[Plugin %s] [warn] %s\n", pluginID, message)
	case "debug":
		return fmt.Sprintf("[Plugin %s] [debug] %s\n", pluginID, message)
	default:
		return fmt.Sprintf("[Plugin %s] %s\n", pluginID, message)
	}
}

// enqueueRuntimeLog writes a runtime line. Runtime lines have no plugin id and
// never reach the log sink.
func enqueueRuntimeLog(line string) {
	enqueueLogLine(pluginLogLine{text: line, writer: os.Stdout})
}

func enqueueLogLine(entry pluginLogLine) {
	workerLogOnce.Do(startLogWriter)
	if len(entry.text) > maxLogEntryBytes+512 {
		entry.text = entry.text[:maxLogEntryBytes+512]
	}
	entry.done = make(chan struct{})
	select {
	case workerLogQueue <- entry:
	default:
		// Discard one old line to keep recent status/error markers visible.
		select {
		case dropped := <-workerLogQueue:
			close(dropped.done)
		default:
		}
		select {
		case workerLogQueue <- entry:
		default:
			close(entry.done)
			return
		}
	}
	timer := time.NewTimer(workerLogWriteBudget)
	defer timer.Stop()
	select {
	case <-entry.done:
	case <-timer.C:
	}
}

// startLogWriter starts the single goroutine that writes queued lines in order
// and then hands plugin lines to the log sink.
func startLogWriter() {
	go func() {
		for entry := range workerLogQueue {
			if !entry.marker {
				_, _ = io.WriteString(entry.writer, entry.text)
				if entry.forPlugin {
					deliverLogSink(entry.pluginID, entry.level, entry.message)
				}
			}
			close(entry.done)
		}
	}()
}

// FlushLogs blocks until every plugin and runtime log line queued before the
// call has been written and handed to the log sink, or until ctx ends. Tests
// and shutdown paths use it so a sink that writes asynchronously is not still
// running when its dependencies go away. Unlike ordinary lines the marker is
// never dropped: the call waits for queue space.
func FlushLogs(ctx context.Context) error {
	workerLogOnce.Do(startLogWriter)
	entry := pluginLogLine{marker: true, done: make(chan struct{})}
	select {
	case workerLogQueue <- entry:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-entry.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// deliverLogSink calls the installed sink. A panic inside the sink is recovered
// so the log goroutine keeps running.
func deliverLogSink(pluginID, level, message string) {
	sink := currentLogSink()
	if sink == nil {
		return
	}
	defer func() { _ = recover() }()

	sink(pluginID, level, message)
}
