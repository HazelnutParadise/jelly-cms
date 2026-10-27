package server

import (
	"context"
	"encoding/json"

	"github.com/HazelnutParadise/jelly-cms/internal/backup"
	"github.com/HazelnutParadise/jelly-cms/internal/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// lockMediaPolicy serializes policy edits with backup destination references
// and file creation, which takes a share lock on the same row.
func lockMediaPolicy(ctx context.Context, tx *gorm.DB, id int64) (mediaPolicyRow, error) {
	var row mediaPolicyRow
	err := tx.WithContext(ctx).Table("media_policies").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND deleted_at IS NULL", id).Take(&row).Error
	return row, err
}

func mediaPolicyHasFiles(ctx context.Context, tx *gorm.DB, id int64) (bool, error) {
	var count int64
	err := tx.WithContext(ctx).Table("media_files").Where("policy_id = ?", id).Count(&count).Error
	return count > 0, err
}

func mediaPolicyHasBackupReference(ctx context.Context, tx *gorm.DB, id int64) (bool, error) {
	return backup.PolicyReferenced(ctx, tx, id)
}

// mediaPolicyDestinationChanged compares normalized driver configuration,
// excluding credentials, which are stored separately and may rotate in place.
func mediaPolicyDestinationChanged(driver, before, after string) (bool, error) {
	if driver != storage.DriverS3 {
		return before != after, nil
	}
	var oldConfig, newConfig mediaS3StoredConfig
	if err := json.Unmarshal([]byte(before), &oldConfig); err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(after), &newConfig); err != nil {
		return false, err
	}
	return oldConfig != newConfig, nil
}
