package plugin

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func writeLayoutArchive(t *testing.T, entries map[string]string, order []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugin.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for _, name := range order {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entries[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

const layoutManifest = `{"id":"layout-probe","name":"Layout Probe","version":"1.0.0"}`

func TestArchiveLayoutPreservesNestedRootAndWrapperLayouts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries map[string]string
		order   []string
	}{
		{name: "root", entries: map[string]string{"plugin.json": layoutManifest, "assets/admin/panel.js": "root-js", "assets/icons/mark.svg": "root-svg"}, order: []string{"plugin.json", "assets/admin/panel.js", "assets/icons/mark.svg"}},
		{name: "one wrapper", entries: map[string]string{"bundle/plugin.json": layoutManifest, "bundle/assets/admin/panel.js": "wrap-js", "bundle/assets/icons/mark.svg": "wrap-svg"}, order: []string{"bundle/plugin.json", "bundle/assets/admin/panel.js", "bundle/assets/icons/mark.svg"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := writeLayoutArchive(t, tc.entries, tc.order)
			root := t.TempDir()
			id, err := InstallArchive(archive, root)
			if err != nil {
				t.Fatalf("InstallArchive: %v", err)
			}
			if id != "layout-probe" {
				t.Fatalf("installed id = %q", id)
			}
			for rel, want := range map[string]string{"assets/admin/panel.js": "root-js", "assets/icons/mark.svg": "root-svg"} {
				if tc.name == "one wrapper" {
					if rel == "assets/admin/panel.js" {
						want = "wrap-js"
					} else {
						want = "wrap-svg"
					}
				}
				got, err := os.ReadFile(filepath.Join(root, id, filepath.FromSlash(rel)))
				if err != nil || string(got) != want {
					t.Errorf("asset %s = %q, err %v; want %q", rel, got, err, want)
				}
			}
		})
	}
}

func TestArchiveLayoutRejectsAmbiguousManifestLayouts(t *testing.T) {
	cases := []struct {
		name    string
		entries map[string]string
		order   []string
	}{
		{name: "duplicate root manifest", entries: map[string]string{"plugin.json": layoutManifest, "./plugin.json": layoutManifest}, order: []string{"plugin.json", "./plugin.json"}},
		{name: "mixed root and wrapper manifests", entries: map[string]string{"plugin.json": layoutManifest, "bundle/plugin.json": layoutManifest}, order: []string{"plugin.json", "bundle/plugin.json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			archive := writeLayoutArchive(t, tc.entries, tc.order)
			if _, err := InstallArchive(archive, t.TempDir()); err == nil {
				t.Fatal("InstallArchive accepted ambiguous plugin manifests")
			}
		})
	}
}

func TestArchiveLayoutRejectedReplacementPreservesExistingPlugin(t *testing.T) {
	root := t.TempDir()
	old := writeLayoutArchive(t, map[string]string{"plugin.json": layoutManifest, "main.js": "known-good"}, []string{"plugin.json", "main.js"})
	if _, err := InstallArchive(old, root); err != nil {
		t.Fatalf("install baseline plugin: %v", err)
	}
	bad := writeLayoutArchive(t, map[string]string{"plugin.json": layoutManifest, "../escape.txt": "outside"}, []string{"plugin.json", "../escape.txt"})
	if _, err := InstallArchive(bad, root); err == nil {
		t.Fatal("InstallArchive accepted archive with traversal entry")
	}
	for rel, want := range map[string]string{"plugin.json": layoutManifest, "main.js": "known-good"} {
		got, err := os.ReadFile(filepath.Join(root, "layout-probe", rel))
		if err != nil || string(got) != want {
			t.Errorf("existing plugin file %s after rejected archive = %q, err %v; want %q", rel, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); !os.IsNotExist(err) {
		t.Errorf("rejected archive wrote outside plugin directory: stat error %v", err)
	}
}
