package plugin

import (
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

type pluginLogLine struct {
	text   string
	writer io.Writer
	done   chan struct{}
}

const workerLogWriteBudget = 2 * time.Millisecond

var (
	workerLogOnce  sync.Once
	workerLogQueue = make(chan pluginLogLine, workerLogQueueSize)
)

func enqueuePluginLog(pluginID, level, message string) {
	var line string
	switch level {
	case "error":
		line = fmt.Sprintf("[Plugin %s] [error] %s\n", pluginID, message)
	case "warn":
		line = fmt.Sprintf("[Plugin %s] [warn] %s\n", pluginID, message)
	case "debug":
		line = fmt.Sprintf("[Plugin %s] [debug] %s\n", pluginID, message)
	default:
		line = fmt.Sprintf("[Plugin %s] %s\n", pluginID, message)
	}
	enqueueRuntimeLog(line)
}

func enqueueRuntimeLog(line string) {
	workerLogOnce.Do(func() {
		go func() {
			for entry := range workerLogQueue {
				_, _ = io.WriteString(entry.writer, entry.text)
				close(entry.done)
			}
		}()
	})
	if len(line) > maxLogEntryBytes+512 {
		line = line[:maxLogEntryBytes+512]
	}
	entry := pluginLogLine{text: line, writer: os.Stdout, done: make(chan struct{})}
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
