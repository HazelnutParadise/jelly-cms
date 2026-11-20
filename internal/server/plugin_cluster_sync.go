package server

import (
	"context"
	"errors"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
)

// Multi-node plugin sync (ticket 37 R1, R2). Every server shares one
// PostgreSQL database; data/plugins is a per-node copy rebuilt from the
// archives stored in the database (plugin_archive_store.go).

var errPluginClusterSyncNotImplemented = errors.New("plugin cluster sync is not implemented")

// Poll cadence. Tests shorten these.
var (
	pluginSyncPollInterval = 3 * time.Second
	pluginSyncFullInterval = 60 * time.Second
)

// bumpPluginStateSeq advances the shared change sequence (plugin_state_seq)
// after an operation that changes plugin state or extension output. It is
// called next to every local bumpPluginExtensionGeneration that other nodes
// must see. A failure is logged and returned; callers do not fail the request
// on it, the 60-second full reconcile covers a missed bump.
func bumpPluginStateSeq(ctx context.Context) error {
	return errPluginClusterSyncNotImplemented
}

// pluginNode is one server's view: its runtime and its own plugin directory.
// The process-wide node uses pluginDirRoot; tests build a second node with
// another root to stand for another server.
type pluginNode struct {
	runtime *plugin.Runtime
	root    string
}

// newPluginNode returns the node for runtime with plugin files under root.
func newPluginNode(runtime *plugin.Runtime, root string) *pluginNode {
	return &pluginNode{runtime: runtime, root: root}
}

// ReconcileOnce brings this node in line with the database once (R2): it
// invalidates local extension caches when the sequence moved, then for every
// plugin materializes, loads, reloads, unloads or removes the local copy.
// full forces a pass over every plugin even when the sequence did not move.
// A plugin whose install lock another node holds is retried on the next pass.
// Per-plugin failures are logged and do not stop the pass.
func (n *pluginNode) ReconcileOnce(ctx context.Context, full bool) error {
	return errPluginClusterSyncNotImplemented
}

// Run polls the sequence every pluginSyncPollInterval and runs a full pass
// every pluginSyncFullInterval until ctx is cancelled.
func (n *pluginNode) Run(ctx context.Context) {}

// ReconcileAtStartup runs before plugins are loaded at start-up: install
// journal recovery, archive backfill for plugins installed before ticket 37,
// then a full reconcile of the local directory (R2 last rule, R3 backfill).
func (n *pluginNode) ReconcileAtStartup(ctx context.Context) error {
	return errPluginClusterSyncNotImplemented
}
