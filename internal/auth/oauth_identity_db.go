package auth

import (
	"context"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"
)

func oauthIdentityDB(ctx context.Context) *gorm.DB {
	if data.DB == nil {
		return nil
	}
	return data.DB.WithContext(ctx).Session(&gorm.Session{
		Logger: gormLogger.Discard.LogMode(gormLogger.Silent),
	})
}
