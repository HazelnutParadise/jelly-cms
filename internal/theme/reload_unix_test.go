//go:build unix

package theme

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

const reloadGateTimeout = 5 * time.Second

type reloadGate struct {
	path                  string
	original, payload     []byte
	opened                chan error
	release, stop         chan struct{}
	result                chan error
	done                  chan struct{}
	releaseOnce, stopOnce sync.Once
	finishOnce            sync.Once
}

func blockThemeJSON(t *testing.T, root, name string) *reloadGate {
	t.Helper()
	p := filepath.Join(root, name, "theme.json")
	original, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(p, 0600); err != nil {
		t.Fatal(err)
	}
	g := &reloadGate{path: p, original: original, payload: append([]byte(nil), original...), opened: make(chan error, 1), release: make(chan struct{}), stop: make(chan struct{}), result: make(chan error, 1), done: make(chan struct{})}
	t.Cleanup(func() {
		if err := g.finish(); err != nil {
			t.Errorf("finish FIFO writer during cleanup: %v", err)
		}
		if err := replaceFile(p, original); err != nil {
			t.Errorf("restore theme.json during cleanup: %v", err)
		}
	})
	go func() {
		defer close(g.done)
		fd, e := openFIFOWriter(p, g.stop)
		g.opened <- e
		if e == nil {
			f := os.NewFile(uintptr(fd), p)
			<-g.release
			_, e = f.Write(g.payload)
			ce := f.Close()
			if e == nil {
				e = ce
			}
		}
		g.result <- e
	}()
	return g
}

func openFIFOWriter(path string, stop <-chan struct{}) (int, error) {
	deadline := time.NewTimer(reloadGateTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			return fd, nil
		}
		if !errors.Is(err, syscall.ENXIO) && !errors.Is(err, syscall.ENOENT) {
			return -1, err
		}
		select {
		case <-stop:
			return -1, errors.New("FIFO writer cancelled")
		case <-deadline.C:
			return -1, errors.New("timed out waiting for FIFO reader")
		case <-tick.C:
		}
	}
}

func replaceFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".theme-json-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func (g *reloadGate) waitOpened(t *testing.T) {
	t.Helper()
	select {
	case err := <-g.opened:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(reloadGateTimeout):
		t.Fatal("ReloadActive did not open FIFO theme.json")
	}
}

func (g *reloadGate) restorePath(t *testing.T) {
	t.Helper()
	if err := replaceFile(g.path, g.original); err != nil {
		t.Fatal(err)
	}
}

func (g *reloadGate) unblock(t *testing.T) {
	t.Helper()
	g.releaseOnce.Do(func() { close(g.release) })
	select {
	case err := <-g.result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(reloadGateTimeout):
		t.Fatal("FIFO writer did not finish")
	}
}

func (g *reloadGate) finish() error {
	var finishErr error
	g.finishOnce.Do(func() {
		fd, err := syscall.Open(g.path, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
		if err == nil {
			defer syscall.Close(fd)
		}
		g.releaseOnce.Do(func() { close(g.release) })
		g.stopOnce.Do(func() { close(g.stop) })
		select {
		case <-g.done:
		case <-time.After(reloadGateTimeout):
			finishErr = errors.New("FIFO writer did not stop")
		}
	})
	return finishErr
}

func TestThemeReloadActiveDiscardsStaleSuccessAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		activate func(*testing.T, *Manager)
	}{
		{"switch-to-b", func(t *testing.T, m *Manager) {
			if err := m.Activate("b"); err != nil {
				t.Fatal(err)
			}
		}},
		{"same-name-reactivation", func(t *testing.T, m *Manager) {
			if err := m.Activate("a"); err != nil {
				t.Fatal(err)
			}
		}},
		{"newer-reload", func(t *testing.T, m *Manager) {
			if err := m.ReloadActive(); err != nil {
				t.Fatal(err)
			}
		}},
		{"aba", func(t *testing.T, m *Manager) {
			if err := m.Activate("b"); err != nil {
				t.Fatal(err)
			}
			if err := m.Activate("a"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name+"/success", func(t *testing.T) { assertStaleReload(t, tc.activate, false) })
		t.Run(tc.name+"/failure", func(t *testing.T) { assertStaleReload(t, tc.activate, true) })
	}
}

func assertStaleReload(t *testing.T, activate func(*testing.T, *Manager), fail bool) {
	t.Helper()
	root := t.TempDir()
	persistenceTheme(t, root, "a", "A")
	persistenceTheme(t, root, "b", "B")
	m := NewManager(root)
	if err := m.Activate("a"); err != nil {
		t.Fatal(err)
	}
	gate := blockThemeJSON(t, root, "a")
	resultCh := make(chan error, 1)
	reloadDone := make(chan struct{})
	t.Cleanup(func() {
		if err := gate.finish(); err != nil {
			t.Errorf("finish reload gate: %v", err)
		}
		select {
		case <-reloadDone:
		case <-time.After(reloadGateTimeout):
			t.Errorf("reload goroutine did not stop during cleanup")
		}
	})
	go func() { defer close(reloadDone); resultCh <- m.ReloadActive() }()
	gate.waitOpened(t)
	gate.restorePath(t)
	activate(t, m)
	wantName, wantSnapshot, wantURL := m.GetActive(), m.AssetSnapshot(), m.AssetURL("/theme/assets/site.css")
	if fail {
		gate.payload = []byte("{")
	}
	gate.unblock(t)
	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("stale reload returned error: %v", err)
		}
	case <-time.After(reloadGateTimeout):
		t.Fatal("ReloadActive did not finish after FIFO release")
	}
	if m.GetActive() != wantName || m.AssetSnapshot() != wantSnapshot || m.AssetURL("/theme/assets/site.css") != wantURL {
		t.Fatalf("stale reload overwrote state: active=%q want=%q snapshotSame=%v", m.GetActive(), wantName, m.AssetSnapshot() == wantSnapshot)
	}
}

func TestThemeReloadActiveCurrentFailureAfterRegisterFuncReturnsError(t *testing.T) {
	root := t.TempDir()
	persistenceTheme(t, root, "a", "A")
	m := NewManager(root)
	if err := m.Activate("a"); err != nil {
		t.Fatal(err)
	}
	before := m.AssetSnapshot()
	gate := blockThemeJSON(t, root, "a")
	resultCh := make(chan error, 1)
	done := make(chan struct{})
	t.Cleanup(func() {
		if err := gate.finish(); err != nil {
			t.Errorf("finish reload gate: %v", err)
		}
		select {
		case <-done:
		case <-time.After(reloadGateTimeout):
			t.Errorf("reload goroutine did not stop during cleanup")
		}
	})
	go func() { defer close(done); resultCh <- m.ReloadActive() }()
	gate.waitOpened(t)
	gate.restorePath(t)
	m.RegisterFunc("reloadRegression", func() string { return "registered" })
	gate.payload = []byte("{")
	gate.unblock(t)
	select {
	case err := <-resultCh:
		if err == nil {
			t.Fatal("current reload failure was suppressed after RegisterFunc")
		}
	case <-time.After(reloadGateTimeout):
		t.Fatal("ReloadActive did not finish after FIFO release")
	}
	if m.GetActive() != "a" || m.AssetSnapshot() != before {
		t.Fatal("current reload failure changed active snapshot")
	}
}
