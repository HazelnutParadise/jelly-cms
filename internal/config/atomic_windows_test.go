//go:build windows

package config

import "errors"

// limitFileSize is not available on Windows, so the write-failure test skips.
func limitFileSize(int64) (func(), error) {
	return nil, errors.New("file size limits are not available on Windows")
}
