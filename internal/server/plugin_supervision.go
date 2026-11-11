package server

import (
	"context"
	"log"
	"sync"

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

// pluginUpgradeGrants maps a plugin id to the permissions hash approved before
// a hot upgrade wrote the new one. While the upgrade runs, the old worker still
// serves host calls and must keep the approval it was started with.
var pluginUpgradeGrants = struct {
	sync.Mutex
	hashes map[string]string
}{hashes: make(map[string]string)}

// registerPluginUpgradeGrant records the approved hash of the row before an
// upgrade writes the new one. Pair it with unregisterPluginUpgradeGrant.
func registerPluginUpgradeGrant(id, hash string) {
	if id == "" || hash == "" {
		return
	}

	pluginUpgradeGrants.Lock()
	defer pluginUpgradeGrants.Unlock()

	pluginUpgradeGrants.hashes[id] = hash
}

// unregisterPluginUpgradeGrant removes the upgrade grant of id.
func unregisterPluginUpgradeGrant(id string) {
	pluginUpgradeGrants.Lock()
	defer pluginUpgradeGrants.Unlock()

	delete(pluginUpgradeGrants.hashes, id)
}

// pluginUpgradeAcceptsGrant reports whether an upgrade in progress registered
// hash for id, so a caller whose manifest hashes to exactly that value may use
// host calls even though the row already holds the new approval.
func pluginUpgradeAcceptsGrant(id, hash string) bool {
	if hash == "" {
		return false
	}

	pluginUpgradeGrants.Lock()
	defer pluginUpgradeGrants.Unlock()

	granted, ok := pluginUpgradeGrants.hashes[id]

	return ok && granted == hash
}
