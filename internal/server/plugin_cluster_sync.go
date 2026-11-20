package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"gorm.io/gorm"
)

// Multi-node plugin sync (ticket 37 R1, R2). Every server shares one
// PostgreSQL database; data/plugins is a per-node copy rebuilt from the
// archives stored in the database (plugin_archive_store.go).

// pluginAdvisoryClass is the first key of withPluginAdvisoryLock.
const pluginAdvisoryClass = 731984203

// Poll cadence. Tests shorten these.
var (
	pluginSyncPollInterval = 3 * time.Second
	pluginSyncFullInterval = 60 * time.Second
)

// readPluginStateSeq returns the current value of plugin_state_seq.
func readPluginStateSeq(ctx context.Context) (int64, error) {
	if data.DB == nil {
		return 0, errors.New("plugin database is unavailable")
	}
	var value int64
	err := data.DB.WithContext(ctx).Raw(`SELECT last_value FROM plugin_state_seq`).Row().Scan(&value)
	return value, err
}

// bumpPluginStateSeq advances the shared change sequence (plugin_state_seq)
// after an operation that changes plugin state or extension output. It is
// called next to every local bumpPluginExtensionGeneration that other nodes
// must see. A failure is returned; callers do not fail the request on it, the
// 60-second full reconcile covers a missed bump.
func bumpPluginStateSeq(ctx context.Context) error {
	if data.DB == nil {
		return errors.New("plugin database is unavailable")
	}
	var value int64
	return data.DB.WithContext(ctx).Raw(`SELECT nextval('plugin_state_seq')`).Row().Scan(&value)
}

// markPluginStateChanged is bumpPluginStateSeq for the call sites that only log.
func markPluginStateChanged(ctx context.Context) {
	if err := bumpPluginStateSeq(ctx); err != nil {
		log.Printf("plugin state sequence not advanced; other servers catch up within %s: %v", pluginSyncFullInterval, err)
	}
}

// loadedPluginTrees remembers, per runtime, the tree digest of the directory
// each loaded plugin was loaded from (ticket 37 R2). A plugin with no entry is
// compared by its local copy instead.
var loadedPluginTrees = struct {
	sync.Mutex
	byRuntime map[*plugin.Runtime]map[string]string
}{byRuntime: map[*plugin.Runtime]map[string]string{}}

// noteLoadedPluginTree records the digest of dir for a plugin just loaded from it.
func noteLoadedPluginTree(rt *plugin.Runtime, id, dir string) {
	if rt == nil {
		return
	}
	tree, err := pluginTreeDigest(dir)
	loadedPluginTrees.Lock()
	defer loadedPluginTrees.Unlock()
	entries := loadedPluginTrees.byRuntime[rt]
	if entries == nil {
		entries = map[string]string{}
		loadedPluginTrees.byRuntime[rt] = entries
	}
	if err != nil {
		delete(entries, id)
		log.Printf("plugin %s: record loaded tree digest: %v", id, err)
		return
	}
	entries[id] = tree
}

// loadedPluginTree returns the digest a loaded plugin was loaded from.
func loadedPluginTree(rt *plugin.Runtime, id string) (string, bool) {
	loadedPluginTrees.Lock()
	defer loadedPluginTrees.Unlock()
	tree, ok := loadedPluginTrees.byRuntime[rt][id]
	return tree, ok
}

// pluginAdvisoryKey is the second advisory-lock key withPluginAdvisoryLock uses.
func pluginAdvisoryKey(id string) int32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return int32(h.Sum32())
}

// withPluginAdvisoryTryLock runs fn under the install advisory lock of id
// without waiting. acquired is false when another server holds it; fn is then
// not run.
func withPluginAdvisoryTryLock(ctx context.Context, id string, fn func() error) (acquired bool, err error) {
	if !plugin.ValidPluginID(id) {
		return false, errPluginNotFound
	}
	if data.DB == nil {
		return false, fmt.Errorf("plugin database is unavailable")
	}
	key := pluginAdvisoryKey(id)
	err = data.DB.Connection(func(conn *gorm.DB) (callbackErr error) {
		var got bool
		if err := conn.WithContext(ctx).Raw(`SELECT pg_try_advisory_lock(?, ?)`, pluginAdvisoryClass, key).Row().Scan(&got); err != nil {
			discardPluginDBConnection(conn)
			return err
		}
		if !got {
			return nil
		}
		acquired = true
		defer func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			unlockErr := conn.WithContext(unlockCtx).Exec(`SELECT pg_advisory_unlock(?, ?)`, pluginAdvisoryClass, key).Error
			if unlockErr != nil {
				discardPluginDBConnection(conn)
			}
			callbackErr = errors.Join(callbackErr, unlockErr)
		}()
		return fn()
	})
	return acquired, err
}

// pluginSyncRow is the plugins row as the sync code reads it.
type pluginSyncRow struct {
	Exists          bool
	Enabled         bool
	Version         string
	PermissionsHash string
	UpdatedAt       time.Time
	ArchiveSHA      *string
}

func readPluginSyncRow(ctx context.Context, id string) (pluginSyncRow, error) {
	var rows []struct {
		Enabled         bool      `gorm:"column:enabled"`
		Version         string    `gorm:"column:version"`
		PermissionsHash string    `gorm:"column:permissions_granted_hash"`
		UpdatedAt       time.Time `gorm:"column:updated_at"`
		ArchiveSHA      *string   `gorm:"column:archive_tree_sha256"`
	}
	if err := data.DB.WithContext(ctx).Raw(`SELECT enabled, version, permissions_granted_hash, updated_at, archive_tree_sha256 FROM plugins WHERE id = ?`, id).Scan(&rows).Error; err != nil {
		return pluginSyncRow{}, err
	}
	if len(rows) == 0 {
		return pluginSyncRow{}, nil
	}
	return pluginSyncRow{
		Exists:          true,
		Enabled:         rows[0].Enabled,
		Version:         rows[0].Version,
		PermissionsHash: rows[0].PermissionsHash,
		UpdatedAt:       rows[0].UpdatedAt,
		ArchiveSHA:      rows[0].ArchiveSHA,
	}, nil
}

func pluginRemovalRecorded(ctx context.Context, id string) (bool, error) {
	var n int64
	err := data.DB.WithContext(ctx).Raw(`SELECT COUNT(*) FROM plugin_removals WHERE plugin_id = ?`, id).Row().Scan(&n)
	return n > 0, err
}

// localPluginDigest returns the tree digest of root/id. present is false when
// the directory does not exist; the digest is then "".
func localPluginDigest(root, id string) (digest string, present bool, err error) {
	dir := filepath.Join(root, id)
	if _, err := os.Lstat(dir); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	digest, err = pluginTreeDigest(dir)
	if err != nil {
		return "", true, err
	}
	return digest, true, nil
}

// materializePluginLocked writes the archive named by row into root/id, using
// the staging, journal and swap flow of an install. The caller holds the
// install lock and any restart hold. The plugins row is not changed.
func materializePluginLocked(ctx context.Context, root, id string, row pluginSyncRow) error {
	if row.ArchiveSHA == nil {
		return fmt.Errorf("plugin %s has no archive", id)
	}
	treeSHA256 := *row.ArchiveSHA
	zipPath, err := openPluginArchive(ctx, id, treeSHA256)
	if err != nil {
		return err
	}
	defer os.Remove(zipPath)
	staged, err := stagePluginArchive(zipPath, root, plugin.ArchiveExpectation{ID: id, Version: row.Version})
	if err != nil {
		return err
	}
	defer staged.Cleanup()
	got, err := pluginTreeDigest(staged.Stage)
	if err != nil {
		return err
	}
	if got != treeSHA256 {
		return fmt.Errorf("plugin %s archive does not reproduce digest %s", id, treeSHA256)
	}
	j, err := commitPluginArchiveIn(root, staged, row.Version, row.PermissionsHash, row.UpdatedAt)
	if err != nil {
		if j.Journaled {
			err = errors.Join(err, rollbackPluginArchiveIn(root, j))
		}
		return err
	}
	if cleanupErr := finishPluginArchiveCommitIn(root, j); cleanupErr != nil {
		log.Printf("plugin %s: post-sync cleanup deferred: %v", id, cleanupErr)
	}
	return nil
}

// backfillPluginArchiveLocked packs the local directory of a plugin installed
// before ticket 37 and points plugins.archive_tree_sha256 at it. The packed
// archive must reproduce the directory digest when staged again. The caller
// holds the install lock.
func backfillPluginArchiveLocked(ctx context.Context, root, id string, row pluginSyncRow) error {
	dir := filepath.Join(root, id)
	digest, present, err := localPluginDigest(root, id)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	meta, err := plugin.ReadManifest(dir)
	if err != nil {
		return err
	}
	if meta.ID != id || meta.Version != row.Version {
		return fmt.Errorf("plugin %s files do not match its row; archive not backfilled", id)
	}
	work, err := os.MkdirTemp("", "jelly-plugin-backfill-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	zipPath := filepath.Join(work, "plugin.zip")
	if err := packPluginDir(dir, zipPath); err != nil {
		return err
	}
	check, err := stagePluginArchive(zipPath, filepath.Join(work, "check"), plugin.ArchiveExpectation{ID: id, Version: row.Version})
	if err != nil {
		return err
	}
	got, err := pluginTreeDigest(check.Stage)
	check.Cleanup()
	if err != nil {
		return err
	}
	if got != digest {
		return fmt.Errorf("plugin %s packed archive does not reproduce its directory digest", id)
	}
	if _, err := storePluginArchive(ctx, id, digest, zipPath); err != nil {
		return err
	}
	// updated_at is the install commit identity and stays untouched.
	return data.DB.WithContext(ctx).Exec(`UPDATE plugins SET archive_tree_sha256 = ? WHERE id = ? AND archive_tree_sha256 IS NULL`, digest, id).Error
}

// ensurePluginLocalCopy makes data/plugins/<id> match plugins.archive_tree_sha256
// before an admin path uses it, so a server that has not synced yet does not
// act on stale files. The caller holds the install lock.
func ensurePluginLocalCopy(ctx context.Context, rt *plugin.Runtime, id string) error {
	row, err := readPluginSyncRow(ctx, id)
	if err != nil {
		return err
	}
	if !row.Exists || row.ArchiveSHA == nil {
		return nil
	}
	digest, present, err := localPluginDigest(pluginDirRoot, id)
	if err != nil {
		return err
	}
	if present && digest == *row.ArchiveSHA {
		return nil
	}
	release := func() {}
	if rt != nil {
		release, err = rt.HoldRestart(ctx, id)
		if err != nil {
			return err
		}
	}
	defer release()
	return materializePluginLocked(ctx, pluginDirRoot, id, row)
}

// pluginNode is one server's view: its runtime and its own plugin directory.
// The process-wide node uses pluginDirRoot; tests build a second node with
// another root to stand for another server.
type pluginNode struct {
	runtime *plugin.Runtime
	root    string
	// files serializes this node's directory changes in this process. The
	// process-wide node shares pluginFilesMu with the install paths.
	files *sync.Mutex
	// seen is the plugin_state_seq value of the last pass that reconciled everything.
	seen int64
	// pending holds plugins whose install lock another server held; they are
	// retried on every pass.
	pending map[string]bool
}

// newPluginNode returns the node for runtime with plugin files under root.
func newPluginNode(runtime *plugin.Runtime, root string) *pluginNode {
	n := &pluginNode{runtime: runtime, root: root, files: &sync.Mutex{}, pending: map[string]bool{}}
	if root == pluginDirRoot {
		n.files = &pluginFilesMu
	}
	// A plugin's OnBoot calls the host APIs, so a runtime that loads plugins
	// needs the handler. Only the handler is set: ConfigurePluginHost also
	// replaces process-wide state that a second node must not touch.
	if runtime != nil {
		runtime.SetHostHandler(handlePluginHostCall)
	}
	return n
}

// ReconcileOnce brings this node in line with the database once (R2): it
// invalidates local extension caches when the sequence moved, then for every
// plugin materializes, loads, reloads, unloads or removes the local copy.
// full forces a pass over every plugin even when the sequence did not move.
// A plugin whose install lock another node holds is retried on the next pass.
// Per-plugin failures are logged and do not stop the pass.
func (n *pluginNode) ReconcileOnce(ctx context.Context, full bool) error {
	if n.runtime == nil {
		return errors.New("plugin runtime is unavailable")
	}
	n.files.Lock()
	defer n.files.Unlock()

	seq, err := readPluginStateSeq(ctx)
	if err != nil {
		return err
	}
	targets := map[string]bool{}
	if full || seq > n.seen {
		bumpPluginExtensionGeneration()
		ids, err := n.pluginIDs(ctx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			targets[id] = true
		}
		n.seen = seq
	}
	for id := range n.pending {
		targets[id] = true
	}
	n.pending = map[string]bool{}

	ids := make([]string, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var errs []error
	for _, id := range ids {
		acquired, err := n.reconcilePlugin(ctx, id)
		if !acquired {
			n.pending[id] = true
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// reconcilePlugin syncs one plugin under its install lock. acquired is false
// when another server holds the lock; the plugin is then left alone.
func (n *pluginNode) reconcilePlugin(ctx context.Context, id string) (bool, error) {
	return withPluginAdvisoryTryLock(ctx, id, func() error {
		return n.syncLocked(ctx, id, true)
	})
}

// syncLocked brings one plugin in line with its row. load=false only makes the
// local files match (start-up, before LoadWhere loads them). The caller holds
// the install lock.
func (n *pluginNode) syncLocked(ctx context.Context, id string, load bool) error {
	if err := n.recoverJournal(id); err != nil {
		return err
	}
	row, err := readPluginSyncRow(ctx, id)
	if err != nil {
		return err
	}
	removed, err := pluginRemovalRecorded(ctx, id)
	if err != nil {
		return err
	}
	loaded := n.runtime.IsLoaded(id)

	if !row.Exists {
		// A directory with no row and no removal record is a manual install and stays.
		if !removed {
			return nil
		}
		if loaded {
			if err := n.runtime.Unload(ctx, id); err != nil {
				return err
			}
		}
		return n.removeLocalCopy(id)
	}
	if !row.Enabled {
		if loaded {
			return n.runtime.Unload(ctx, id)
		}
		return nil
	}
	if row.ArchiveSHA == nil {
		// Installed before ticket 37 and not backfilled: nothing to sync from.
		return nil
	}

	want := *row.ArchiveSHA
	local, present, err := localPluginDigest(n.root, id)
	if err != nil {
		return err
	}
	needFiles := !present || local != want
	needLoad := false
	if load {
		if loaded {
			tree, known := loadedPluginTree(n.runtime, id)
			if !known {
				tree = local
			}
			needLoad = tree != want
		} else {
			needLoad = true
		}
	}
	if !needFiles && !needLoad {
		return nil
	}

	// Keep the supervisor from restarting onto the files while they change.
	release, err := n.runtime.HoldRestart(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	if needFiles {
		if err := materializePluginLocked(ctx, n.root, id, row); err != nil {
			return err
		}
	}
	if !needLoad {
		return nil
	}

	dir := filepath.Join(n.root, id)
	if loaded {
		err = n.runtime.ReloadForID(ctx, id, dir)
	} else {
		err = n.runtime.Load(ctx, dir)
	}
	if err != nil {
		if loaded {
			// The old instance keeps running, but its directory now holds the new
			// files, so an automatic restart must not load them.
			n.runtime.ForbidRestarts(id, err)
		}
		return err
	}
	noteLoadedPluginTree(n.runtime, id, dir)
	return nil
}

// recoverJournal resolves an interrupted install or uninstall of id in this
// node's directory. The caller holds the install lock.
func (n *pluginNode) recoverJournal(id string) error {
	path := pluginInstallJournalPathIn(n.root, id)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var j pluginInstallJournal
	if err := json.Unmarshal(raw, &j); err != nil || validatePluginInstallJournal(j) != nil || j.ID != id {
		return fmt.Errorf("invalid plugin transaction journal %s", path)
	}
	return recoverPluginInstallJournalIn(n.root, path, j)
}

// removeLocalCopy deletes root/id through the uninstall transaction flow: the
// directory moves to a backup, a journal records the swap, then both go. A
// crash before the journal leaves only a backup that nothing reads.
func (n *pluginNode) removeLocalCopy(id string) error {
	dir := filepath.Join(n.root, id)
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("plugin %s directory is not a regular directory", id)
	}
	treeSHA256, err := pluginTreeDigest(dir)
	if err != nil {
		return err
	}
	backupName := fmt.Sprintf(".jelly-plugin-uninstall-%s-%d", id, time.Now().UnixNano())
	backup := pluginTransactionPathIn(n.root, backupName)
	if err := os.Rename(dir, backup); err != nil {
		return err
	}
	j := pluginInstallJournal{Action: "uninstall", ID: id, TreeSHA256: treeSHA256, Backup: backupName, Phase: "swapped"}
	journal := pluginInstallJournalPathIn(n.root, id)
	if err := writePluginInstallJournalIn(n.root, journal, j); err != nil {
		if restoreErr := os.Rename(backup, dir); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return err
	}
	j.Journaled = true
	return finishPluginArchiveCommitIn(n.root, j)
}

// pluginIDs lists every plugin this pass must look at: rows, removal records,
// local directories and loaded plugins.
func (n *pluginNode) pluginIDs(ctx context.Context) ([]string, error) {
	set := map[string]bool{}
	var fromRows []string
	if err := data.DB.WithContext(ctx).Table("plugins").Pluck("id", &fromRows).Error; err != nil {
		return nil, err
	}
	var removed []string
	if err := data.DB.WithContext(ctx).Table("plugin_removals").Pluck("plugin_id", &removed).Error; err != nil {
		return nil, err
	}
	for _, id := range append(fromRows, removed...) {
		set[id] = true
	}
	entries, err := os.ReadDir(n.root)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			set[entry.Name()] = true
		}
	}
	for _, meta := range n.runtime.LoadedPlugins() {
		set[meta.ID] = true
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		if plugin.ValidPluginID(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// Run polls the sequence every pluginSyncPollInterval and runs a full pass
// every pluginSyncFullInterval until ctx is cancelled.
func (n *pluginNode) Run(ctx context.Context) {
	poll := time.NewTicker(pluginSyncPollInterval)
	defer poll.Stop()
	full := time.NewTicker(pluginSyncFullInterval)
	defer full.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			if err := n.ReconcileOnce(ctx, false); err != nil && ctx.Err() == nil {
				log.Printf("plugin sync: %v", err)
			}
		case <-full.C:
			if err := n.ReconcileOnce(ctx, true); err != nil && ctx.Err() == nil {
				log.Printf("plugin sync: %v", err)
			}
		}
	}
}

// ReconcileAtStartup runs before plugins are loaded at start-up: install
// journal recovery, archive backfill for plugins installed before ticket 37,
// then a full reconcile of the local directory (R2 last rule, R3 backfill). It
// never loads a plugin; LoadWhere does that.
func (n *pluginNode) ReconcileAtStartup(ctx context.Context) error {
	n.files.Lock()
	defer n.files.Unlock()

	if err := recoverPluginInstallJournalsIn(n.root); err != nil {
		return err
	}
	seq, err := readPluginStateSeq(ctx)
	if err != nil {
		return err
	}
	ids, err := n.pluginIDs(ctx)
	if err != nil {
		return err
	}

	var errs []error
	for _, id := range ids {
		err := withPluginAdvisoryLock(ctx, id, func() error {
			row, err := readPluginSyncRow(ctx, id)
			if err != nil || !row.Exists || row.ArchiveSHA != nil {
				return err
			}
			return backfillPluginArchiveLocked(ctx, n.root, id, row)
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: archive backfill: %w", id, err))
		}
	}
	for _, id := range ids {
		err := withPluginAdvisoryLock(ctx, id, func() error {
			return n.syncLocked(ctx, id, false)
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: %w", id, err))
		}
	}
	n.seen = seq
	return errors.Join(errs...)
}

// PluginSync is this process's plugin sync (ticket 37): the process directory
// data/plugins follows the shared database.
type PluginSync struct {
	node *pluginNode
}

// NewPluginSync returns the sync of this process for runtime.
func NewPluginSync(runtime *plugin.Runtime) *PluginSync {
	return &PluginSync{node: newPluginNode(runtime, pluginDirRoot)}
}

// ReconcileAtStartup must run before LoadEnabled.
func (s *PluginSync) ReconcileAtStartup(ctx context.Context) error {
	return s.node.ReconcileAtStartup(ctx)
}

// LoadEnabled loads the enabled plugin directories as before ticket 37 and
// remembers the digest each one was loaded from.
func (s *PluginSync) LoadEnabled(ctx context.Context) error {
	rt := s.node.runtime
	if err := rt.LoadWhere(ctx, pluginDirRoot, PluginEnabledFilter()); err != nil {
		return err
	}
	for _, meta := range rt.LoadedPlugins() {
		noteLoadedPluginTree(rt, meta.ID, filepath.Join(pluginDirRoot, meta.ID))
	}
	return nil
}

// Run keeps the plugins in line until ctx is done. Call it after LoadEnabled.
func (s *PluginSync) Run(ctx context.Context) {
	s.node.Run(ctx)
}
