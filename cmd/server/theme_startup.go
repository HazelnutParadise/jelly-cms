package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"gorm.io/gorm"
)

func activateStartupTheme(ctx context.Context, tm *theme.Manager, db *gorm.DB) error {
	if db == nil {
		if err := tm.Activate("default"); err != nil {
			return fmt.Errorf("activate default theme: %w", err)
		}
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var option core.Option
	readErr := db.WithContext(ctx).Where("key = ?", "active_theme").First(&option).Error
	if readErr == nil && strings.TrimSpace(option.Value) == "" {
		return activateDefaultTheme(tm, nil)
	}
	if readErr == nil {
		if err := tm.Activate(option.Value); err == nil {
			return nil
		} else {
			return activateDefaultTheme(tm, fmt.Errorf("restore saved theme %q: %w", option.Value, err))
		}
	}
	if errors.Is(readErr, gorm.ErrRecordNotFound) {
		return activateDefaultTheme(tm, nil)
	}
	return activateDefaultTheme(tm, fmt.Errorf("read saved active theme: %w", readErr))
}

func activateDefaultTheme(tm *theme.Manager, cause error) error {
	if err := tm.Activate("default"); err != nil {
		if cause != nil {
			return fmt.Errorf("%v; activate default theme: %w", cause, err)
		}
		return fmt.Errorf("activate default theme: %w", err)
	}
	return cause
}
