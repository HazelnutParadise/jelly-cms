package server

import (
	"context"

	"github.com/HazelnutParadise/jelly-cms/internal/backup"
	"github.com/HazelnutParadise/jelly-cms/internal/update"
)

func withUpdatePreBackup(ctx context.Context, actor uint, jobID, target string) context.Context {
	return update.WithBeforeSwap(ctx, func(callbackCtx context.Context) error {
		_, err := backup.RunBeforeUpdate(callbackCtx, actor, jobID, target)
		return err
	})
}
