package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SendFunc performs one actual delivery. The caller supplies configured SMTP
// and SSRF-safe webhook transport; this package owns durable outbox state.
type SendFunc func(context.Context, Delivery) error

// DueDeliveryIDs returns pending or quiet-hour-held deliveries ready to run.
func DueDeliveryIDs(ctx context.Context, db *gorm.DB, limit int) ([]uint, error) {
	if db == nil {
		return nil, fmt.Errorf("notify: database is not initialized")
	}
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	var ids []uint
	err := db.WithContext(ctx).Model(&core.NotificationDelivery{}).
		Where("status IN ? AND next_attempt_at <= ?", []string{"pending", "held"}, time.Now().UTC()).
		Order("next_attempt_at, id").Limit(limit).Pluck("id", &ids).Error
	return ids, err
}

// ProcessDelivery serializes one outbox row across workers, honors the
// recipient's quiet window, and records bounded retry state.
func ProcessDelivery(ctx context.Context, db *gorm.DB, id uint, send SendFunc) error {
	if db == nil || id == 0 || send == nil {
		return fmt.Errorf("notify: delivery processor is not configured")
	}
	var deliveryErr error
	txErr := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row core.NotificationDelivery
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, id).Error; err != nil {
			return err
		}
		if row.Status == "sent" || row.Status == "failed" {
			return nil
		}
		if row.Status != "pending" && row.Status != "held" {
			return fmt.Errorf("notify: unsupported delivery state")
		}
		now := time.Now().UTC()
		if now.Before(row.NextAttemptAt) {
			return nil
		}
		var prefs core.NotificationSettings
		var userID uint
		if row.NotificationID != 0 {
			var n core.Notification
			if err := tx.Select("user_id").First(&n, row.NotificationID).Error; err != nil {
				return err
			}
			userID = n.UserID
			if err := tx.First(&prefs, "user_id = ?", userID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if quietNow(now, prefs) {
			row.Status = "held"
			row.NextAttemptAt = quietEnd(now, prefs)
			row.Error = ""
			return tx.Model(&core.NotificationDelivery{}).Where("id = ?", id).Updates(map[string]any{"status": row.Status, "next_attempt_at": row.NextAttemptAt, "error": ""}).Error
		}
		delivery := Delivery{ID: row.ID, NotificationID: row.NotificationID, Channel: row.Channel, Kind: row.Kind, Target: row.ToAddress}
		if err := json.Unmarshal(row.Params, &delivery.Params); err != nil {
			return fmt.Errorf("notify: delivery parameters are invalid")
		}
		if row.Channel == "webhook" {
			delivery.Target = prefs.WebhookURL
		}
		row.Attempts++
		if err := send(ctx, delivery); err != nil {
			deliveryErr = err
			row.Error = "delivery failed"
			if row.Attempts >= 5 {
				row.Status = "failed"
			} else {
				row.Status = "pending"
				delay := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}[min(row.Attempts-1, 2)]
				row.NextAttemptAt = now.Add(delay)
			}
			if err := tx.Model(&core.NotificationDelivery{}).Where("id = ?", id).Updates(map[string]any{"attempts": row.Attempts, "status": row.Status, "next_attempt_at": row.NextAttemptAt, "error": row.Error}).Error; err != nil {
				return err
			}
			return nil
		}
		return tx.Model(&core.NotificationDelivery{}).Where("id = ?", id).Updates(map[string]any{"attempts": row.Attempts, "status": "sent", "error": ""}).Error
	})
	if txErr != nil {
		return txErr
	}
	return deliveryErr
}

func quietEnd(now time.Time, pref core.NotificationSettings) time.Time {
	location, err := time.LoadLocation(pref.QuietTZ)
	if err != nil {
		return now.Add(time.Hour)
	}
	end, err := time.Parse("15:04", pref.QuietEnd)
	if err != nil {
		return now.Add(time.Hour)
	}
	local := now.In(location)
	next := time.Date(local.Year(), local.Month(), local.Day(), end.Hour(), end.Minute(), 0, 0, location)
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}
	return next.UTC()
}

// Prune removes expired inbox entries and old terminal outbox records.
func Prune(ctx context.Context, db *gorm.DB, now time.Time) error {
	if db == nil {
		return fmt.Errorf("notify: database is not initialized")
	}
	if err := db.WithContext(ctx).Where("expires_at <= ?", now.UTC()).Delete(&core.Notification{}).Error; err != nil {
		return err
	}
	return db.WithContext(ctx).Where("status IN ? AND next_attempt_at < ?", []string{"sent", "failed"}, now.UTC().Add(-90*24*time.Hour)).Delete(&core.NotificationDelivery{}).Error
}
