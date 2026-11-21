package server

// Acceptance tests for ticket 37 (multi-node plugin sync): plugin state and
// extension caches stay consistent across servers that share one PostgreSQL
// database but keep their own data/plugins directory.
//
// Node A is the harness HTTP router with h.runtime and pluginDirRoot. Node B is
// a second runtime with its own directory under t.TempDir(), driven through
// newPluginNode and ReconcileOnce. Covers R1 (plugin_state_seq), R2 (reconcile,
// advisory-lock pending retry, startup reconcile) and R3 (archives stored in
// PostgreSQL as chunks, backfill, tamper detection).
//
// This file is the ticket-37 contract and is written before the implementation.
// Implementers must not edit this file; make plugin_cluster_sync.go,
// plugin_archive_store.go and the install, upgrade and uninstall paths satisfy
// it instead.
//
// Every test needs JELLY_TEST_POSTGRES=1 and the dedicated PostgreSQL container.
// Waits poll with a bounded loop; a fixed sleep is never followed directly by an
// assertion. Assertions that read tables added by ticket 37 use the table names
// from the ticket and, where the ticket leaves column names open, match rows by
// their JSON text so this file does not depend on those names.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
)

// pcsAdvisoryClass is the first key of withPluginAdvisoryLock
// (plugin_install_recovery.go). The second key is the FNV-1a hash of the id.
const pcsAdvisoryClass = 731984203

// pcsBudget bounds each wait for a change to appear on another node.
const pcsBudget = 5 * time.Second

// pcsFixture is node A (the harness) plus node B, which shares its database.
type pcsFixture struct {
	h        *reloadHarness
	token    string
	rootB    string
	runtimeB *plugin.Runtime
	nodeB    *pluginNode
}

// newPcsFixture builds the two-node setup. Node B's runtime is closed on cleanup.
func newPcsFixture(t *testing.T, admin string) *pcsFixture {
	t.Helper()

	h := newPluginReloadHarness(t)
	token := h.token(h.user("admin", admin))
	rootB := filepath.Join(t.TempDir(), "plugins")
	if err := os.MkdirAll(rootB, 0o755); err != nil {
		t.Fatalf("create node B plugin directory %s: %v", rootB, err)
	}
	runtimeB, err := plugin.NewRuntime()
	if err != nil {
		t.Fatalf("create node B runtime: %v", err)
	}
	t.Cleanup(runtimeB.Close)

	return &pcsFixture{h: h, token: token, rootB: rootB, runtimeB: runtimeB, nodeB: newPluginNode(runtimeB, rootB)}
}

// pcsCtx returns a context bounded by the test's lifetime and a generous limit.
func pcsCtx(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// pcsReconcile runs one ReconcileOnce pass on n and requires it to succeed.
func pcsReconcile(t *testing.T, n *pluginNode, full bool) {
	t.Helper()

	if err := n.ReconcileOnce(pcsCtx(t), full); err != nil {
		t.Fatalf("ReconcileOnce(full=%t) = %v, want nil", full, err)
	}
}

// pcsDigest returns the tree digest of root/id and whether the directory exists.
func pcsDigest(t *testing.T, root, id string) (string, bool) {
	t.Helper()

	dir := filepath.Join(root, id)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return "", false
		}
		t.Fatalf("stat plugin directory %s: %v", dir, err)
	}
	digest, err := pluginTreeDigest(dir)
	if err != nil {
		t.Fatalf("hash plugin tree %s: %v", dir, err)
	}
	return digest, true
}

// pcsLoadedVersion returns the version of a loaded plugin, or "" when it is not loaded.
func pcsLoadedVersion(runtime *plugin.Runtime, id string) string {
	meta, err := runtime.GetPlugin(id)
	if err != nil || meta == nil {
		return ""
	}
	return meta.Version
}

// pcsArchiveDigest reads plugins.archive_tree_sha256 for id ("" when NULL).
func pcsArchiveDigest(t *testing.T, h *accessHarness, id string) string {
	t.Helper()

	var rows []struct {
		Digest *string `gorm:"column:archive_tree_sha256"`
	}
	if err := h.db.Raw(`SELECT archive_tree_sha256 FROM plugins WHERE id = ?`, id).Scan(&rows).Error; err != nil {
		t.Fatalf("read plugins.archive_tree_sha256 for %s: %v", id, err)
	}
	if len(rows) != 1 {
		t.Fatalf("plugins rows for %s = %d, want 1", id, len(rows))
	}
	if rows[0].Digest == nil {
		return ""
	}
	return *rows[0].Digest
}

// pcsSeq reads the current value of plugin_state_seq (R1).
func pcsSeq(t *testing.T, h *accessHarness) int64 {
	t.Helper()

	var value int64
	if err := h.db.Raw(`SELECT last_value FROM plugin_state_seq`).Row().Scan(&value); err != nil {
		t.Fatalf("read plugin_state_seq last_value: %v", err)
	}
	return value
}

// pcsRowsMentioning counts rows of table whose JSON text contains every value as
// a quoted string. It identifies archive rows without depending on column names.
func pcsRowsMentioning(t *testing.T, h *accessHarness, table string, values ...string) int64 {
	t.Helper()

	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s t WHERE TRUE`, table)
	args := make([]any, 0, len(values))
	for _, v := range values {
		query += ` AND to_jsonb(t)::text LIKE ?`
		args = append(args, `%"`+v+`"%`)
	}
	var n int64
	if err := h.db.Raw(query, args...).Row().Scan(&n); err != nil {
		t.Fatalf("count %s rows mentioning %v: %v", table, values, err)
	}
	return n
}

// pcsLogCount drains the plugin log pipelines and counts marker lines so far.
func pcsLogCount(t *testing.T, h *reloadHarness, marker string) int {
	t.Helper()

	drainPluginLogPipelines(t)
	return supervisionLogCount(h, marker)
}

// pcsBootScript returns a plugin entry that logs one OnBoot marker.
func pcsBootScript(run, label string) string {
	return fmt.Sprintf(`
JellyCMS.registerHook("OnBoot", function () {
  JellyCMS.log.info("PCS-%s-boot-%s");
});
`, run, label)
}

// pcsInstall uploads a plugin through the real endpoints. The first install
// starts disabled; a plugin already installed is hot upgraded in place.
func pcsInstall(t *testing.T, f *pcsFixture, id, version, script string) {
	t.Helper()

	archive := hotUpgradeArchive(t, hotUpgradeManifest(id, version, nil, nil, nil), script)
	if r := hotUpgradeInstallPreview(t, f.h.accessHarness, f.token, archive); r.code != http.StatusOK {
		t.Fatalf("upload %s %s = %d, want 200; body=%q", id, version, r.code, r.body)
	}
}

// pcsUpgrade uploads a new version or content over a running plugin and waits
// for its boot marker on node A.
func pcsUpgrade(t *testing.T, f *pcsFixture, id, version, script, marker string) {
	t.Helper()

	pcsInstall(t, f, id, version, script)
	f.h.logs.waitFor(t, marker)
}

// pcsEnable enables an installed plugin on node A and waits for its boot marker.
func pcsEnable(t *testing.T, f *pcsFixture, id, marker string) {
	t.Helper()

	hotUpgradeEnable(t, f.h, f.token, id, marker)
}

// pcsIncompressibleText returns size bytes of printable ASCII (without '*') that
// deflate cannot shrink much, so the archive stays larger than the chunk size.
func pcsIncompressibleText(t *testing.T, size int) string {
	t.Helper()

	alphabet := make([]byte, 0, 94)
	for c := byte(0x20); c <= 0x7e; c++ {
		if c != '*' {
			alphabet = append(alphabet, c)
		}
	}
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("read random bytes for the padding: %v", err)
	}
	for i, b := range raw {
		raw[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(raw)
}

// pcsSHA256Hex returns the SHA-256 of the file at path, hex encoded.
func pcsSHA256Hex(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// pcsLockKey is the second advisory-lock key withPluginAdvisoryLock uses for id.
func pcsLockKey(id string) int32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return int32(h.Sum32())
}

// 1. A installs and enables; B's first pass materializes the directory and loads
// the plugin with the same tree digest. OnBoot runs once on each node.
func TestPluginClusterSyncInstallEnableReachesOtherNode(t *testing.T) {
	f := newPcsFixture(t, "pcs-install-admin")
	const id = "pcs-install"
	run := nextPluginReloadRun()
	marker := "PCS-" + run + "-boot-v1"

	pcsInstall(t, f, id, "1.0.0", pcsBootScript(run, "v1"))
	pcsEnable(t, f, id, marker)
	treeA := hotUpgradeTreeDigest(t, id)

	pcsReconcile(t, f.nodeB, false)

	if !f.runtimeB.IsLoaded(id) {
		t.Errorf("node B IsLoaded(%q) = false after one reconcile, want true", id)
	}
	if treeB, ok := pcsDigest(t, f.rootB, id); !ok || treeB != treeA {
		t.Errorf("node B tree digest of %s = %q (present=%t), want node A digest %q", id, treeB, ok, treeA)
	}

	// One OnBoot on node A and one on node B: two lines, and later passes add none.
	supervisionWaitCondition(t, "node B OnBoot log line", pcsBudget, func() bool {
		return pcsLogCount(t, f.h, marker) >= 2
	})
	pcsReconcile(t, f.nodeB, false)
	pcsReconcile(t, f.nodeB, true)
	if got := pcsLogCount(t, f.h, marker); got != 2 {
		t.Errorf("OnBoot log %q count = %d, want 2 (one boot on node A, one on node B, no repeats)", marker, got)
	}
}

// 2. A disables the plugin; B's next pass unloads it.
func TestPluginClusterSyncDisableUnloadsOtherNode(t *testing.T) {
	f := newPcsFixture(t, "pcs-disable-admin")
	const id = "pcs-disable"
	run := nextPluginReloadRun()

	pcsInstall(t, f, id, "1.0.0", pcsBootScript(run, "v1"))
	pcsEnable(t, f, id, "PCS-"+run+"-boot-v1")
	pcsReconcile(t, f.nodeB, false)
	if !f.runtimeB.IsLoaded(id) {
		t.Fatalf("node B IsLoaded(%q) = false before the disable, want true", id)
	}

	if r := updCall(f.h.accessHarness, http.MethodPost, "/api/admin/plugins/"+id+"/disable", "", f.token); r.code != http.StatusOK {
		t.Fatalf("disable on node A = %d, want 200; body=%q", r.code, r.body)
	}
	pcsReconcile(t, f.nodeB, false)

	if f.runtimeB.IsLoaded(id) {
		t.Errorf("node B IsLoaded(%q) = true after node A disabled it, want false", id)
	}
}

// 3. A hot upgrades to 2.0.0; B's next pass replaces its directory and loads 2.0.0.
func TestPluginClusterSyncHotUpgradeReachesOtherNode(t *testing.T) {
	f := newPcsFixture(t, "pcs-upgrade-admin")
	const id = "pcs-upgrade"
	run := nextPluginReloadRun()

	pcsInstall(t, f, id, "1.0.0", pcsBootScript(run, "v1"))
	pcsEnable(t, f, id, "PCS-"+run+"-boot-v1")
	pcsReconcile(t, f.nodeB, false)
	treeV1 := hotUpgradeTreeDigest(t, id)

	pcsUpgrade(t, f, id, "2.0.0", pcsBootScript(run, "v2"), "PCS-"+run+"-boot-v2")
	treeV2 := hotUpgradeTreeDigest(t, id)
	if treeV2 == treeV1 {
		t.Fatalf("test premise broken: v2 tree digest equals v1 digest %q", treeV1)
	}

	pcsReconcile(t, f.nodeB, false)

	if treeB, ok := pcsDigest(t, f.rootB, id); !ok || treeB != treeV2 {
		t.Errorf("node B tree digest of %s after the upgrade = %q (present=%t), want v2 digest %q", id, treeB, ok, treeV2)
	}
	if got := pcsLoadedVersion(f.runtimeB, id); got != "2.0.0" {
		t.Errorf("node B loaded version of %s = %q, want 2.0.0", id, got)
	}
	supervisionWaitCondition(t, "node B OnBoot of v2", pcsBudget, func() bool {
		return pcsLogCount(t, f.h, "PCS-"+run+"-boot-v2") >= 2
	})
}

// 4. A hot upgrade whose new OnBoot throws is refused with 409. The plugins row
// keeps the v1 archive digest, and node B stays on v1 after its passes.
func TestPluginClusterSyncFailedHotUpgradeKeepsOldArchive(t *testing.T) {
	f := newPcsFixture(t, "pcs-failed-admin")
	const id = "pcs-failed"
	run := nextPluginReloadRun()

	pcsInstall(t, f, id, "1.0.0", pcsBootScript(run, "v1"))
	pcsEnable(t, f, id, "PCS-"+run+"-boot-v1")
	pcsReconcile(t, f.nodeB, false)
	treeV1 := hotUpgradeTreeDigest(t, id)

	v2 := hotUpgradeArchive(t, hotUpgradeManifest(id, "2.0.0", nil, nil, nil), fmt.Sprintf(`
JellyCMS.registerHook("OnBoot", function () {
  throw new Error("PCS-%s-boot-failure");
});
`, run))
	failed := hotUpgradeInstallPreview(t, f.h.accessHarness, f.token, v2)
	if failed.code != http.StatusConflict {
		t.Fatalf("hot upgrade with a failing OnBoot = %d, want 409; body=%q", failed.code, failed.body)
	}

	if got := pcsArchiveDigest(t, f.h.accessHarness, id); got != treeV1 {
		t.Errorf("plugins.archive_tree_sha256 after the failed upgrade = %q, want the v1 digest %q", got, treeV1)
	}

	pcsReconcile(t, f.nodeB, false)
	pcsReconcile(t, f.nodeB, true)

	if treeB, ok := pcsDigest(t, f.rootB, id); !ok || treeB != treeV1 {
		t.Errorf("node B tree digest of %s after the failed upgrade = %q (present=%t), want v1 digest %q", id, treeB, ok, treeV1)
	}
	if got := pcsLoadedVersion(f.runtimeB, id); got != "1.0.0" {
		t.Errorf("node B loaded version of %s after the failed upgrade = %q, want 1.0.0", id, got)
	}
}

// 5. A uploads the same version with different content; node B switches to the
// new content even though the version number did not change.
func TestPluginClusterSyncSameVersionNewContentReachesOtherNode(t *testing.T) {
	f := newPcsFixture(t, "pcs-same-admin")
	const id = "pcs-same"
	run := nextPluginReloadRun()

	pcsInstall(t, f, id, "1.0.0", pcsBootScript(run, "a"))
	pcsEnable(t, f, id, "PCS-"+run+"-boot-a")
	pcsReconcile(t, f.nodeB, false)
	treeA := hotUpgradeTreeDigest(t, id)

	pcsUpgrade(t, f, id, "1.0.0", pcsBootScript(run, "b"), "PCS-"+run+"-boot-b")
	treeNew := hotUpgradeTreeDigest(t, id)
	if treeNew == treeA {
		t.Fatalf("test premise broken: same-version content change left the tree digest at %q", treeA)
	}

	pcsReconcile(t, f.nodeB, false)

	if treeB, ok := pcsDigest(t, f.rootB, id); !ok || treeB != treeNew {
		t.Errorf("node B tree digest of %s = %q (present=%t), want the new content digest %q (old %q)", id, treeB, ok, treeNew, treeA)
	}
	supervisionWaitCondition(t, "node B OnBoot of the new content", pcsBudget, func() bool {
		return pcsLogCount(t, f.h, "PCS-"+run+"-boot-b") >= 2
	})
}

// pcsSettingsPlugin installs a plugin with one int setting, approves its
// permission and enables it on node A. Its OnBoot logs the limit it read.
func pcsSettingsPlugin(t *testing.T, f *pcsFixture, id, run string) {
	t.Helper()

	settings := []map[string]any{{"key": "limit", "type": "int", "default": 3, "min": 1, "max": 10}}
	script := fmt.Sprintf(`
JellyCMS.registerHook("OnBoot", function () {
  JellyCMS.log.info("PCS-%s-boot:" + JellyCMS.config.get("limit"));
});
`, run)
	archive := hotUpgradeArchive(t, hotUpgradeManifest(id, "1.0.0", []string{"settings"}, nil, settings), script)
	if r := hotUpgradeInstallPreview(t, f.h.accessHarness, f.token, archive); r.code != http.StatusOK {
		t.Fatalf("install settings plugin %s = %d, want 200; body=%q", id, r.code, r.body)
	}
	setGrantedPermissions(t, f.h.accessHarness, id)
	hotUpgradeEnable(t, f.h, f.token, id, "PCS-"+run+"-boot:3")
}

// 6. A settings write moves plugin_state_seq, and node B's next pass advances its
// own extension generation, so its cached extension output is invalidated.
func TestPluginClusterSyncSettingsWriteAdvancesSeqAndGeneration(t *testing.T) {
	f := newPcsFixture(t, "pcs-settings-admin")
	const id = "pcs-settings"
	run := nextPluginReloadRun()

	pcsSettingsPlugin(t, f, id, run)
	pcsReconcile(t, f.nodeB, false)
	if !f.runtimeB.IsLoaded(id) {
		t.Fatalf("node B IsLoaded(%q) = false before the settings write, want true", id)
	}

	seqBefore := pcsSeq(t, f.h.accessHarness)
	if r := updCall(f.h.accessHarness, http.MethodPut, "/api/admin/plugins/"+id+"/settings", `{"values":{"limit":7}}`, f.token); r.code != http.StatusOK {
		t.Fatalf("PUT settings on node A = %d, want 200; body=%q", r.code, r.body)
	}
	if seqAfter := pcsSeq(t, f.h.accessHarness); seqAfter <= seqBefore {
		t.Errorf("plugin_state_seq last_value after a settings write = %d, want more than %d", seqAfter, seqBefore)
	}

	genBefore := pluginExtensionGeneration()
	pcsReconcile(t, f.nodeB, false)
	if genAfter := pluginExtensionGeneration(); genAfter <= genBefore {
		t.Errorf("pluginExtensionGeneration() after node B reconciled = %d, want more than %d (node B must invalidate its extension cache)", genAfter, genBefore)
	}
}

// 7. After node A runs an operation, node A's own reconcile sees a consistent
// state and does not reload the plugin or run OnBoot again.
func TestPluginClusterSyncOwnOperationDoesNotReloadOnNodeA(t *testing.T) {
	f := newPcsFixture(t, "pcs-self-admin")
	const id = "pcs-self"
	run := nextPluginReloadRun()

	pcsSettingsPlugin(t, f, id, run)
	pcsReconcile(t, f.nodeB, false)
	bootPrefix := "PCS-" + run + "-boot:"
	supervisionWaitCondition(t, "two OnBoot lines before the operation", pcsBudget, func() bool {
		return pcsLogCount(t, f.h, bootPrefix) >= 2
	})
	before := pcsLogCount(t, f.h, bootPrefix)

	if r := updCall(f.h.accessHarness, http.MethodPut, "/api/admin/plugins/"+id+"/settings", `{"values":{"limit":5}}`, f.token); r.code != http.StatusOK {
		t.Fatalf("PUT settings on node A = %d, want 200; body=%q", r.code, r.body)
	}

	nodeA := newPluginNode(f.h.runtime, pluginDirRoot)
	pcsReconcile(t, nodeA, false)
	pcsReconcile(t, nodeA, true)

	if !f.h.runtime.IsLoaded(id) {
		t.Errorf("node A IsLoaded(%q) = false after its own reconcile, want true", id)
	}
	if after := pcsLogCount(t, f.h, bootPrefix); after != before {
		t.Errorf("OnBoot lines after node A's operation and reconcile = %d, want %d (node A must not reload or re-boot)", after, before)
	}
}

// 8. A uninstalls the plugin; node B unloads it and deletes its directory. A
// fresh runtime started on node B's directory afterwards does not load it.
func TestPluginClusterSyncUninstallRemovesOtherNodeAndStaysGone(t *testing.T) {
	f := newPcsFixture(t, "pcs-uninstall-admin")
	const id = "pcs-uninstall"
	run := nextPluginReloadRun()

	pcsInstall(t, f, id, "1.0.0", pcsBootScript(run, "v1"))
	pcsEnable(t, f, id, "PCS-"+run+"-boot-v1")
	pcsReconcile(t, f.nodeB, false)
	if !f.runtimeB.IsLoaded(id) {
		t.Fatalf("node B IsLoaded(%q) = false before the uninstall, want true", id)
	}

	if r := updCall(f.h.accessHarness, http.MethodDelete, "/api/admin/plugins/"+id+"?keep_settings=false", "", f.token); r.code != http.StatusOK && r.code != http.StatusNoContent {
		t.Fatalf("uninstall on node A = %d, want 200 or 204; body=%q", r.code, r.body)
	}
	pcsReconcile(t, f.nodeB, false)

	if f.runtimeB.IsLoaded(id) {
		t.Errorf("node B IsLoaded(%q) = true after the uninstall, want false", id)
	}
	if _, ok := pcsDigest(t, f.rootB, id); ok {
		t.Errorf("node B directory %s still exists after the uninstall, want it removed", filepath.Join(f.rootB, id))
	}

	runtimeC, err := plugin.NewRuntime()
	if err != nil {
		t.Fatalf("create fresh runtime for the restart check: %v", err)
	}
	t.Cleanup(runtimeC.Close)
	if err := newPluginNode(runtimeC, f.rootB).ReconcileAtStartup(pcsCtx(t)); err != nil {
		t.Fatalf("ReconcileAtStartup on node B's directory = %v, want nil", err)
	}
	if runtimeC.IsLoaded(id) {
		t.Errorf("fresh runtime loaded %q after the uninstall, want it gone", id)
	}
}

// 9. A plugin directory placed by hand on node B, with no plugins row and no
// removal record, is left alone by reconcile.
func TestPluginClusterSyncKeepsManualPluginDirectory(t *testing.T) {
	f := newPcsFixture(t, "pcs-manual-admin")
	const id = "pcs-manual"
	run := nextPluginReloadRun()

	dir := filepath.Join(f.rootB, id)
	writeReloadPlugin(t, dir, reloadPluginMeta(id, "1.0.0", "index.js"), pcsBootScript(run, "manual"))
	before, err := pluginTreeDigest(dir)
	if err != nil {
		t.Fatalf("hash manual plugin %s: %v", dir, err)
	}

	pcsReconcile(t, f.nodeB, false)
	pcsReconcile(t, f.nodeB, true)

	after, ok := pcsDigest(t, f.rootB, id)
	if !ok {
		t.Fatalf("node B removed the manual plugin directory %s, want it kept", dir)
	}
	if after != before {
		t.Errorf("manual plugin directory digest after reconcile = %q, want unchanged %q", after, before)
	}
}

// 10. A full reconcile on node B restores a deleted local directory from the
// database and loads the plugin again.
func TestPluginClusterSyncFullReconcileRestoresDeletedDirectory(t *testing.T) {
	f := newPcsFixture(t, "pcs-restore-admin")
	const id = "pcs-restore"
	run := nextPluginReloadRun()

	pcsInstall(t, f, id, "1.0.0", pcsBootScript(run, "v1"))
	pcsEnable(t, f, id, "PCS-"+run+"-boot-v1")
	pcsReconcile(t, f.nodeB, false)
	treeA := hotUpgradeTreeDigest(t, id)

	if err := os.RemoveAll(filepath.Join(f.rootB, id)); err != nil {
		t.Fatalf("delete node B directory for %s: %v", id, err)
	}
	pcsReconcile(t, f.nodeB, true)

	treeB, ok := pcsDigest(t, f.rootB, id)
	if !ok || treeB != treeA {
		t.Errorf("node B tree digest of %s after a full reconcile = %q (present=%t), want node A digest %q", id, treeB, ok, treeA)
	}
	if !f.runtimeB.IsLoaded(id) {
		t.Errorf("node B IsLoaded(%q) = false after a full reconcile, want true", id)
	}
}

// 11. While another node holds the install advisory lock for a plugin, node B's
// pass returns within 2 seconds and leaves the plugin alone. After the lock is
// released, the next pass applies the change even though the sequence did not move.
func TestPluginClusterSyncHeldInstallLockDefersOtherNode(t *testing.T) {
	f := newPcsFixture(t, "pcs-lock-admin")
	const id = "pcs-lock"
	run := nextPluginReloadRun()

	pcsInstall(t, f, id, "1.0.0", pcsBootScript(run, "v1"))
	pcsEnable(t, f, id, "PCS-"+run+"-boot-v1")
	pcsReconcile(t, f.nodeB, false)
	treeV1 := hotUpgradeTreeDigest(t, id)

	pcsUpgrade(t, f, id, "2.0.0", pcsBootScript(run, "v2"), "PCS-"+run+"-boot-v2")
	treeV2 := hotUpgradeTreeDigest(t, id)
	if treeV2 == treeV1 {
		t.Fatalf("test premise broken: v2 tree digest equals v1 digest %q", treeV1)
	}

	// Hold the install lock for this plugin from a second session, the way a
	// concurrent install on another server would.
	ctx := pcsCtx(t)
	sqlDB, err := f.h.db.DB()
	if err != nil {
		t.Fatalf("get database handle: %v", err)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("open lock session: %v", err)
	}
	key := pcsLockKey(id)
	unlock := func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1, $2)`, pcsAdvisoryClass, key)
	}
	t.Cleanup(func() {
		unlock()
		_ = conn.Close()
	})
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1, $2)`, pcsAdvisoryClass, key); err != nil {
		t.Fatalf("take install advisory lock for %s: %v", id, err)
	}

	start := time.Now()
	err = f.nodeB.ReconcileOnce(pcsCtx(t), true)
	elapsed := time.Since(start)
	if err != nil {
		t.Errorf("ReconcileOnce while the install lock is held = %v, want nil (the plugin is pending, not an error)", err)
	}
	if elapsed >= 2*time.Second {
		t.Errorf("ReconcileOnce while the install lock is held took %s, want it to return within 2s", elapsed)
	}
	if treeB, ok := pcsDigest(t, f.rootB, id); !ok || treeB != treeV1 {
		t.Errorf("node B tree digest of %s while locked = %q (present=%t), want unchanged v1 digest %q", id, treeB, ok, treeV1)
	}
	if got := pcsLoadedVersion(f.runtimeB, id); got != "1.0.0" {
		t.Errorf("node B loaded version of %s while locked = %q, want 1.0.0", id, got)
	}

	seqLocked := pcsSeq(t, f.h.accessHarness)
	unlock()
	if seqReleased := pcsSeq(t, f.h.accessHarness); seqReleased != seqLocked {
		t.Fatalf("test premise broken: plugin_state_seq moved from %d to %d between the passes, want it unchanged", seqLocked, seqReleased)
	}

	pcsReconcile(t, f.nodeB, false)

	if treeB, ok := pcsDigest(t, f.rootB, id); !ok || treeB != treeV2 {
		t.Errorf("node B tree digest of %s after the lock was released = %q (present=%t), want v2 digest %q", id, treeB, ok, treeV2)
	}
	if got := pcsLoadedVersion(f.runtimeB, id); got != "2.0.0" {
		t.Errorf("node B loaded version of %s after the lock was released = %q, want 2.0.0", id, got)
	}
}

// 12. Backfill: a plugin installed before ticket 37 has no archive pointer. The
// first startup reconcile stores its archive, including empty directories, and
// sets plugins.archive_tree_sha256 to the directory digest.
func TestPluginClusterSyncStartupBackfillsArchiveForLegacyPlugin(t *testing.T) {
	f := newPluginReloadHarness(t)
	const id = "pcs-backfill"

	manifest := fmt.Sprintf(`{"id":%q,"name":%q,"version":"1.0.0","entrypoint":"index.js"}`, id, id)
	plgSeed(t, f.accessHarness, id, manifest, false)
	hostWriteEntry(t, id, `JellyCMS.registerHook("OnBoot", function () {});`)
	if err := os.MkdirAll(filepath.Join(pluginDirRoot, id, "assets", "empty"), 0o755); err != nil {
		t.Fatalf("create empty directory inside %s: %v", id, err)
	}

	if got := pcsArchiveDigest(t, f.accessHarness, id); got != "" {
		t.Fatalf("test premise broken: archive pointer before backfill = %q, want NULL", got)
	}
	want, err := pluginTreeDigest(filepath.Join(pluginDirRoot, id))
	if err != nil {
		t.Fatalf("hash legacy plugin tree %s: %v", id, err)
	}

	nodeA := newPluginNode(f.runtime, pluginDirRoot)
	if err := nodeA.ReconcileAtStartup(pcsCtx(t)); err != nil {
		t.Fatalf("ReconcileAtStartup on node A = %v, want nil", err)
	}

	if got := pcsArchiveDigest(t, f.accessHarness, id); got != want {
		t.Errorf("plugins.archive_tree_sha256 after startup backfill = %q, want the directory digest %q", got, want)
	}
	if n := pcsRowsMentioning(t, f.accessHarness, "plugin_archives", id, want); n != 1 {
		t.Errorf("plugin_archives rows for %s at digest %s = %d, want 1", id, want, n)
	}
}

// 13. A plugin larger than one chunk is stored as at least three chunks, and
// node B restores a directory with the same digest. The bulk is about 2.5 MiB of
// incompressible text in assets/padding.txt, not in the entry script: the
// entry script is capped at 1 MiB (maxEntryBytes), so a 2.5 MiB entry file could
// never be loaded.
func TestPluginClusterSyncLargeArchiveIsStoredInChunks(t *testing.T) {
	f := newPcsFixture(t, "pcs-large-admin")
	const id = "pcs-large"
	run := nextPluginReloadRun()

	padding := pcsIncompressibleText(t, 2500*1024)
	raw, err := json.Marshal(hotUpgradeManifest(id, "1.0.0", nil, nil, nil))
	if err != nil {
		t.Fatalf("marshal large plugin manifest: %v", err)
	}
	archive := secZip(t,
		secZipEntry{name: "plugin.json", body: raw},
		secZipEntry{name: "main.js", body: []byte(pcsBootScript(run, "big"))},
		secZipEntry{name: "assets/padding.txt", body: []byte(padding)},
	)
	if len(archive) <= 2*pluginArchiveChunkSize {
		t.Fatalf("test premise broken: upload archive is %d bytes, want more than %d so that it spans three chunks", len(archive), 2*pluginArchiveChunkSize)
	}
	if r := hotUpgradeInstallPreview(t, f.h.accessHarness, f.token, archive); r.code != http.StatusOK {
		t.Fatalf("upload large plugin = %d, want 200; body=%q", r.code, r.body)
	}
	pcsEnable(t, f, id, "PCS-"+run+"-boot-big")
	treeA := hotUpgradeTreeDigest(t, id)

	if n := pcsRowsMentioning(t, f.h.accessHarness, "plugin_archive_chunks", id, treeA); n < 3 {
		t.Errorf("plugin_archive_chunks rows for %s = %d, want at least 3 (upload is %d bytes, chunk size %d)", id, n, len(archive), pluginArchiveChunkSize)
	}

	pcsReconcile(t, f.nodeB, false)

	if treeB, ok := pcsDigest(t, f.rootB, id); !ok || treeB != treeA {
		t.Errorf("node B tree digest of large plugin %s = %q (present=%t), want node A digest %q", id, treeB, ok, treeA)
	}
}

// 14. A stored archive with one altered byte in a chunk fails to open. The
// function returns no path and leaves no temporary file behind.
func TestPluginClusterSyncTamperedArchiveChunkIsRejected(t *testing.T) {
	f := newPluginReloadHarness(t)
	h := f.accessHarness
	const id = "pcs-tamper"
	treeSHA := strings.Repeat("ab", 32)

	zipPath := filepath.Join(t.TempDir(), "plugin.zip")
	archive := hotUpgradeArchive(t, hotUpgradeManifest(id, "1.0.0", nil, nil, nil), `JellyCMS.registerHook("OnBoot", function () {});`)
	if err := os.WriteFile(zipPath, archive, 0o600); err != nil {
		t.Fatalf("write archive fixture: %v", err)
	}
	archiveSum := sha256.Sum256(archive)
	wantSHA := hex.EncodeToString(archiveSum[:])

	ctx := pcsCtx(t)
	zipSHA, err := storePluginArchive(ctx, id, treeSHA, zipPath)
	if err != nil {
		t.Fatalf("storePluginArchive = %v, want nil", err)
	}
	if zipSHA != wantSHA {
		t.Errorf("storePluginArchive SHA-256 = %q, want %q", zipSHA, wantSHA)
	}

	okDir := t.TempDir()
	t.Setenv("TMPDIR", okDir)
	path, err := openPluginArchive(ctx, id, treeSHA)
	if err != nil {
		t.Fatalf("openPluginArchive of an intact archive = %v, want nil", err)
	}
	if got := pcsSHA256Hex(t, path); got != wantSHA {
		t.Errorf("openPluginArchive assembled SHA-256 = %q, want %q", got, wantSHA)
	}

	var columns []struct {
		Name string `gorm:"column:column_name"`
	}
	if err := h.db.Raw(`SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'plugin_archive_chunks' AND data_type = 'bytea'`).Scan(&columns).Error; err != nil {
		t.Fatalf("read plugin_archive_chunks bytea column: %v", err)
	}
	if len(columns) != 1 {
		t.Fatalf("plugin_archive_chunks bytea columns = %d, want 1 (the chunk bytes)", len(columns))
	}
	col := columns[0].Name
	tamper := fmt.Sprintf(`UPDATE plugin_archive_chunks SET "%[1]s" = set_byte("%[1]s", 0, (get_byte("%[1]s", 0) + 1) %% 256) WHERE ctid = (SELECT ctid FROM plugin_archive_chunks LIMIT 1)`, col)
	if err := h.db.Exec(tamper).Error; err != nil {
		t.Fatalf("alter one stored chunk byte: %v", err)
	}

	badDir := t.TempDir()
	t.Setenv("TMPDIR", badDir)
	path, err = openPluginArchive(ctx, id, treeSHA)
	if err == nil {
		t.Errorf("openPluginArchive of a tampered archive returned %q with nil error, want an error", path)
	}
	if path != "" {
		t.Errorf("openPluginArchive of a tampered archive returned path %q, want empty", path)
	}
	if entries, readErr := os.ReadDir(badDir); readErr != nil {
		t.Fatalf("read temporary directory: %v", readErr)
	} else if len(entries) != 0 {
		t.Errorf("openPluginArchive left %d file(s) in the temporary directory after a checksum failure, want none", len(entries))
	}
}

// 15. The background loop: with a 100ms poll interval, node B's Run loads a
// plugin that node A enabled within 3 seconds, without any explicit reconcile.
func TestPluginClusterSyncRunLoadsWithinPollWindow(t *testing.T) {
	f := newPcsFixture(t, "pcs-run-admin")
	const id = "pcs-run"
	run := nextPluginReloadRun()

	previous := pluginSyncPollInterval
	pluginSyncPollInterval = 100 * time.Millisecond
	t.Cleanup(func() { pluginSyncPollInterval = previous })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.nodeB.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	pcsInstall(t, f, id, "1.0.0", pcsBootScript(run, "v1"))
	pcsEnable(t, f, id, "PCS-"+run+"-boot-v1")

	supervisionWaitCondition(t, "node B Run loads the plugin enabled on node A", 3*time.Second, func() bool {
		return f.runtimeB.IsLoaded(id)
	})
	treeA := hotUpgradeTreeDigest(t, id)
	if treeB, ok := pcsDigest(t, f.rootB, id); !ok || treeB != treeA {
		t.Errorf("node B tree digest of %s after Run loaded it = %q (present=%t), want node A digest %q", id, treeB, ok, treeA)
	}
}
