package server

import (
	"context"
	"log"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/notify"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
)

// Display states that are not part of the worker supervision states.
const (
	pluginStateDisabled = "disabled"
	pluginStateStopped  = "stopped"
)

// installPluginFailureHandler wires the supervision notifications. The failure
// handler runs outside the runtime locks after every failure and once more when a
// plugin enters the crashed state. The recovery handler runs after an automatic
// restart succeeded; it only moves the extension generation, no notification.
func installPluginFailureHandler(runtime *plugin.Runtime) {
	if runtime == nil {
		return
	}

	runtime.SetRecoveryHandler(func(id string) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("plugin %s: recovery handler panicked: %v", id, recovered)
			}
		}()

		bumpPluginExtensionGeneration()
	})

	runtime.SetFailureHandler(func(id string, status plugin.PluginStatus) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("plugin %s: failure handler panicked: %v", id, recovered)
			}
		}()

		bumpPluginExtensionGeneration()
		if status.State != plugin.StateCrashed || data.DB == nil {
			return
		}

		name := id
		if meta, err := runtime.GetPlugin(id); err == nil && meta != nil && meta.Name != "" {
			name = meta.Name
		}
		if err := notify.Emit(context.Background(), data.DB, notify.Event{
			Kind:     notify.KindSystemPluginFailed,
			Category: notify.CategorySystem,
			Params: map[string]any{
				"plugin": id,
				"name":   name,
				"error":  status.LastError,
			},
			GroupKey:   notify.KindSystemPluginFailed + ":" + id,
			Recipients: notify.Recipients{Roles: []string{"admin"}},
		}); err != nil {
			log.Printf("plugin %s: send failure notification: %v", id, err)
		}
	})
}

// pluginStatusView returns the display state and supervision status of a plugin.
// A disabled plugin is disabled even if a stale runtime entry still exists.
func pluginStatusView(enabled bool, runtime *plugin.Runtime, id string) (string, plugin.PluginStatus) {
	status := plugin.PluginStatus{}
	if runtime != nil {
		if current, ok := runtime.Status(id); ok {
			status = current
		}
	}
	if !enabled {
		return pluginStateDisabled, status
	}
	if status.State != "" {
		return status.State, status
	}

	return pluginStateStopped, status
}
