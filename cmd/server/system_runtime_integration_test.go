package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
)

type runtimeSMTPMessage struct {
	recipient string
	body      string
}

type runtimeSMTPServer struct {
	listener net.Listener
	messages chan runtimeSMTPMessage
	fail     bool
	wg       sync.WaitGroup
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
}

func startRuntimeSMTP(t *testing.T, fail bool) *runtimeSMTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for fake SMTP: %v", err)
	}
	s := &runtimeSMTPServer{listener: listener, messages: make(chan runtimeSMTPMessage, 10), fail: fail, conns: make(map[net.Conn]struct{})}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			s.wg.Add(1)
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				_ = conn.Close()
				s.wg.Done()
				continue
			}
			s.conns[conn] = struct{}{}
			s.mu.Unlock()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			go func() {
				defer s.wg.Done()
				defer func() {
					_ = conn.Close()
					s.mu.Lock()
					delete(s.conns, conn)
					s.mu.Unlock()
				}()
				_, _ = fmt.Fprint(conn, "220 localhost ESMTP\r\n")
				reader := bufio.NewReader(conn)
				recipient := ""
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					command := strings.TrimRight(line, "\r\n")
					upper := strings.ToUpper(command)
					switch {
					case strings.HasPrefix(upper, "EHLO "), strings.HasPrefix(upper, "HELO "):
						_, _ = fmt.Fprint(conn, "250 localhost\r\n")
					case strings.HasPrefix(upper, "MAIL FROM:"):
						_, _ = fmt.Fprint(conn, "250 sender accepted\r\n")
					case strings.HasPrefix(upper, "RCPT TO:"):
						recipient = strings.Trim(strings.TrimSpace(command[len("RCPT TO:"):]), "<>")
						_, _ = fmt.Fprint(conn, "250 recipient accepted\r\n")
					case upper == "DATA":
						_, _ = fmt.Fprint(conn, "354 end with dot\r\n")
						var body strings.Builder
						for {
							dataLine, readErr := reader.ReadString('\n')
							if readErr != nil {
								return
							}
							if strings.TrimRight(dataLine, "\r\n") == "." {
								break
							}
							body.WriteString(dataLine)
						}
						if s.fail {
							_, _ = fmt.Fprint(conn, "451 temporary delivery failure\r\n")
							return
						}
						select {
						case s.messages <- runtimeSMTPMessage{recipient: recipient, body: body.String()}:
						default:
						}
						_, _ = fmt.Fprint(conn, "250 queued\r\n")
					case upper == "QUIT":
						_, _ = fmt.Fprint(conn, "221 bye\r\n")
						return
					default:
						_, _ = fmt.Fprint(conn, "250 ok\r\n")
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		s.mu.Lock()
		s.closed = true
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.mu.Unlock()
		done := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("fake SMTP server did not stop within 2s")
		}
	})
	return s
}

func startSystemRuntimeForTest(t *testing.T) func() time.Duration {
	t.Helper()
	parent, cancel := context.WithCancel(context.Background())
	stop := startSystemRuntime(parent)
	var once sync.Once
	done := make(chan struct{})
	stopBounded := func() time.Duration {
		once.Do(func() {
			cancel()
			go func() {
				stop()
				close(done)
			}()
		})
		started := time.Now()
		select {
		case <-done:
			return time.Since(started)
		case <-time.After(2 * time.Second):
			t.Error("system runtime did not stop within 2s")
			return time.Since(started)
		}
	}
	t.Cleanup(func() { stopBounded() })
	return stopBounded
}

func runtimeWaitJob(t *testing.T, inspect queue.Inspector, id, state string, timeout time.Duration) queue.JobInfo {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		job, err := inspect.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("read runtime job %s: %v", id, err)
		}
		if job.State == state || (state == "retry" && job.State == "queued" && job.Attempts > 0) || (state == "error" && job.Attempts > 0 && job.LastError != "") {
			return job
		}
		time.Sleep(25 * time.Millisecond)
	}
	job, err := inspect.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("read timed-out runtime job %s: %v", id, err)
	}
	return job
}

func TestSystemRuntimeRegressionProcessesMailAndStopsCleanly(t *testing.T) {
	requireBootPostgres(t)
	admin := openBootHandle(t, bootAdminConfig())
	database := createBootDatabase(t, admin)
	t.Chdir(t.TempDir())
	useBootGlobals(t)
	cfg := bootDatabaseConfig(database)
	if err := config.Save(&config.Config{Database: cfg}); err != nil {
		t.Fatalf("write isolated test site config: %v", err)
	}
	if err := initializeSite(); err != nil {
		t.Fatalf("initialize isolated PostgreSQL site: %v", err)
	}

	ctx := context.Background()
	if err := settings.Set(ctx, 0, map[string]string{
		"mail_enabled": "true", "mail_host": "127.0.0.1", "mail_port": "2525",
		"mail_from": "runtime@example.test", "mail_tls": "none", "queue_paused": "true",
	}); err != nil {
		t.Fatalf("configure isolated local mail delivery: %v", err)
	}
	q, err := queue.Open(queue.Config{Driver: queue.DriverPostgres, Workers: 1})
	if err != nil {
		t.Fatalf("open PostgreSQL mail queue: %v", err)
	}
	defer q.Close()
	inspector, ok := q.(queue.Inspector)
	if !ok {
		t.Fatal("PostgreSQL queue does not expose job inspection")
	}

	smtp := startRuntimeSMTP(t, false)
	port := smtp.listener.Addr().(*net.TCPAddr).Port
	if err := settings.Set(ctx, 0, map[string]string{"mail_port": fmt.Sprint(port)}); err != nil {
		t.Fatalf("set fake SMTP port: %v", err)
	}
	enqueue := func(to string) string {
		t.Helper()
		payload, err := json.Marshal(map[string]string{
			"to": to, "kind": "test", "subject": "Runtime regression", "body": "runtime-body-marker",
		})
		if err != nil {
			t.Fatal(err)
		}
		id, err := q.Enqueue(ctx, queue.Job{Queue: queue.QueueMail, Kind: "mail.send", Payload: payload, MaxAttempts: queue.DefaultMaxAttempts})
		if err != nil {
			t.Fatalf("enqueue mail.send: %v", err)
		}
		return id
	}

	pausedID := enqueue("paused@example.test")
	stop := startSystemRuntimeForTest(t)
	time.Sleep(500 * time.Millisecond)
	paused, err := inspector.Get(ctx, pausedID)
	if err != nil {
		t.Fatalf("inspect paused mail job: %v", err)
	}
	if paused.State != "queued" || paused.Attempts != 0 {
		t.Errorf("paused mail job = state %q attempts %d, want queued and untouched", paused.State, paused.Attempts)
	}
	if len(smtp.messages) != 0 {
		t.Errorf("paused queue delivered %d SMTP messages", len(smtp.messages))
	}
	if err := settings.Set(ctx, 0, map[string]string{"queue_paused": "false"}); err != nil {
		t.Fatalf("resume isolated mail queue: %v", err)
	}

	id := enqueue("recipient+full@example.test")
	pausedDone := runtimeWaitJob(t, inspector, pausedID, "done", 8*time.Second)
	fullDone := runtimeWaitJob(t, inspector, id, "done", 8*time.Second)
	for label, job := range map[string]queue.JobInfo{"paused": pausedDone, "full-recipient": fullDone} {
		if job.State != "done" {
			t.Errorf("%s mail job after resume = %q with %d attempts, want done", label, job.State, job.Attempts)
		}
	}
	wantRecipients := map[string]bool{"paused@example.test": false, "recipient+full@example.test": false}
	captureDeadline := time.NewTimer(5 * time.Second)
	defer captureDeadline.Stop()
	for range 2 {
		select {
		case message := <-smtp.messages:
			if _, expected := wantRecipients[message.recipient]; !expected {
				t.Errorf("SMTP recipient = %q, want one of the two queued recipients", message.recipient)
				continue
			}
			wantRecipients[message.recipient] = true
			if !strings.Contains(message.body, message.recipient) || !strings.Contains(message.body, "runtime-body-marker") {
				t.Errorf("SMTP body for %s omitted the recipient or message: %q", message.recipient, message.body)
			}
		case <-captureDeadline.C:
			t.Fatal("both acknowledged mail jobs must produce captured SMTP messages")
		}
	}
	for recipient, captured := range wantRecipients {
		if !captured {
			t.Errorf("no SMTP message captured for %s", recipient)
		}
	}

	stopStart := time.Now()
	stopElapsed := stop()
	if stopElapsed > 2*time.Second || time.Since(stopStart) > 2*time.Second {
		t.Errorf("system runtime shutdown took %s, want bounded under 2s", time.Since(stopStart))
	}
	before := len(smtp.messages)
	postStopID := enqueue("after-stop@example.test")
	time.Sleep(300 * time.Millisecond)
	postStop, err := inspector.Get(ctx, postStopID)
	if err != nil {
		t.Fatalf("inspect mail queued after shutdown: %v", err)
	}
	if postStop.State != "queued" || postStop.Attempts != 0 {
		t.Errorf("job after runtime shutdown = state %q attempts %d, want untouched queued job", postStop.State, postStop.Attempts)
	}
	if got := len(smtp.messages); got != before {
		t.Errorf("SMTP side effects continued after shutdown: count %d -> %d", before, got)
	}

	failureServer := startRuntimeSMTP(t, true)
	if err := settings.Set(ctx, 0, map[string]string{"mail_port": fmt.Sprint(failureServer.listener.Addr().(*net.TCPAddr).Port)}); err != nil {
		t.Fatalf("configure failing local SMTP: %v", err)
	}
	failedID := enqueue("failure@example.test")
	failStop := startSystemRuntimeForTest(t)
	failed := runtimeWaitJob(t, inspector, failedID, "error", 8*time.Second)
	_ = failStop()
	if failed.Attempts == 0 || failed.LastError == "" {
		t.Errorf("SMTP failure job = state %q attempts %d error %q, want recorded retry", failed.State, failed.Attempts, failed.LastError)
	}
}
