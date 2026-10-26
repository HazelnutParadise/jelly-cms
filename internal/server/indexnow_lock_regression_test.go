package server

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/settings"
)

func TestIndexNowLockRegressionPendingDispatchDoesNotInvertSystemWriteGate(t *testing.T) {
	t.Setenv("APP_URL", "https://indexnow-lock.example.test")
	h := newAccessHarness(t)
	admin := h.user("admin", "indexnow-lock-regression-admin")
	indexNowSetEnabled(t, h, admin.ID)
	if systemRuntimeState.active != nil {
		t.Fatal("test requires an isolated runtime to start after the pending intent is created")
	}
	response, postID := indexNowPublishPost(t, h, h.token(admin), "IndexNow Lock Post", "indexnow-lock-post", 0)
	if response.code != http.StatusOK || postID == 0 {
		t.Fatalf("publish IndexNow post = %d id=%d: %s", response.code, postID, response.body)
	}
	if _, ok := indexNowPendingOption(t, h, "post", postID); !ok {
		t.Fatal("public post did not leave its durable IndexNow pending intent before runtime startup")
	}
	if err := settings.Set(context.Background(), admin.ID, map[string]string{"queue_paused": "true"}); err != nil {
		t.Fatalf("pause system workers: %v", err)
	}

	stateValue, _ := indexNowPendingDispatchStates.LoadOrStore(h.db, &indexNowPendingDispatchState{})
	state := stateValue.(*indexNowPendingDispatchState)
	state.Lock()
	manualStateLockHeld := true
	var stop func()
	var foregroundRelease func()
	var foregroundCancel context.CancelFunc
	var foregroundDone chan error
	foregroundResultRead := false
	exclusiveCancel := func() {}
	exclusiveDone := make(chan struct{})
	exclusiveRelease := make(chan func(), 1)
	var exclusiveResultRead bool
	servicesStarted := false
	t.Cleanup(func() {
		if manualStateLockHeld {
			state.Unlock()
			manualStateLockHeld = false
		}
		if foregroundCancel != nil {
			foregroundCancel()
		}
		if foregroundRelease != nil {
			foregroundRelease()
			foregroundRelease = nil
		}
		exclusiveCancel()
		if !exclusiveResultRead {
			select {
			case release := <-exclusiveRelease:
				if release != nil {
					release()
				}
				exclusiveResultRead = true
			case <-exclusiveDone:
			case <-time.After(3 * time.Second):
				t.Error("exclusive gate waiter did not stop during cleanup")
			}
		}
		if servicesStarted {
			stop()
			SetSystemDatabase(nil)
		}
		if foregroundDone != nil && !foregroundResultRead {
			select {
			case <-foregroundDone:
			case <-time.After(3 * time.Second):
				t.Error("foreground IndexNow dispatch goroutine did not stop during cleanup")
			}
		}
	})

	SetSystemDatabase(h.db)
	stop = StartSystemServices(context.Background())
	servicesStarted = true
	readyDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(readyDeadline) {
		systemRuntimeState.RLock()
		runtime := systemRuntimeState.active
		ready := runtime != nil && runtime.db == h.db && runtime.ready.Load()
		systemRuntimeState.RUnlock()
		if ready {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	systemRuntimeState.RLock()
	runtime := systemRuntimeState.active
	ready := runtime != nil && runtime.db == h.db && runtime.ready.Load()
	systemRuntimeState.RUnlock()
	if !ready {
		t.Fatal("paused system queue runtime did not become ready")
	}

	foregroundCtx, cancelForeground := context.WithTimeout(context.Background(), 2*time.Second)
	foregroundGateCtx, release, err := acquireSharedSystemWrite(foregroundCtx, h.db)
	if err != nil {
		cancelForeground()
		t.Fatalf("foreground acquire shared system write gate: %v", err)
	}
	foregroundRelease = release
	foregroundCancel = cancelForeground

	exclusiveCtx, cancelExclusive := context.WithTimeout(context.Background(), 4*time.Second)
	exclusiveCancel = cancelExclusive
	go func() {
		_, unlock, err := acquireExclusiveSystemWrite(exclusiveCtx, h.db)
		if err != nil {
			close(exclusiveDone)
			return
		}
		exclusiveRelease <- unlock
		close(exclusiveDone)
	}()
	if !waitForRuntimeCoordinationActivity(t, h.db, 2*time.Second, `state = 'active' AND wait_event_type = 'Lock' AND query ILIKE '%pg_advisory_lock($1)%' AND query NOT ILIKE '%shared%'`) {
		t.Fatal("exclusive system write gate did not queue behind the foreground shared lease")
	}

	// The runtime's real background dispatcher was held at its per-database
	// state mutex until the foreground shared gate and queued exclusive waiter
	// were both established. It now must drop that mutex before it waits for the
	// PostgreSQL shared gate in EnqueueSystemJob.
	state.Unlock()
	manualStateLockHeld = false
	if !waitForRuntimeCoordinationActivity(t, h.db, 2*time.Second, `state = 'active' AND wait_event_type = 'Lock' AND query ILIKE '%pg_advisory_lock_shared%'`) {
		t.Fatal("background pending dispatcher did not block on the shared PostgreSQL system gate")
	}

	foregroundDone = make(chan error, 1)
	go func() {
		foregroundDone <- dispatchIndexNowPending(foregroundGateCtx, h.db, 10)
	}()
	select {
	case err := <-foregroundDone:
		foregroundResultRead = true
		if err != nil {
			t.Fatalf("foreground pending dispatch with existing gate lease: %v", err)
		}
	case <-time.After(750 * time.Millisecond):
		t.Errorf("foreground dispatch waited on the background state mutex while the background waited for PostgreSQL; lock-order inversion reproduced")
	}

	// Release each waiter and join it before the isolated database is closed.
	if foregroundRelease != nil {
		foregroundRelease()
		foregroundRelease = nil
	}
	if !exclusiveResultRead {
		select {
		case unlock := <-exclusiveRelease:
			exclusiveResultRead = true
			unlock()
		case <-time.After(2 * time.Second):
			exclusiveCancel()
			t.Error("exclusive system write gate did not finish after the foreground released its shared lease")
		}
	}
	if foregroundCancel != nil {
		foregroundCancel()
	}
	select {
	case <-exclusiveDone:
	case <-time.After(2 * time.Second):
		t.Error("exclusive system write gate goroutine did not stop")
	}
	if foregroundDone != nil && !foregroundResultRead {
		select {
		case <-foregroundDone:
			foregroundResultRead = true
		case <-time.After(2 * time.Second):
			t.Error("foreground dispatch goroutine did not stop after cancellation")
		}
	}

	// The real queue is paused, so this test never submits the public URL to
	// IndexNow. It only verifies the durable job generated from the public post.
	var jobs int64
	if err := h.db.Model(&struct{}{}).Table("jobs").Where("kind = ? AND payload->'item'->>'id' = ?", indexNowJobKind, fmt.Sprint(postID)).Count(&jobs).Error; err != nil {
		t.Fatalf("count paused IndexNow job for public post %d: %v", postID, err)
	}
	if jobs != 1 {
		t.Errorf("paused IndexNow jobs for public post %d = %d, want one", postID, jobs)
	}
}
