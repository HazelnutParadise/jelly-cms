package server

// Regression contract for the ticket 37 review fixes on multi-node plugin sync.
// Each test pins one defect the review found in plugin_cluster_sync.go and the
// plugin filter, so the fix can be checked against the same scenario:
//
//  1. The first change made after plugin_state_seq is reset must be seen. The
//     first nextval leaves last_value at 1, the same value a fresh sequence has.
//  2. A plugin whose entry throws on load is not loaded again on every pass; it
//     is retried only after its archive pointer or the sequence changes.
//  3. PluginEnabledFilter skips a plugin whose plugins row was removed while its
//     directory is still on disk.
//  4. A pass that fails to write a plugin's files, without an install-lock
//     conflict, is retried on the next poll even when the sequence did not move.
//
// This file is the ticket 37 review contract. Implementers must not edit it;
// make plugin_cluster_sync.go, plugin_detail_api.go and the install paths
// satisfy it instead.
//
// Every test needs JELLY_TEST_POSTGRES=1 and the dedicated PostgreSQL container.
// Waits poll with a bounded loop; a fixed sleep is never followed directly by an
// assertion.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
)

// csrSeqState reads last_value and is_called of plugin_state_seq.
func csrSeqState(t *testing.T, h *accessHarness) (lastValue int64, called bool) {
	t.Helper()

	if err := h.db.Raw(`SELECT last_value, is_called FROM plugin_state_seq`).Row().Scan(&lastValue, &called); err != nil {
		t.Fatalf("read plugin_state_seq last_value and is_called: %v", err)
	}
	return lastValue, called
}

// csrBumpSeq advances plugin_state_seq once, the way a state change does.
func csrBumpSeq(t *testing.T, h *accessHarness) {
	t.Helper()

	var value int64
	if err := h.db.Raw(`SELECT nextval('plugin_state_seq')`).Row().Scan(&value); err != nil {
		t.Fatalf("advance plugin_state_seq: %v", err)
	}
}

// csrStagedArchive packs a plugin archive and stages it the way an install does.
// It returns the zip path and the tree digest the staged files reproduce.
func csrStagedArchive(t *testing.T, id, version, script string) (string, string) {
	t.Helper()

	archive := hotUpgradeArchive(t, hotUpgradeManifest(id, version, nil, nil, nil), script)
	zipPath := filepath.Join(t.TempDir(), "plugin.zip")
	if err := os.WriteFile(zipPath, archive, 0o600); err != nil {
		t.Fatalf("write archive fixture for %s: %v", id, err)
	}
	staged, err := stagePluginArchive(zipPath, t.TempDir(), plugin.ArchiveExpectation{ID: id, Version: version})
	if err != nil {
		t.Fatalf("stage archive fixture for %s: %v", id, err)
	}
	digest, err := pluginTreeDigest(staged.Stage)
	staged.Cleanup()
	if err != nil {
		t.Fatalf("hash staged archive fixture for %s: %v", id, err)
	}
	return zipPath, digest
}

// 1. A fresh sequence is reset with RESTART (is_called = false, last_value = 1).
// Node B aligns once, then A enables the plugin. That enable is the first nextval
// after the reset, which leaves last_value at 1, so node B must still see it.
func TestPluginClusterSyncFirstChangeOnFreshSequenceIsSeen(t *testing.T) {
	f := newPcsFixture(t, "csr-first-admin")
	const id = "csr-first"
	run := nextPluginReloadRun()

	// The install runs before the reset: its own nextval would otherwise be the
	// first change after the reset and hide the case under test.
	pcsInstall(t, f, id, "1.0.0", pcsBootScript(run, "v1"))
	if err := f.h.db.Exec(`ALTER SEQUENCE plugin_state_seq RESTART`).Error; err != nil {
		t.Fatalf("reset plugin_state_seq: %v", err)
	}
	if last, called := csrSeqState(t, f.h.accessHarness); called || last != 1 {
		t.Fatalf("test premise broken: plugin_state_seq after RESTART = last_value %d is_called %t, want last_value 1 is_called false", last, called)
	}

	pcsReconcile(t, f.nodeB, false)

	pcsEnable(t, f, id, "PCS-"+run+"-boot-v1")
	if last, called := csrSeqState(t, f.h.accessHarness); !called || last != 1 {
		t.Fatalf("test premise broken: plugin_state_seq after the enable = last_value %d is_called %t, want last_value 1 is_called true (the enable must be the first nextval after the reset)", last, called)
	}

	pcsReconcile(t, f.nodeB, false)

	if !f.runtimeB.IsLoaded(id) {
		last, called := csrSeqState(t, f.h.accessHarness)
		t.Errorf("node B IsLoaded(%q) = false after its pass that follows the first change on the reset sequence (plugin_state_seq last_value %d is_called %t), want true", id, last, called)
	}
	treeA := hotUpgradeTreeDigest(t, id)
	if treeB, ok := pcsDigest(t, f.rootB, id); !ok || treeB != treeA {
		t.Errorf("node B tree digest of %s = %q (present=%t), want node A digest %q", id, treeB, ok, treeA)
	}
}

// 2. The entry of a plugin throws on load. Node B tries to load it once and then
// does not try again on later passes. The archive pointer then changes to a
// working archive and the sequence moves; the next pass loads it.
func TestPluginClusterSyncFailedLoadIsNotRetriedUntilTargetChanges(t *testing.T) {
	f := newPcsFixture(t, "csr-failed-load-admin")
	const id = "csr-failed-load"
	run := nextPluginReloadRun()
	h := f.h.accessHarness

	// The top-level log line runs on every load attempt, so it counts attempts.
	badMarker := "CSR-" + run + "-entry-bad"
	badScript := fmt.Sprintf("JellyCMS.log.info(%q);\nthrow new Error(%q);\n", badMarker, "CSR-"+run+"-throw")
	badZip, badTree := csrStagedArchive(t, id, "1.0.0", badScript)
	goodZip, goodTree := csrStagedArchive(t, id, "1.0.0", pcsBootScript(run, "good"))
	if badTree == goodTree {
		t.Fatalf("test premise broken: bad and good archives have the same tree digest %q", badTree)
	}

	plgSeed(t, h, id, fmt.Sprintf(`{"id":%q,"name":%q,"version":"1.0.0","entrypoint":"main.js"}`, id, id), true)
	if _, err := storePluginArchive(pcsCtx(t), id, badTree, badZip); err != nil {
		t.Fatalf("store the failing archive for %s: %v", id, err)
	}
	if err := h.db.Exec(`UPDATE plugins SET archive_tree_sha256 = ? WHERE id = ?`, badTree, id).Error; err != nil {
		t.Fatalf("point plugins.archive_tree_sha256 of %s at the failing archive: %v", id, err)
	}
	// The sequence is still fresh here. Advance it once so that the change made
	// later is a move past the value node B already saw; the fresh-sequence case
	// itself is pinned by TestPluginClusterSyncFirstChangeOnFreshSequenceIsSeen.
	csrBumpSeq(t, h)

	for pass := 1; pass <= 3; pass++ {
		// A failed load is logged and the pass result is not asserted here.
		if err := f.nodeB.ReconcileOnce(pcsCtx(t), true); err != nil {
			t.Logf("node B full pass %d returned: %v", pass, err)
		}
		if pass == 1 {
			supervisionWaitCondition(t, "the failing entry runs on node B's first load attempt", pcsBudget, func() bool {
				return pcsLogCount(t, f.h, badMarker) >= 1
			})
		}
	}
	if got := pcsLogCount(t, f.h, badMarker); got != 1 {
		t.Errorf("failing entry runs after three full passes on node B = %d, want 1 (a failed load is not retried until the target changes)", got)
	}
	if f.runtimeB.IsLoaded(id) {
		t.Errorf("node B IsLoaded(%q) = true with the failing archive, want false", id)
	}

	csrStoreAndPoint(t, f, id, goodTree, goodZip)
	csrBumpSeq(t, h)

	pcsReconcile(t, f.nodeB, false)

	if !f.runtimeB.IsLoaded(id) {
		t.Errorf("node B IsLoaded(%q) = false after the archive pointer moved to a working archive, want true", id)
	}
	if treeB, ok := pcsDigest(t, f.rootB, id); !ok || treeB != goodTree {
		t.Errorf("node B tree digest of %s after the pointer moved = %q (present=%t), want the working archive digest %q", id, treeB, ok, goodTree)
	}
	supervisionWaitCondition(t, "node B boot of the working archive", pcsBudget, func() bool {
		return pcsLogCount(t, f.h, "PCS-"+run+"-boot-good") >= 1
	})
}

// csrStoreAndPoint stores a plugin archive and points plugins.archive_tree_sha256
// at it.
func csrStoreAndPoint(t *testing.T, f *pcsFixture, id, digest, zipPath string) {
	t.Helper()

	if _, err := storePluginArchive(pcsCtx(t), id, digest, zipPath); err != nil {
		t.Fatalf("store archive %s for %s: %v", digest, id, err)
	}
	if err := f.h.db.Exec(`UPDATE plugins SET archive_tree_sha256 = ? WHERE id = ?`, digest, id).Error; err != nil {
		t.Fatalf("point plugins.archive_tree_sha256 of %s at %s: %v", id, digest, err)
	}
}

// 3. A plugin whose plugins row was removed still has its directory on disk.
// PluginEnabledFilter must skip it, because plugin_removals records the removal.
// A directory with neither a row nor a removal record is a manual install and
// stays loadable.
func TestPluginEnabledFilterSkipsRemovedPlugins(t *testing.T) {
	h := newPluginReloadHarness(t)
	const removedID = "csr-removed"
	const manualID = "csr-manual"

	for _, id := range []string{removedID, manualID} {
		if err := os.MkdirAll(filepath.Join(pluginDirRoot, id), 0o755); err != nil {
			t.Fatalf("create plugin directory for %s: %v", id, err)
		}
	}
	if err := h.db.Exec(`INSERT INTO plugin_removals (plugin_id) VALUES (?)`, removedID).Error; err != nil {
		t.Fatalf("record removal of %s: %v", removedID, err)
	}

	include := PluginEnabledFilter()
	if got := include(removedID); got {
		t.Errorf("PluginEnabledFilter()(%q) = %t, want false (plugin_removals records it and plugins has no row)", removedID, got)
	}
	if got := include(manualID); !got {
		t.Errorf("PluginEnabledFilter()(%q) = %t, want true (a directory with no row and no removal record is a manual install)", manualID, got)
	}
}

// 4. Node B cannot write a plugin's files, so its pass fails without an install
// lock conflict. The sequence does not move afterwards. Once the directory is
// writable again, the next poll must finish the change without a full reconcile.
func TestPluginClusterSyncNonLockFailureRetriedOnNextPoll(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop root from writing; run this test as a regular user")
	}
	f := newPcsFixture(t, "csr-nonlock-admin")
	const id = "csr-nonlock"
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

	if err := os.Chmod(f.rootB, 0o500); err != nil {
		t.Fatalf("make node B plugin directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.rootB, 0o700) })

	failErr := f.nodeB.ReconcileOnce(pcsCtx(t), false)
	t.Logf("node B pass with a read-only plugin directory returned: %v", failErr)
	if treeB, ok := pcsDigest(t, f.rootB, id); !ok || treeB != treeV1 {
		t.Fatalf("test premise broken: node B tree digest of %s with a read-only directory = %q (present=%t), want unchanged v1 digest %q", id, treeB, ok, treeV1)
	}
	seqFailed, _ := csrSeqState(t, f.h.accessHarness)

	if err := os.Chmod(f.rootB, 0o700); err != nil {
		t.Fatalf("restore node B plugin directory permissions: %v", err)
	}
	if seqRetry, _ := csrSeqState(t, f.h.accessHarness); seqRetry != seqFailed {
		t.Fatalf("test premise broken: plugin_state_seq moved from %d to %d between the failed pass and the retry, want it unchanged", seqFailed, seqRetry)
	}

	pcsReconcile(t, f.nodeB, false)

	if got := pcsLoadedVersion(f.runtimeB, id); got != "2.0.0" {
		t.Errorf("node B loaded version of %s after the next poll = %q, want 2.0.0 (the failed plugin must be retried without a sequence change)", id, got)
	}
	if treeB, ok := pcsDigest(t, f.rootB, id); !ok || treeB != treeV2 {
		t.Errorf("node B tree digest of %s after the next poll = %q (present=%t), want v2 digest %q", id, treeB, ok, treeV2)
	}
}
