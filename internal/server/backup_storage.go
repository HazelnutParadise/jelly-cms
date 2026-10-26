package server

import "github.com/HazelnutParadise/jelly-cms/internal/backup"

// Register the storage resolver during package setup. The callback is invoked
// by backup workers with the destination ID captured on the backup row.
func init() {
	backup.ConfigureStorageResolver(mediaBackupStorageDriver)
}
