package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
)

// Prune applies the retention policy. It keeps the newest scheduled backup of
// each of the newest backup_keep_daily days and removes the other scheduled
// backups. Manual and pre_restore backups are kept. Exports older than seven
// days are removed as well.
func Prune(ctx context.Context) error {
	if data.DB == nil {
		return ErrNotInstalled
	}
	keep := keepDaily(ctx)
	var rows []record
	if err := data.DB.WithContext(ctx).
		Where("kind = ? AND state = ?", KindScheduled, stateDone).
		Order("created_at DESC, id DESC").
		Find(&rows).Error; err != nil {
		return err
	}
	var errs []error
	kept := map[string]bool{}
	for _, row := range rows {
		day := row.CreatedAt.UTC().Format("2006-01-02")
		if !kept[day] && len(kept) < keep {
			kept[day] = true
			continue
		}
		if err := removeArchive(row); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := data.DB.WithContext(ctx).Delete(&record{}, row.ID).Error; err != nil {
			errs = append(errs, err)
		}
	}
	if err := removeOldExports(time.Now()); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func keepDaily(ctx context.Context) int {
	n, err := strconv.Atoi(settings.Get(ctx, "backup_keep_daily"))
	if err != nil || n < 1 {
		return defaultKeepDaily
	}
	return n
}

func removeOldExports(now time.Time) error {
	entries, err := os.ReadDir(ExportDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if now.Sub(info.ModTime()) <= exportRetention {
			continue
		}
		if err := os.Remove(filepath.Join(ExportDirectory, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// RunScheduledIfDue writes the daily scheduled backup when backup_enabled is
// on, the clock in site_timezone shows backup_time, and no scheduled backup
// has started today. It then applies the retention policy. The database row
// is what prevents a second run after a restart, so no state is kept in memory.
func RunScheduledIfDue(ctx context.Context, now time.Time) error {
	if data.DB == nil {
		return nil
	}
	if settings.Get(ctx, "backup_enabled") != "true" {
		return nil
	}
	loc := time.UTC
	if name := settings.Get(ctx, "site_timezone"); name != "" {
		if l, err := time.LoadLocation(name); err == nil {
			loc = l
		}
	}
	local := now.In(loc)
	if local.Format("15:04") != settings.Get(ctx, "backup_time") {
		return nil
	}
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).UTC()
	var started int64
	if err := data.DB.WithContext(ctx).Model(&record{}).
		Where("kind = ? AND created_at >= ?", KindScheduled, dayStart).
		Count(&started).Error; err != nil {
		return err
	}
	if started > 0 {
		return nil
	}
	_, err := Run(ctx, Request{Contents: []string{ContentDatabase, ContentSettings}, Kind: KindScheduled})
	if errors.Is(err, ErrBusy) {
		// A manual backup or restore is running. The next tick in this minute retries.
		return nil
	}
	if err != nil {
		return err
	}
	return Prune(ctx)
}
