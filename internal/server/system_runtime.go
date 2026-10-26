package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/backup"
	"github.com/HazelnutParadise/jelly-cms/internal/importer"
	"github.com/HazelnutParadise/jelly-cms/internal/notify"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
	"github.com/HazelnutParadise/jelly-cms/internal/redirects"
	"github.com/HazelnutParadise/jelly-cms/internal/safehttp"
	"github.com/HazelnutParadise/jelly-cms/internal/schedule"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/store"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/HazelnutParadise/jelly-cms/internal/update"
	"github.com/HazelnutParadise/jelly-cms/internal/version"
	"gorm.io/gorm"
)

const (
	systemWorkerLease       = 2 * time.Minute
	systemJobTimeout        = 90 * time.Second
	mailJobPurpose          = "system-job-mail-v1"
	indexNowPendingInterval = 5 * time.Second
	largeSystemJobTimeout   = 25 * time.Minute
)

var systemQueues = []string{queue.QueueMail, queue.QueueMedia, queue.QueueWebhook, queue.QueueImport, queue.QueueSchedule}

type systemJobHandler func(context.Context, *gorm.DB, queue.Job) error

type systemRuntime struct {
	db       *gorm.DB
	ctx      context.Context
	cancel   context.CancelFunc
	queue    queue.Queue
	queueSig string
	queueCfg connConfig
	ready    atomic.Bool
	started  atomic.Bool
	gate     sync.RWMutex
	inflight sync.WaitGroup
	workers  sync.WaitGroup
}

var systemRuntimeState = struct {
	sync.RWMutex
	active   *systemRuntime
	handlers map[string]systemJobHandler
}{handlers: make(map[string]systemJobHandler)}

var publishedSystemDB = struct {
	sync.Mutex
	db      *gorm.DB
	changes chan *gorm.DB
}{changes: make(chan *gorm.DB, 1)}

var systemServices = struct {
	sync.Mutex
	refs   int
	cancel context.CancelFunc
	done   chan struct{}
}{}

// RegisterSystemJobHandler registers a handler that receives the database for
// the active site. Handlers are looked up at execution time, so they cannot
// accidentally retain a previous site's database after reinstallation.
func RegisterSystemJobHandler(kind string, handler func(context.Context, *gorm.DB, queue.Job) error) {
	if kind == "" || handler == nil {
		return
	}
	systemRuntimeState.Lock()
	systemRuntimeState.handlers[kind] = handler
	systemRuntimeState.Unlock()
}

// SystemJobEnqueuer returns an enqueue function bound to a database identity.
func SystemJobEnqueuer(db *gorm.DB) func(context.Context, queue.Job) (string, error) {
	return func(ctx context.Context, job queue.Job) (string, error) {
		return EnqueueSystemJob(ctx, db, job)
	}
}

// SetSystemDatabase publishes a fully initialized site database to the
// explicit worker lifecycle. Startup and installation call this after commit.
func SetSystemDatabase(db *gorm.DB) {
	publishedSystemDB.Lock()
	publishedSystemDB.db = db
	select {
	case publishedSystemDB.changes <- db:
	default:
		select {
		case <-publishedSystemDB.changes:
		default:
		}
		select {
		case publishedSystemDB.changes <- db:
		default:
		}
	}
	publishedSystemDB.Unlock()
}

// EnqueueSystemJob places a job on the active site's configured queue. The
// database pointer is an identity boundary: a different test or site never
// shares a queue service merely because it uses the same driver settings.
func EnqueueSystemJob(ctx context.Context, db *gorm.DB, job queue.Job) (string, error) {
	if db == nil {
		return "", errors.New("system jobs: database is not ready")
	}
	systemRuntimeState.RLock()
	runtime := systemRuntimeState.active
	systemRuntimeState.RUnlock()
	if runtime == nil || runtime.db != db {
		return "", errors.New("system jobs: background services are not running for this database")
	}
	if !runtime.ready.Load() {
		return "", errors.New("system jobs: queue recovery is pending")
	}
	gateCtx, release, err := acquireSharedSystemWrite(ctx, db)
	if err != nil {
		return "", fmt.Errorf("system jobs: acquire write gate: %w", err)
	}
	defer release()
	ctx = gateCtx
	pending, journalErr := queueSwitchJournalPending(ctx, db)
	if journalErr != nil || pending {
		runtime.requireRecovery()
		return "", errors.New("system jobs: queue transfer recovery is pending")
	}
	if ctx.Value(systemHandlerRuntimeKey{}) == runtime {
		if runtime.queue == nil {
			return "", errors.New("system jobs: queue is unavailable")
		}
		return runtime.queue.Enqueue(ctx, job)
	}
	runtime.gate.RLock()
	defer runtime.gate.RUnlock()
	if !runtime.ready.Load() || runtime.queue == nil {
		return "", errors.New("system jobs: queue is unavailable")
	}
	if runtime.queueSig != queueConfig(ctx).signature() {
		runtime.requireRecovery()
		return "", errors.New("system jobs: configured queue changed; recovery is pending")
	}
	return runtime.queue.Enqueue(ctx, job)
}

func EnqueueManualStoreRefresh(ctx context.Context) (string, error) {
	systemRuntimeState.RLock()
	runtime := systemRuntimeState.active
	systemRuntimeState.RUnlock()
	if runtime == nil {
		return "", errors.New("system jobs: background services are not running")
	}
	return EnqueueSystemJob(ctx, runtime.db, queue.Job{
		Queue: queue.QueueSchedule, Kind: "store.refresh",
		DedupeKey:   fmt.Sprintf("store.refresh:manual:%d", time.Now().UTC().UnixNano()),
		MaxAttempts: queue.DefaultMaxAttempts,
	})
}

type systemHandlerRuntimeKey struct{}

func systemQueueForDB(db *gorm.DB) (queue.Queue, connConfig, bool) {
	if db == nil {
		return nil, connConfig{}, false
	}
	systemRuntimeState.RLock()
	defer systemRuntimeState.RUnlock()
	runtime := systemRuntimeState.active
	if runtime == nil || runtime.db != db || !runtime.ready.Load() {
		return nil, connConfig{}, false
	}
	runtime.gate.RLock()
	defer runtime.gate.RUnlock()
	if !runtime.ready.Load() || runtime.queue == nil {
		return nil, connConfig{}, false
	}
	return runtime.queue, runtime.queueCfg, true
}

func systemQueueRecoveryPending(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	systemRuntimeState.RLock()
	defer systemRuntimeState.RUnlock()
	runtime := systemRuntimeState.active
	return runtime != nil && runtime.db == db && !runtime.ready.Load()
}

func requestSystemQueueRecovery(db *gorm.DB) {
	systemRuntimeState.RLock()
	runtime := systemRuntimeState.active
	systemRuntimeState.RUnlock()
	if runtime != nil && runtime.db == db {
		runtime.requireRecovery()
	}
}

func switchSystemQueue(ctx context.Context, db *gorm.DB, next queue.Queue, nextCfg connConfig, switchQueue func(queue.Queue) error) (bool, error) {
	if db == nil || next == nil || switchQueue == nil {
		return false, errors.New("system queue switch is invalid")
	}
	systemRuntimeState.RLock()
	runtime := systemRuntimeState.active
	systemRuntimeState.RUnlock()
	if runtime == nil || runtime.db != db {
		return false, nil
	}
	runtime.gate.Lock()
	defer runtime.gate.Unlock()
	if !runtime.ready.Load() {
		return true, errors.New("system queue recovery is pending")
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	// Workers add to inflight while holding gate.RLock after a successful
	// reserve. Taking the writer lock therefore freezes reservations before we
	// wait for every already reserved job to finish and acknowledge.
	runtime.inflight.Wait()
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if err := switchQueue(runtime.queue); err != nil {
		journal, journalErr := loadQueueSwitchJournal(ctx, db)
		if journalErr != nil || journal != nil {
			runtime.ready.Store(false)
			runtime.workers.Add(1)
			go runtime.recoverStartup()
		}
		return true, err
	}
	runtime.queue = next
	runtime.queueSig = nextCfg.signature()
	runtime.queueCfg = nextCfg
	return true, nil
}

// StartSystemServices explicitly starts the durable queue workers and
// scheduler. RegisterRoutes intentionally does not start background work.
func StartSystemServices(parent context.Context) func() {
	systemServices.Lock()
	if systemServices.refs == 0 {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		systemServices.cancel, systemServices.done = cancel, done
		publishedSystemDB.Lock()
		initialDB := publishedSystemDB.db
		publishedSystemDB.Unlock()
		go func() {
			defer close(done)
			watchSystemDatabase(ctx, initialDB)
		}()
	}
	systemServices.refs++
	done := systemServices.done
	systemServices.Unlock()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			systemServices.Lock()
			systemServices.refs--
			last := systemServices.refs == 0
			cancel, done := systemServices.cancel, systemServices.done
			if last {
				systemServices.cancel = nil
				systemServices.done = nil
			}
			systemServices.Unlock()
			if last {
				cancel()
				<-done
			}
		})
	}
	if parent != nil {
		go func() {
			select {
			case <-parent.Done():
				stop()
			case <-done:
			}
		}()
	}
	return stop
}

func watchSystemDatabase(ctx context.Context, initial *gorm.DB) {
	var current *systemRuntime
	if initial != nil {
		current = startSystemRuntimeForDB(ctx, initial)
	}
	defer func() {
		if current != nil {
			current.stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case db := <-publishedSystemDB.changes:
			if current != nil && current.db == db {
				continue
			}
			if current != nil {
				current.stop()
				current = nil
			}
			if db != nil {
				current = startSystemRuntimeForDB(ctx, db)
			}
		}
	}
}

func startSystemRuntimeForDB(parent context.Context, db *gorm.DB) *systemRuntime {
	ctx, cancel := context.WithCancel(parent)
	service := &systemRuntime{db: db, ctx: ctx, cancel: cancel}
	systemRuntimeState.Lock()
	systemRuntimeState.active = service
	systemRuntimeState.Unlock()
	service.workers.Add(1)
	go service.recoverStartup()
	return service
}

// recoverStartup keeps the queue fail-closed until any durable transfer
// journal is readable and reconciled. A repaired journal is retried without
// ever allowing the source workers to consume a copied job first.
func (rt *systemRuntime) recoverStartup() {
	defer rt.workers.Done()
	for rt.ctx.Err() == nil {
		cfg := queueConfig(rt.ctx)
		if !queueDriverAllowed(cfg.Driver) {
			log.Printf("system jobs: refusing non-durable queue driver %q", cfg.Driver)
			if !waitSystem(rt.ctx, time.Second) {
				return
			}
			continue
		}
		if rt.queue == nil {
			q, err := queue.Open(queue.Config{Driver: cfg.Driver, URL: cfg.URL, Workers: queueWorkers(rt.ctx)})
			if err != nil {
				if rt.ctx.Err() != nil {
					return
				}
				log.Printf("system jobs: open queue: %v", safeError(err, cfg.URL))
				if !waitSystem(rt.ctx, time.Second) {
					return
				}
				continue
			}
			rt.gate.Lock()
			old := rt.queue
			rt.queue, rt.queueSig, rt.queueCfg = q, cfg.signature(), cfg
			rt.gate.Unlock()
			if old != nil {
				_ = old.Close()
			}
		}
		if err := recoverQueueSwitch(rt.ctx, rt.db, rt.queue, rt.queueCfg, 0); err != nil {
			if rt.ctx.Err() != nil {
				return
			}
			log.Printf("system jobs: recover queue switch: %v", err)
			if !waitSystem(rt.ctx, time.Second) {
				return
			}
			continue
		}
		cfg = queueConfig(rt.ctx)
		if !queueDriverAllowed(cfg.Driver) {
			continue
		}
		if cfg.signature() != rt.queueSig {
			if rt.started.Load() {
				stats, err := rt.queue.Stats(rt.ctx)
				if err != nil {
					log.Printf("system jobs: inspect previous queue before recovery: %v", err)
					if !waitSystem(rt.ctx, time.Second) {
						return
					}
					continue
				}
				pending := false
				for _, stat := range stats {
					if stat.Queued > 0 || stat.Running > 0 {
						pending = true
						break
					}
				}
				if pending {
					log.Printf("system jobs: previous queue still contains jobs; refusing an unjournaled switch")
					if !waitSystem(rt.ctx, time.Second) {
						return
					}
					continue
				}
			}
			q, err := queue.Open(queue.Config{Driver: cfg.Driver, URL: cfg.URL, Workers: queueWorkers(rt.ctx)})
			if err != nil {
				if rt.ctx.Err() != nil {
					return
				}
				log.Printf("system jobs: open recovered queue: %v", safeError(err, cfg.URL))
				if !waitSystem(rt.ctx, time.Second) {
					return
				}
				continue
			}
			rt.gate.Lock()
			old := rt.queue
			rt.queue, rt.queueSig, rt.queueCfg = q, cfg.signature(), cfg
			rt.gate.Unlock()
			if old != nil {
				_ = old.Close()
			}
		}
		rt.activate()
		return
	}
}

func (rt *systemRuntime) activate() {
	if rt.ctx.Err() != nil {
		return
	}
	rt.ready.Store(true)
	if !rt.started.CompareAndSwap(false, true) {
		return
	}
	workers := queueWorkers(rt.ctx)
	for i := 0; i < workers; i++ {
		rt.workers.Add(1)
		go rt.worker(i)
	}
	rt.workers.Add(1)
	go rt.schedule()
	rt.workers.Add(1)
	go rt.recoverQueuedBackups()
	rt.workers.Add(1)
	go rt.recoverQueueTransfer()
	rt.workers.Add(1)
	go rt.dispatchIndexNowPendingLoop()
}

func (rt *systemRuntime) dispatchIndexNowPendingLoop() {
	defer rt.workers.Done()
	for rt.ctx.Err() == nil {
		if rt.ready.Load() {
			if err := dispatchIndexNowPending(rt.ctx, rt.db, 100); err != nil && rt.ctx.Err() == nil {
				log.Printf("system jobs: dispatch pending IndexNow intents: %v", err)
			}
		}
		if !waitSystem(rt.ctx, indexNowPendingInterval) {
			return
		}
	}
}

func (rt *systemRuntime) requireRecovery() {
	if rt.ctx.Err() != nil || !rt.ready.CompareAndSwap(true, false) {
		return
	}
	rt.workers.Add(1)
	go rt.recoverStartup()
}

func (rt *systemRuntime) recoverQueueTransfer() {
	defer rt.workers.Done()
	for rt.ctx.Err() == nil {
		if !rt.ready.Load() {
			if !waitSystem(rt.ctx, time.Second) {
				return
			}
			continue
		}
		gateCtx, release, gateErr := acquireExclusiveSystemWrite(rt.ctx, rt.db)
		if gateErr == nil {
			rt.gate.Lock()
			journal, err := loadQueueSwitchJournal(gateCtx, rt.db)
			if err == nil && journal != nil && rt.queueCfg.signature() == journal.Target.signature() {
				err = recoverQueueSwitch(gateCtx, rt.db, rt.queue, rt.queueCfg, 0)
			}
			rt.gate.Unlock()
			release()
			if err != nil && rt.ctx.Err() == nil {
				log.Printf("system jobs: finalize queue switch: %v", err)
			}
		} else if rt.ctx.Err() == nil {
			log.Printf("system jobs: acquire queue recovery gate: %v", gateErr)
		}
		if !waitSystem(rt.ctx, 15*time.Second) {
			return
		}
	}
}

func (rt *systemRuntime) recoverQueuedBackups() {
	defer rt.workers.Done()
	for rt.ctx.Err() == nil {
		ids, err := backup.QueuedIDs(rt.ctx, 100)
		if err != nil && rt.ctx.Err() == nil {
			log.Printf("system jobs: recover queued backups: %v", err)
		}
		for _, id := range ids {
			payload, _ := json.Marshal(map[string]int64{"prepared_id": id})
			_, enqueueErr := EnqueueSystemJob(rt.ctx, rt.db, queue.Job{Queue: queue.QueueSchedule, Kind: "backup.run", DedupeKey: fmt.Sprintf("backup:%d", id), Payload: payload})
			if enqueueErr != nil && rt.ctx.Err() == nil {
				log.Printf("system jobs: enqueue backup %d: %v", id, enqueueErr)
			}
		}
		if !waitSystem(rt.ctx, 5*time.Second) {
			return
		}
	}
}

func (rt *systemRuntime) stop() {
	rt.ready.Store(false)
	rt.cancel()
	rt.workers.Wait()
	rt.gate.Lock()
	if rt.queue != nil {
		_ = rt.queue.Close()
	}
	rt.gate.Unlock()
	systemRuntimeState.Lock()
	if systemRuntimeState.active == rt {
		systemRuntimeState.active = nil
	}
	systemRuntimeState.Unlock()
}

func (rt *systemRuntime) worker(index int) {
	defer rt.workers.Done()
	workerID := fmt.Sprintf("jelly-%p-%d", rt.db, index)
	for rt.ctx.Err() == nil {
		if !rt.ready.Load() {
			if !waitSystem(rt.ctx, 250*time.Millisecond) {
				return
			}
			continue
		}
		if settings.Get(rt.ctx, keyQueuePaused) == "true" {
			if !waitSystem(rt.ctx, 250*time.Millisecond) {
				return
			}
			continue
		}
		gateCtx, release, gateErr := acquireSharedSystemWrite(rt.ctx, rt.db)
		if gateErr != nil {
			if rt.ctx.Err() != nil {
				return
			}
			log.Printf("system jobs: reserve gate: %v", gateErr)
			if !waitSystem(rt.ctx, time.Second) {
				return
			}
			continue
		}
		pending, journalErr := queueSwitchJournalPending(gateCtx, rt.db)
		if journalErr != nil || pending {
			rt.requireRecovery()
			release()
			if !waitSystem(rt.ctx, 250*time.Millisecond) {
				return
			}
			continue
		}
		rt.gate.RLock()
		q := rt.queue
		if !rt.ready.Load() || q == nil {
			rt.gate.RUnlock()
			release()
			if !waitSystem(rt.ctx, time.Second) {
				return
			}
			continue
		}
		if rt.queueSig != queueConfig(gateCtx).signature() {
			rt.gate.RUnlock()
			rt.requireRecovery()
			release()
			if !waitSystem(rt.ctx, 250*time.Millisecond) {
				return
			}
			continue
		}
		ctx, cancel := context.WithTimeout(gateCtx, time.Second)
		job, err := q.Reserve(ctx, systemQueues, workerID, systemWorkerLease)
		cancel()
		if err == nil && job != nil {
			rt.inflight.Add(1)
		}
		rt.gate.RUnlock()
		if err != nil {
			release()
			if rt.ctx.Err() != nil {
				return
			}
			log.Printf("system jobs: reserve: %v", err)
			if !waitSystem(rt.ctx, time.Second) {
				return
			}
			continue
		}
		if job == nil {
			release()
			if !waitSystem(rt.ctx, 250*time.Millisecond) {
				return
			}
			continue
		}
		rt.process(q, workerID, *job, gateCtx, release)
	}
}

func (rt *systemRuntime) process(q queue.Queue, workerID string, job queue.Job, gateCtx context.Context, releaseGate func()) {
	defer rt.inflight.Done()
	defer releaseGate()
	ctx, cancel := context.WithTimeout(gateCtx, systemJobTimeoutFor(job.Kind))
	ctx = context.WithValue(ctx, systemHandlerRuntimeKey{}, rt)
	finished := make(chan error, 1)
	go func() { finished <- rt.handle(ctx, job) }()
	lease, ok := q.(queue.LeaseExtender)
	var ticker *time.Ticker
	var beat <-chan time.Time
	if ok {
		ticker = time.NewTicker(systemWorkerLease / 3)
		beat = ticker.C
	}
	var err error
	lostLease := false
	handlerFinished := false
	for err == nil {
		select {
		case err = <-finished:
			handlerFinished = true
			goto complete
		case <-beat:
			extendCtx, extendCancel := context.WithTimeout(rt.ctx, 5*time.Second)
			err = lease.ExtendLease(extendCtx, job.ID, workerID, systemWorkerLease)
			extendCancel()
			if err != nil {
				lostLease = true
			}
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
complete:
	if ticker != nil {
		ticker.Stop()
	}
	cancel()
	if err != nil && !handlerFinished {
		<-finished
	}
	if rt.ctx.Err() != nil || lostLease {
		return // Leave the lease to expire; a cancelled owner never acknowledges.
	}
	if err == nil {
		if ackErr := q.Ack(rt.ctx, job.ID); ackErr != nil {
			log.Printf("system jobs: ack %s: %v", job.ID, ackErr)
			return
		}
		if shouldRestartAfterUpdateJob(job) {
			update.RequestRestart()
		}
		return
	}
	var retryAt *time.Time
	if job.Attempts < queue.DefaultMaxAttempts {
		delay := queue.RetryBackoff[min(job.Attempts-1, len(queue.RetryBackoff)-1)]
		next := time.Now().Add(delay)
		retryAt = &next
	}
	if failErr := q.Fail(rt.ctx, job.ID, err, retryAt); failErr != nil {
		log.Printf("system jobs: fail %s: %v", job.ID, failErr)
	}
}

func systemJobTimeoutFor(kind string) time.Duration {
	switch kind {
	case "backup.run", "backup.restore", "import.wxr", "import.attachments", "update.apply":
		return largeSystemJobTimeout
	default:
		return systemJobTimeout
	}
}

func (rt *systemRuntime) handle(ctx context.Context, job queue.Job) error {
	if job.Kind == "mail.send" {
		return deliverSystemMail(ctx, job)
	}
	systemRuntimeState.RLock()
	handler := systemRuntimeState.handlers[job.Kind]
	systemRuntimeState.RUnlock()
	if handler != nil {
		return handler(ctx, rt.db, job)
	}
	if handler, ok := queue.HandlerFor(job.Kind); ok {
		return handler(ctx, job)
	}
	return fmt.Errorf("no handler registered for job kind %q", job.Kind)
}

func (rt *systemRuntime) schedule() {
	defer rt.workers.Done()
	// This scheduler uses the PostgreSQL advisory lock in schedule.Run. All
	// schedules target the same configured queue used by explicit enqueues.
	// Register jobs as handlers become available; unknown kinds fail visibly.
	jobs := []schedule.Schedule{
		{Name: "backup.run", Spec: "* * * * *", Queue: queue.QueueSchedule, Kind: "backup.run"},
		{Name: "backup.prune", Spec: "17 3 * * *", Queue: queue.QueueSchedule, Kind: "backup.prune"},
		{Name: "logs.prune", Spec: "29 3 * * *", Queue: queue.QueueSchedule, Kind: "logs.prune"},
		{Name: "trash.purge", Spec: "41 3 * * *", Queue: queue.QueueSchedule, Kind: "trash.purge"},
		{Name: "redirects.flush_hits", Spec: "* * * * *", Queue: queue.QueueSchedule, Kind: "redirects.flush_hits"},
		{Name: "update.check", Spec: "* * * * *", Queue: queue.QueueSchedule, Kind: "update.check"},
		{Name: "store.refresh", Spec: "13 4 * * *", Queue: queue.QueueSchedule, Kind: "store.refresh"},
		{Name: "notifications.dispatch", Spec: "* * * * *", Queue: queue.QueueSchedule, Kind: "notifications.dispatch"},
		{Name: "notifications.prune", Spec: "23 3 * * *", Queue: queue.QueueSchedule, Kind: "notifications.prune"},
		{Name: "redirects.check_targets", Spec: "37 4 * * *", Queue: queue.QueueSchedule, Kind: "redirects.check_targets"},
	}
	if err := schedule.New(systemQueueAdapter{runtime: rt}, jobs...).RunWithDB(rt.ctx, rt.db); err != nil && rt.ctx.Err() == nil {
		log.Printf("system schedule: %v", err)
	}
}

// systemQueueAdapter gives the scheduler a stable Queue while enqueueing
// through the runtime's active queue after a backend switch.
type systemQueueAdapter struct{ runtime *systemRuntime }

func (a systemQueueAdapter) Enqueue(ctx context.Context, job queue.Job) (string, error) {
	return EnqueueSystemJob(ctx, a.runtime.db, job)
}
func (a systemQueueAdapter) Reserve(ctx context.Context, names []string, worker string, lease time.Duration) (*queue.Job, error) {
	return a.runtime.queue.Reserve(ctx, names, worker, lease)
}
func (a systemQueueAdapter) Ack(ctx context.Context, id string) error {
	return a.runtime.queue.Ack(ctx, id)
}
func (a systemQueueAdapter) Fail(ctx context.Context, id string, cause error, retryAt *time.Time) error {
	return a.runtime.queue.Fail(ctx, id, cause, retryAt)
}
func (a systemQueueAdapter) Retry(ctx context.Context, id string) error {
	return a.runtime.queue.Retry(ctx, id)
}
func (a systemQueueAdapter) Stats(ctx context.Context) ([]queue.QueueStats, error) {
	return a.runtime.queue.Stats(ctx)
}
func (a systemQueueAdapter) Drain(ctx context.Context, limit int) ([]queue.Job, error) {
	return a.runtime.queue.Drain(ctx, limit)
}
func (a systemQueueAdapter) Ping(ctx context.Context) (time.Duration, error) {
	return a.runtime.queue.Ping(ctx)
}
func (a systemQueueAdapter) Close() error { return nil }

func init() {
	RegisterSystemJobHandler("backup.run", func(ctx context.Context, db *gorm.DB, job queue.Job) error {
		if db == nil {
			return errors.New("scheduled backup database is not available")
		}
		var payload struct {
			PreparedID int64 `json:"prepared_id"`
		}
		if len(job.Payload) > 0 {
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return errors.New("backup job payload is invalid")
			}
		}
		if payload.PreparedID > 0 {
			_, err := backup.RunPrepared(ctx, payload.PreparedID)
			return err
		}
		dueAt := job.RunAt
		if dueAt.IsZero() {
			dueAt = time.Now()
		}
		id, err := backup.PrepareScheduledIfDue(ctx, dueAt)
		if err != nil || id == 0 {
			return err
		}
		encoded, _ := json.Marshal(map[string]int64{"prepared_id": id})
		_, err = EnqueueSystemJob(ctx, db, queue.Job{Queue: queue.QueueSchedule, Kind: "backup.run", DedupeKey: fmt.Sprintf("backup:%d", id), Payload: encoded})
		return err
	})
	RegisterSystemJobHandler("backup.prune", func(ctx context.Context, db *gorm.DB, _ queue.Job) error {
		if db == nil {
			return errors.New("backup retention database is not available")
		}
		return backup.Prune(ctx)
	})
	RegisterSystemJobHandler("logs.prune", func(ctx context.Context, db *gorm.DB, _ queue.Job) error {
		_, err := PruneSystemLogs(ctx, db, time.Now().UTC())
		return err
	})
	RegisterSystemJobHandler("trash.purge", func(ctx context.Context, db *gorm.DB, _ queue.Job) error {
		if db == nil {
			return errors.New("trash purge database is not available")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := PurgeExpiredTrash(db, time.Now())
		return err
	})
	RegisterSystemJobHandler("redirects.flush_hits", func(ctx context.Context, db *gorm.DB, _ queue.Job) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if db == nil {
			return errors.New("redirect hit database is not available")
		}
		return redirects.FlushHits()
	})
	RegisterSystemJobHandler("update.check", func(ctx context.Context, db *gorm.DB, job queue.Job) error {
		if db == nil {
			return errors.New("update check database is not available")
		}
		var payload struct {
			Manual  bool   `json:"manual"`
			Channel string `json:"channel"`
		}
		if len(job.Payload) > 0 {
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return errors.New("update check job payload is invalid")
			}
		}
		dueAt := job.RunAt
		if dueAt.IsZero() {
			dueAt = time.Now()
		}
		channel := payload.Channel
		if !payload.Manual {
			if settings.Get(ctx, "update_auto_check") != "true" {
				return nil
			}
			if dueAt.In(siteLocation(ctx)).Format("15:04") != settings.Get(ctx, "update_auto_time") {
				return nil
			}
			channel = settings.Get(ctx, "update_channel")
		} else if channel == "" {
			channel = settings.Get(ctx, "update_channel")
		}
		rel, err := update.Check(ctx, channel)
		if errors.Is(err, update.ErrNoRelease) {
			return nil
		}
		if err != nil {
			return err
		}
		if settings.Get(ctx, "update_on_new") == "notify" && update.IsNewer(rel.Version, version.Version) {
			return notify.Emit(ctx, db, notify.Event{Kind: notify.KindSystemUpdateAvailable, Category: notify.CategorySystem,
				Params: map[string]any{"version": rel.Version, "channel": rel.Channel}, GroupKey: "system.update_available:" + rel.Version,
				Recipients: notify.Recipients{Roles: []string{"admin"}}})
		}
		return nil
	})
	RegisterSystemJobHandler("update.apply", func(ctx context.Context, _ *gorm.DB, job queue.Job) error {
		var payload struct {
			Version  string `json:"version"`
			Expected string `json:"expected_version"`
			Actor    uint   `json:"actor"`
		}
		if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.Version == "" || payload.Expected == "" {
			return errors.New("update apply job payload is invalid")
		}
		if version.Version != payload.Expected || version.Version == payload.Version {
			return nil
		}
		ctx = withUpdatePreBackup(ctx, payload.Actor, job.ID, payload.Version)
		if err := update.Apply(ctx, payload.Version); err != nil {
			return err
		}
		syslog.Audit(ctx, payload.Actor, "update.apply", map[string]any{"version": payload.Version, "state": "applied"})
		return nil
	})
	RegisterSystemJobHandler("update.rollback", func(ctx context.Context, _ *gorm.DB, job queue.Job) error {
		var payload struct {
			Expected string `json:"expected_version"`
			Actor    uint   `json:"actor"`
		}
		if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.Expected == "" {
			return errors.New("update rollback job payload is invalid")
		}
		if version.Version != payload.Expected {
			return nil
		}
		if err := update.Rollback(ctx); err != nil {
			return err
		}
		syslog.Audit(ctx, payload.Actor, "update.rollback", nil)
		return nil
	})
	RegisterSystemJobHandler("store.refresh", func(ctx context.Context, db *gorm.DB, _ queue.Job) error {
		if db == nil {
			return errors.New("store catalog database is not available")
		}
		source := store.CatalogURL()
		label := storeSourceEmbedded
		if source != "" {
			label = storeSourceRemote
		}
		items, err := loadStoreCatalog(ctx, source)
		if err != nil {
			return err
		}
		if err := writeStoreCatalog(ctx, items, label, false); err != nil {
			return err
		}
		return nil
	})
	RegisterSystemJobHandler("import.wxr", func(ctx context.Context, db *gorm.DB, job queue.Job) error {
		if db == nil {
			return errors.New("WXR import database is not available")
		}
		var payload struct {
			ID    int64 `json:"id"`
			Actor uint  `json:"actor"`
		}
		if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.ID <= 0 {
			return errors.New("WXR import job payload is invalid")
		}
		_, _, err := importer.Run(ctx, db.WithContext(ctx), payload.ID, importHostForDB(ctx, payload.Actor, db))
		return err
	})
	RegisterSystemJobHandler("notifications.dispatch", func(ctx context.Context, db *gorm.DB, _ queue.Job) error {
		ids, err := notify.DueDeliveryIDs(ctx, db, 100)
		if err != nil {
			return err
		}
		for _, id := range ids {
			var row struct {
				Attempts      int
				NextAttemptAt time.Time
			}
			if err := db.WithContext(ctx).Table("notification_deliveries").Select("attempts, next_attempt_at").Where("id = ?", id).Take(&row).Error; err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]uint{"id": id})
			dedupe := fmt.Sprintf("notification:%d:%d:%d", id, row.Attempts, row.NextAttemptAt.Unix())
			if _, err := EnqueueSystemJob(ctx, db, queue.Job{Queue: queue.QueueWebhook, Kind: "notifications.deliver", DedupeKey: dedupe, Payload: payload}); err != nil {
				return err
			}
		}
		return nil
	})
	RegisterSystemJobHandler("notifications.deliver", func(ctx context.Context, db *gorm.DB, job queue.Job) error {
		var payload struct {
			ID uint `json:"id"`
		}
		if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.ID == 0 {
			return errors.New("notification delivery payload is invalid")
		}
		return notify.ProcessDelivery(ctx, db, payload.ID, sendSystemNotification)
	})
	RegisterSystemJobHandler("notifications.prune", func(ctx context.Context, db *gorm.DB, _ queue.Job) error {
		return notify.Prune(ctx, db, time.Now().UTC())
	})
	RegisterSystemJobHandler("redirects.check_targets", checkRedirectTargets)
}

func shouldRestartAfterUpdateJob(job queue.Job) bool {
	var payload struct {
		Expected string `json:"expected_version"`
	}
	if json.Unmarshal(job.Payload, &payload) != nil || payload.Expected != version.Version {
		return false
	}
	return job.Kind == "update.apply" || job.Kind == "update.rollback"
}

func sendSystemNotification(ctx context.Context, d notify.Delivery) error {
	switch d.Channel {
	case "email":
		params, err := json.Marshal(d.Params)
		if err != nil {
			return errors.New("notification parameters are invalid")
		}
		payload, _ := json.Marshal(systemMailPayload{To: d.Target, Kind: d.Kind, Subject: "Jelly CMS: " + d.Kind, Body: string(params)})
		return deliverSystemMail(ctx, queue.Job{Payload: payload})
	case "webhook":
		if d.Target == "" {
			return errors.New("notification webhook is not configured")
		}
		target, err := url.Parse(d.Target)
		if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil {
			return errors.New("notification webhook URL is invalid")
		}
		body, err := json.Marshal(map[string]any{"kind": d.Kind, "notification_id": d.NotificationID, "params": d.Params})
		if err != nil {
			return errors.New("notification webhook payload is invalid")
		}
		if len(body) > 1<<20 {
			return errors.New("notification webhook payload is too large")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Target, strings.NewReader(string(body)))
		if err != nil {
			return errors.New("notification webhook URL is invalid")
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := safehttp.Client(safehttp.Options{Timeout: 10 * time.Second, MaxBytes: 32 * 1024}).Do(req)
		if err != nil {
			return errors.New("notification webhook request failed")
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32*1024))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("notification webhook returned HTTP %d", resp.StatusCode)
		}
		return nil
	default:
		return errors.New("notification channel is invalid")
	}
}

func checkRedirectTargets(ctx context.Context, db *gorm.DB, _ queue.Job) error {
	if db == nil {
		return errors.New("redirect target database is not available")
	}
	now := time.Now().UTC()
	var rows []redirects.Redirect
	if err := db.WithContext(ctx).Where("(expires_at IS NULL OR expires_at > ?) AND (checked_at IS NULL OR checked_at < ?)", now, now.Add(-24*time.Hour)).
		Order("checked_at ASC NULLS FIRST, id ASC").Limit(10).Find(&rows).Error; err != nil {
		return err
	}
	client := safehttp.Client(safehttp.Options{Timeout: 8 * time.Second, MaxBytes: 32 * 1024})
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		var status *int
		parsed, parseErr := url.Parse(row.ToURL)
		if parseErr == nil && parsed.IsAbs() && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" {
			req, err := http.NewRequestWithContext(ctx, http.MethodHead, row.ToURL, nil)
			if err == nil {
				resp, requestErr := client.Do(req)
				if requestErr == nil {
					code := resp.StatusCode
					status = &code
					_ = resp.Body.Close()
				} else {
					code := 0
					status = &code
				}
			}
		}
		if err := db.WithContext(ctx).Model(&redirects.Redirect{}).Where("id = ?", row.ID).Updates(map[string]any{"target_status": status, "checked_at": now}).Error; err != nil {
			return err
		}
	}
	return nil
}

func siteLocation(ctx context.Context) *time.Location {
	name := strings.TrimSpace(settings.Get(ctx, "site_timezone"))
	if name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return time.UTC
}

func waitSystem(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type systemMailPayload struct {
	To      string `json:"to"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Sealed  []byte `json:"sealed,omitempty"`
}

func deliverSystemMail(ctx context.Context, job queue.Job) error {
	var payload systemMailPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return errors.New("mail job payload is invalid")
	}
	to, err := mail.ParseAddress(payload.To)
	if err != nil || to.Address != payload.To {
		return errors.New("mail job recipient is invalid")
	}
	if payload.Sealed != nil {
		plain, err := settings.OpenBytes(mailJobPurpose, payload.Sealed)
		if err != nil {
			return errors.New("mail job secret payload cannot be opened")
		}
		var secret struct {
			Password string `json:"password"`
		}
		if err := json.Unmarshal(plain, &secret); err != nil {
			return errors.New("mail job secret payload is invalid")
		}
		payload.Body += secret.Password
	}
	if settings.Get(ctx, "mail_enabled") != "true" {
		return errors.New("mail delivery is disabled")
	}
	host := strings.TrimSpace(resolveMailEnv(ctx, "mail_host", "JELLY_MAIL_HOST"))
	port, err := strconv.Atoi(strings.TrimSpace(resolveMailEnv(ctx, "mail_port", "JELLY_MAIL_PORT")))
	if err != nil || port < 1 || port > 65535 || host == "" || strings.ContainsAny(host, "\r\n/ ") {
		return errors.New("mail server settings are incomplete")
	}
	from, err := mail.ParseAddress(strings.TrimSpace(resolveMailEnv(ctx, "mail_from", "JELLY_MAIL_FROM")))
	if err != nil || from.Address == "" {
		return errors.New("mail sender address is invalid")
	}
	user := resolveMailEnv(ctx, "mail_user", "JELLY_MAIL_USER")
	password := resolveMailEnv(ctx, "mail_password_enc", "JELLY_MAIL_PASSWORD")
	mode := strings.ToLower(strings.TrimSpace(resolveMailEnv(ctx, "mail_tls", "JELLY_MAIL_TLS")))
	if mode == "" {
		mode = "starttls"
	}
	if mode != "none" && mode != "starttls" && mode != "tls" {
		return errors.New("mail TLS mode is invalid")
	}
	if user != "" && mode == "none" {
		return errors.New("SMTP authentication requires TLS")
	}
	if payload.Subject == "" {
		payload.Subject = "Jelly CMS mail test"
	}
	if strings.ContainsAny(payload.Subject, "\r\n") || len(payload.Subject) > 500 || len(payload.Body) > 1024*1024 {
		return errors.New("mail message headers or body are invalid")
	}
	if payload.Body == "" {
		payload.Body = "This is a test message from Jelly CMS.\nRecipient: " + to.Address + "\n"
	}
	return sendSystemSMTP(ctx, host, port, mode, user, password, from, to, payload.Subject, payload.Body)
}

func resolveMailEnv(ctx context.Context, key, envName string) string {
	value := strings.TrimSpace(settings.Get(ctx, key))
	if envName != "" {
		if env := strings.TrimSpace(os.Getenv(envName)); env != "" {
			value = env
		}
	}
	if strings.HasPrefix(value, "env:") {
		return strings.TrimSpace(os.Getenv(strings.TrimPrefix(value, "env:")))
	}
	return value
}

func sendSystemSMTP(ctx context.Context, host string, port int, mode, user, password string, from, to *mail.Address, subject, body string) error {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return errors.New("SMTP connection failed")
	}
	defer conn.Close()
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	closed := make(chan struct{})
	baseConn := conn
	go func() {
		select {
		case <-ctx.Done():
			_ = baseConn.Close()
		case <-closed:
		}
	}()
	defer close(closed)
	if mode == "tls" {
		tlsConn := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return errors.New("SMTP TLS handshake failed")
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return errors.New("SMTP protocol negotiation failed")
	}
	defer client.Close()
	if mode == "starttls" {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return errors.New("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}); err != nil {
			return errors.New("SMTP STARTTLS failed")
		}
	}
	if user != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("SMTP server does not support authentication")
		}
		if err := client.Auth(smtp.PlainAuth("", user, password, host)); err != nil {
			return errors.New("SMTP authentication failed")
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return errors.New("SMTP sender was rejected")
	}
	if err := client.Rcpt(to.Address); err != nil {
		return errors.New("SMTP recipient was rejected")
	}
	w, err := client.Data()
	if err != nil {
		return errors.New("SMTP message was rejected")
	}
	_, writeErr := fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", from.String(), to.String(), subject, strings.ReplaceAll(body, "\n", "\r\n"))
	closeErr := w.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("SMTP message delivery failed")
	}
	if err := client.Quit(); err != nil {
		return errors.New("SMTP server did not confirm delivery")
	}
	return nil
}
