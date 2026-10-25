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

// Prune applies daily and weekly retention to scheduled backups. Daily slots
// are site-local calendar dates; weekly slots are ISO weeks inside the
// configured rolling calendar-month window. Manual and pre-restore backups
// are never selected for deletion.
func Prune(ctx context.Context) error {
	if data.DB == nil {
		return ErrNotInstalled
	}
	loc := siteLocation(ctx)
	dailyKeep := configuredCount(ctx, "backup_keep_daily", defaultKeepDaily, 0, 10000)
	weeklyMonths := configuredCount(ctx, "backup_keep_weekly_months", 3, 0, 120)
	localKeep := configuredCount(ctx, "backup_local_keep", 3, 0, 10000)
	var rows []record
	if err := data.DB.WithContext(ctx).
		Where("kind = ? AND state = ?", KindScheduled, stateDone).
		Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return err
	}

	keep := make(map[int64]bool)
	days := make(map[string]bool)
	weekStart := time.Time{}
	if weeklyMonths > 0 {
		now := time.Now().In(loc)
		weekStart = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, -(weeklyMonths - 1), 0)
	}
	weeks := make(map[string]bool)
	for _, row := range rows {
		created := row.CreatedAt.In(loc)
		day := created.Format("2006-01-02")
		if !days[day] && len(days) < dailyKeep {
			days[day] = true
			keep[row.ID] = true
		}
		if weeklyMonths == 0 || created.Before(weekStart) {
			continue
		}
		year, week := created.ISOWeek()
		key := strconv.Itoa(year) + "-" + strconv.Itoa(week)
		if !weeks[key] {
			weeks[key] = true
			keep[row.ID] = true
		}
	}

	var errs []error
	for _, row := range rows {
		if keep[row.ID] {
			continue
		}
		if err := deleteBackupRow(ctx, row); err != nil {
			errs = append(errs, err)
		}
	}
	localCopies := 0
	for _, row := range rows {
		if !keep[row.ID] {
			continue
		}
		if _, ok := verifiedRemoteMetadata(row); !ok || row.FilePath == nil {
			continue
		}
		if localCopies < localKeep {
			localCopies++
			continue
		}
		if err := pruneLocalCopy(ctx, row); err != nil {
			errs = append(errs, err)
		}
	}
	if err := removeOldExports(time.Now()); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func configuredCount(ctx context.Context, key string, fallback, min, max int) int {
	n, err := strconv.Atoi(settings.Get(ctx, key))
	if err != nil || n < min || n > max {
		return fallback
	}
	return n
}

func siteLocation(ctx context.Context) *time.Location {
	if name := settings.Get(ctx, "site_timezone"); name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return time.UTC
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
// has started today. It then applies the retention policy.
func RunScheduledIfDue(ctx context.Context, now time.Time) error {
	if data.DB == nil {
		return nil
	}
	if settings.Get(ctx, "backup_enabled") != "true" {
		return nil
	}
	loc := siteLocation(ctx)
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
		return nil
	}
	if err != nil {
		return err
	}
	return Prune(ctx)
}
