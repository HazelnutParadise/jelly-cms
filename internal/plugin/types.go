package plugin

import (
	"context"
)

// PluginMetadata defines the structure of a plugin.json file.
type PluginMetadata struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Author      string `json:"author"`
	Entrypoint  string `json:"entrypoint"` // e.g., "index.js"
}

// Manager handles the lifecycle of plugins.
type Manager interface {
	LoadAll(ctx context.Context) error
	Load(ctx context.Context, pluginID string) error
	Unload(ctx context.Context, pluginID string) error
	GetPlugin(pluginID string) (*PluginMetadata, error)
}

// Hook defines an extension point.
type Hook string

const (
	HookOnRequest  Hook = "OnRequest"
	HookOnPostSave Hook = "OnPostSave"
	HookOnBoot     Hook = "OnBoot"
)
