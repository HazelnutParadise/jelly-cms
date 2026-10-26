package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const queueSwitchJournalKey = "system_queue_switch_journal"

type queueSwitchJournal struct {
	Version int         `json:"version"`
	Stage   string      `json:"stage"`
	Source  connConfig  `json:"source"`
	Target  connConfig  `json:"target"`
	Jobs    []queue.Job `json:"jobs"`
}

// durableQueueTransfer journals the complete snapshot before writing to the
// target. Settings are switched only after every target job is confirmed.
func durableQueueTransfer(ctx context.Context, db *gorm.DB, from, to queue.Queue, source, target connConfig, actor uint, cutover func() error) error {
	if db == nil || from == nil || to == nil || cutover == nil {
		return errors.New("queue transfer: invalid input")
	}
	src, ok := from.(queue.TransferSource)
	if !ok {
		return errors.New("queue transfer: source does not support durable snapshots")
	}
	pending, err := loadQueueSwitchJournal(ctx, db)
	if err != nil {
		return err
	}
	if pending != nil {
		return errors.New("queue transfer: a previous cutover still needs recovery")
	}
	jobs, err := src.SnapshotQueued(ctx, 0)
	if err != nil {
		return fmt.Errorf("queue transfer: snapshot: %w", err)
	}
	journal := queueSwitchJournal{Version: 1, Stage: "prepared", Source: source, Target: target, Jobs: jobs}
	if err := saveQueueSwitchJournal(ctx, db, journal); err != nil {
		return err
	}
	if err := enqueueSnapshot(ctx, to, jobs); err != nil {
		return err
	}
	journal.Stage = "target_ready"
	if err := saveQueueSwitchJournal(ctx, db, journal); err != nil {
		return err
	}
	if err := cutover(); err != nil {
		if current := queueConfig(ctx); current.signature() == target.signature() {
			log.Printf("queue transfer: cutover settings may have committed; source cleanup will be recovered")
			return nil
		}
		return fmt.Errorf("queue transfer: save cutover settings: %w", err)
	}
	if err := confirmQueueSource(ctx, src, jobs); err != nil {
		log.Printf("queue transfer: cutover completed; source confirmation will be recovered: %v", err)
		return nil
	}
	return deleteQueueSwitchJournal(ctx, db)
}

func enqueueSnapshot(ctx context.Context, to queue.Queue, jobs []queue.Job) error {
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		id, err := to.Enqueue(ctx, job)
		if err != nil {
			return fmt.Errorf("queue transfer: target enqueue %s: %w", job.ID, err)
		}
		if id != job.ID {
			return fmt.Errorf("queue transfer: target returned different ID for %s", job.ID)
		}
	}
	return nil
}

func confirmQueueSource(ctx context.Context, src queue.TransferSource, jobs []queue.Job) error {
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.ID)
	}
	return src.ConfirmQueued(ctx, ids)
}

func saveQueueSwitchJournal(ctx context.Context, db *gorm.DB, journal queueSwitchJournal) error {
	plain, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	sealed, err := settings.SealBytes(queueSwitchJournalKey, plain)
	if err != nil {
		return fmt.Errorf("queue transfer: encrypt journal: %w", err)
	}
	row := core.Option{Key: queueSwitchJournalKey, Value: base64.StdEncoding.EncodeToString(sealed)}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&row).Error; err != nil {
		return fmt.Errorf("queue transfer: save journal: %w", err)
	}
	return nil
}

func loadQueueSwitchJournal(ctx context.Context, db *gorm.DB) (*queueSwitchJournal, error) {
	var row core.Option
	err := db.WithContext(ctx).Where("key = ?", queueSwitchJournalKey).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sealed, err := base64.StdEncoding.DecodeString(row.Value)
	if err != nil {
		return nil, errors.New("queue transfer: journal encoding is invalid")
	}
	plain, err := settings.OpenBytes(queueSwitchJournalKey, sealed)
	if err != nil {
		return nil, fmt.Errorf("queue transfer: decrypt journal: %w", err)
	}
	var journal queueSwitchJournal
	if err := json.Unmarshal(plain, &journal); err != nil || journal.Version != 1 || len(journal.Stage) == 0 {
		return nil, errors.New("queue transfer: journal is invalid")
	}
	return &journal, nil
}

func queueSwitchJournalPending(ctx context.Context, db *gorm.DB) (bool, error) {
	journal, err := loadQueueSwitchJournal(ctx, db)
	return journal != nil, err
}

func deleteQueueSwitchJournal(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Where("key = ?", queueSwitchJournalKey).Delete(&core.Option{}).Error
}

// recoverQueueSwitch completes an interrupted transfer before workers start.
func recoverQueueSwitch(ctx context.Context, db *gorm.DB, active queue.Queue, activeCfg connConfig, actor uint) error {
	gateCtx, release, err := acquireExclusiveSystemWrite(ctx, db)
	if err != nil {
		return err
	}
	defer release()
	ctx = gateCtx
	journal, err := loadQueueSwitchJournal(ctx, db)
	if err != nil || journal == nil {
		return err
	}
	if !queueDriverAllowed(journal.Source.Driver) || !queueDriverAllowed(journal.Target.Driver) {
		return errors.New("queue transfer: journal references a queue driver unavailable in production")
	}
	if activeCfg.signature() == journal.Target.signature() {
		from, err := queue.Open(queue.Config{Driver: journal.Source.Driver, URL: journal.Source.URL})
		if err != nil {
			return fmt.Errorf("queue transfer: reopen source: %w", err)
		}
		defer from.Close()
		src, ok := from.(queue.TransferSource)
		if !ok {
			return errors.New("queue transfer: recovery source lacks snapshot support")
		}
		if err := confirmQueueSource(ctx, src, journal.Jobs); err != nil {
			return err
		}
		return deleteQueueSwitchJournal(ctx, db)
	}
	if activeCfg.signature() != journal.Source.signature() {
		return errors.New("queue transfer: active backend differs from journal source and target")
	}
	to, err := queue.Open(queue.Config{Driver: journal.Target.Driver, URL: journal.Target.URL})
	if err != nil {
		return fmt.Errorf("queue transfer: reopen target: %w", err)
	}
	defer to.Close()
	if err := enqueueSnapshot(ctx, to, journal.Jobs); err != nil {
		return err
	}
	journal.Stage = "target_ready"
	if err := saveQueueSwitchJournal(ctx, db, *journal); err != nil {
		return err
	}
	values := map[string]string{keyQueueDriver: journal.Target.Driver}
	if journal.Target.Source == "request" {
		values[keyQueueURL] = journal.Target.URL
	}
	if err := settings.Set(ctx, actor, values); err != nil {
		return err
	}
	src, ok := active.(queue.TransferSource)
	if !ok {
		return errors.New("queue transfer: recovery source lacks snapshot support")
	}
	if err := confirmQueueSource(ctx, src, journal.Jobs); err != nil {
		return err
	}
	return deleteQueueSwitchJournal(ctx, db)
}
