package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/google/uuid"
)

type beforeUpdateContextKey struct{}

type beforeUpdateMetadata struct {
	JobID  string `json:"job_id"`
	Target string `json:"target"`
}

const preUpdateLockClass = 731984206

func withBeforeUpdateMetadata(ctx context.Context, metadata beforeUpdateMetadata) context.Context {
	return context.WithValue(ctx, beforeUpdateContextKey{}, metadata)
}

func beforeUpdateMetadataFromContext(ctx context.Context) (beforeUpdateMetadata, bool) {
	metadata, ok := ctx.Value(beforeUpdateContextKey{}).(beforeUpdateMetadata)
	return metadata, ok
}

// RunBeforeUpdate creates or reuses a verified local database-only snapshot
// for one update job and target version.
func RunBeforeUpdate(ctx context.Context, actor uint, jobID, target string) (int64, error) {
	if ctx == nil {
		return 0, errors.New("backup: pre-update context is required")
	}
	if data.DB == nil {
		return 0, ErrNotInstalled
	}
	if actor == 0 || jobID == "" || target == "" {
		return 0, errors.New("backup: pre-update actor, job ID and target are required")
	}
	if _, err := uuid.Parse(jobID); err != nil {
		return 0, errors.New("backup: pre-update job ID must be a UUID")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !runMu.TryLock() {
		return 0, ErrBusy
	}
	defer runMu.Unlock()
	lockConn, unlock, err := acquirePreUpdateLock(ctx, jobID, target)
	if err != nil {
		return 0, err
	}
	defer unlock(lockConn)

	var rows []record
	if err := data.DB.WithContext(ctx).
		Where("kind = ? AND manifest->'_jelly_update'->>'job_id' = ? AND manifest->'_jelly_update'->>'target' = ?", KindManual, jobID, target).
		Order("id ASC").Limit(2).Find(&rows).Error; err != nil {
		return 0, fmt.Errorf("backup: find pre-update backup: %w", err)
	}
	if len(rows) > 1 {
		return 0, errors.New("backup: duplicate pre-update backups exist for this job and target")
	}
	if len(rows) == 1 {
		return reuseOrRetryPreUpdate(ctx, rows[0], actor, jobID, target)
	}

	metadata := beforeUpdateMetadata{JobID: jobID, Target: target}
	ctx = withBeforeUpdateMetadata(ctx, metadata)
	return runLocked(ctx, Request{Contents: []string{ContentDatabase}, Kind: KindManual, Actor: actor})
}

func acquirePreUpdateLock(ctx context.Context, jobID, target string) (*sql.Conn, func(*sql.Conn), error) {
	sqlDB, err := data.DB.DB()
	if err != nil {
		return nil, nil, fmt.Errorf("backup: open pre-update lock connection: %w", err)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("backup: acquire pre-update lock connection: %w", err)
	}
	digest := sha256.Sum256([]byte(jobID + "\x00" + target))
	key := int32(binary.BigEndian.Uint32(digest[:4]))
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1, $2)", preUpdateLockClass, key).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("backup: acquire pre-update lock: %w", err)
	}
	if !acquired {
		_ = conn.Close()
		return nil, nil, ErrBusy
	}
	unlock := func(conn *sql.Conn) {
		if conn == nil {
			return
		}
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released bool
		if err := conn.QueryRowContext(unlockCtx, "SELECT pg_advisory_unlock($1, $2)", preUpdateLockClass, key).Scan(&released); err != nil || !released {
			// Returning this connection while it holds the session lock would
			// block later attempts after the work itself has finished.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}
	return conn, unlock, nil
}

func reuseOrRetryPreUpdate(ctx context.Context, row record, actor uint, jobID, target string) (int64, error) {
	if row.CreatedBy == nil || *row.CreatedBy != actor {
		return 0, errors.New("backup: pre-update backup actor does not match")
	}
	manifest := decodeDatabaseManifest(row.Manifest)
	if manifest.malformed {
		return 0, ErrInvalidArchive
	}
	var metadata beforeUpdateMetadata
	if err := json.Unmarshal(manifest.fields["_jelly_update"], &metadata); err != nil || metadata.JobID != jobID || metadata.Target != target {
		return 0, errors.New("backup: pre-update backup metadata does not match")
	}
	switch row.State {
	case stateDone:
		verified, err := Verify(ctx, row.ID, "")
		if err != nil {
			return 0, fmt.Errorf("backup: refusing unverified pre-update backup: %w", err)
		}
		if !slices.Equal(verified.Contents, []string{ContentDatabase}) {
			return 0, errors.New("backup: pre-update archive is not database-only")
		}
		return row.ID, nil
	case stateRunning:
		return 0, ErrBusy
	case stateFailed:
		return retryPreUpdate(ctx, row, actor)
	default:
		return 0, fmt.Errorf("backup: pre-update backup has unexpected state %q", row.State)
	}
}

func retryPreUpdate(ctx context.Context, row record, actor uint) (int64, error) {
	conn, unlock, err := acquirePreparedLock(ctx, row.ID)
	if err != nil {
		return 0, err
	}
	defer func() { unlock(conn) }()
	current, err := loadRecord(ctx, row.ID)
	if err != nil {
		return 0, err
	}
	if current.State != stateFailed {
		if current.State == stateRunning {
			return 0, ErrBusy
		}
		if current.State == stateDone {
			manifest := decodeDatabaseManifest(current.Manifest)
			var metadata beforeUpdateMetadata
			if manifest.malformed || json.Unmarshal(manifest.fields["_jelly_update"], &metadata) != nil {
				return 0, ErrInvalidArchive
			}
			unlock(conn)
			conn = nil
			return reuseOrRetryPreUpdate(ctx, current, actor, metadata.JobID, metadata.Target)
		}
		return 0, ErrNotFailed
	}
	var contents []string
	if err := json.Unmarshal(current.Contents, &contents); err != nil {
		return 0, fmt.Errorf("backup: stored pre-update contents are damaged: %w", err)
	}
	if !slices.Equal(contents, []string{ContentDatabase}) {
		return 0, errors.New("backup: stored pre-update backup is not database-only")
	}
	if err := data.DB.WithContext(ctx).Model(&record{}).Where("id = ?", current.ID).Updates(map[string]any{
		"state": stateRunning, "last_error": nil, "finished_at": nil,
	}).Error; err != nil {
		return 0, fmt.Errorf("backup: mark pre-update backup running: %w", err)
	}
	return current.ID, execute(ctx, current.ID, Request{Contents: contents, Kind: KindManual, Actor: actor})
}
