package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type accountSessionResponse struct {
	ID          string    `json:"id"`
	DeviceLabel string    `json:"device_label"`
	IPMasked    string    `json:"ip_masked"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	Current     bool      `json:"current"`
	NewDevice   bool      `json:"new_device"`
}

func listAccountSessions(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	now := time.Now()
	var sessions []core.LoginSession
	if err := data.DB.WithContext(c.Request().Context()).
		Where("user_id = ? AND revoked_at IS NULL AND expires_at > ?", user.ID, now).
		Order("last_seen_at DESC, created_at DESC").Find(&sessions).Error; err != nil {
		return accountServerError(c, "Unable to load sessions")
	}
	type deviceCount struct {
		UAHash string
		Count  int64
	}
	var counts []deviceCount
	if err := data.DB.WithContext(c.Request().Context()).Model(&core.LoginSession{}).
		Select("ua_hash, COUNT(*) AS count").
		Where("user_id = ? AND created_at > ?", user.ID, now.Add(-90*24*time.Hour)).
		Group("ua_hash").Scan(&counts).Error; err != nil {
		return accountServerError(c, "Unable to load sessions")
	}
	recentDevices := make(map[string]int64, len(counts))
	for _, count := range counts {
		recentDevices[count.UAHash] = count.Count
	}
	current := currentAccountLoginHash(c)
	response := make([]accountSessionResponse, 0, len(sessions))
	for _, session := range sessions {
		response = append(response, accountSessionResponse{
			ID: session.ID[:16], DeviceLabel: session.DeviceLabel, IPMasked: session.IPMasked,
			CreatedAt: session.CreatedAt, LastSeenAt: session.LastSeenAt,
			Current: session.ID == current, NewDevice: session.UAHash != "" && recentDevices[session.UAHash] == 1,
		})
	}
	return c.JSON(http.StatusOK, response)
}

func revokeAccountSession(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	id := c.Param("id")
	if len(id) != 16 || !isLowerHex(id) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Session not found"})
	}
	now := time.Now()
	err = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		var sessions []core.LoginSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND id LIKE ? AND revoked_at IS NULL AND expires_at > ?", user.ID, id+"%", now).
			Limit(2).Find(&sessions).Error; err != nil {
			return err
		}
		if len(sessions) != 1 {
			return gorm.ErrRecordNotFound
		}
		return revokeLoginSessions(tx, sessions, now)
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Session not found"})
		}
		return accountServerError(c, "Unable to revoke session")
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Session revoked"})
}

func revokeOtherAccountSessions(c echo.Context) error {
	user, err := accountUser(c)
	if err != nil {
		return err
	}
	current := currentAccountLoginHash(c)
	err = auth.RevokeUserLogins(data.DB.WithContext(c.Request().Context()), user.ID, current)
	if err != nil {
		return accountServerError(c, "Unable to revoke other sessions")
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Other sessions revoked"})
}

func revokeLoginSessions(tx *gorm.DB, sessions []core.LoginSession, now time.Time) error {
	if len(sessions) == 0 {
		return nil
	}
	ids := make([]string, len(sessions))
	revoked := make([]core.RevokedLogin, 0, len(sessions))
	for i, session := range sessions {
		ids[i] = session.ID
		revoked = append(revoked, core.RevokedLogin{ID: session.ID, ExpiresAt: session.ExpiresAt})
	}
	if err := tx.Model(&core.LoginSession{}).Where("id IN ?", ids).Update("revoked_at", now).Error; err != nil {
		return err
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&revoked).Error
}

func isLowerHex(value string) bool {
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
