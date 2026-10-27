package update

import (
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/version"
)

// TestRuntimeDetectionTable drives DetectRuntime through injected probes. The
// skeleton's version.Runtime() takes no probe, so this test assumes:
//
//	type Probe struct {
//		Env            map[string]string // environment variables
//		Files          map[string]string // paths that exist (cgroup content or "" for markers)
//		Executable     string            // os.Executable()
//		ExecDirWritable bool             // the executable's directory is writable
//	}
//	func DetectRuntime(p Probe) version.RuntimeInfo
func TestRuntimeDetectionTable(t *testing.T) {
	cases := []struct {
		name         string
		probe        Probe
		wantKind     string
		wantWritable bool
	}{
		{
			name:         "dockerenv marker means container",
			probe:        Probe{Files: map[string]string{"/.dockerenv": ""}, Executable: "/srv/jelly/jelly-cms", ExecDirWritable: true},
			wantKind:     version.KindContainer,
			wantWritable: false,
		},
		{
			name:         "cgroup mentions docker means container",
			probe:        Probe{Files: map[string]string{"/proc/1/cgroup": "0::/docker/3f2a9c"}, Executable: "/srv/jelly/jelly-cms", ExecDirWritable: true},
			wantKind:     version.KindContainer,
			wantWritable: false,
		},
		{
			name:         "cgroup mentions kubepods means kubernetes",
			probe:        Probe{Files: map[string]string{"/proc/1/cgroup": "0::/kubepods/besteffort/pod1"}, Executable: "/srv/jelly/jelly-cms"},
			wantKind:     version.KindKubernetes,
			wantWritable: false,
		},
		{
			name:         "KUBERNETES_SERVICE_HOST means kubernetes",
			probe:        Probe{Env: map[string]string{"KUBERNETES_SERVICE_HOST": "10.96.0.1"}, Executable: "/srv/jelly/jelly-cms", ExecDirWritable: true},
			wantKind:     version.KindKubernetes,
			wantWritable: false,
		},
		{
			name:         "Homebrew prefix means package",
			probe:        Probe{Executable: "/opt/homebrew/bin/jelly-cms", ExecDirWritable: true},
			wantKind:     version.KindPackage,
			wantWritable: false,
		},
		{
			name:         "Cellar path means package",
			probe:        Probe{Executable: "/usr/local/Cellar/jelly-cms/1.2.0/bin/jelly-cms", ExecDirWritable: true},
			wantKind:     version.KindPackage,
			wantWritable: false,
		},
		{
			name:         "writable executable directory means binary",
			probe:        Probe{Executable: "/home/app/bin/jelly-cms", ExecDirWritable: true},
			wantKind:     version.KindBinary,
			wantWritable: true,
		},
		{
			name:         "read-only executable directory means binary that cannot self-replace",
			probe:        Probe{Executable: "/usr/bin/jelly-cms"},
			wantKind:     version.KindBinary,
			wantWritable: false,
		},
		{
			name: "JELLY_RUNTIME overrides every other signal",
			probe: Probe{
				Env:             map[string]string{"JELLY_RUNTIME": "container", "KUBERNETES_SERVICE_HOST": "10.96.0.1"},
				Executable:      "/home/app/bin/jelly-cms",
				ExecDirWritable: true,
			},
			wantKind:     version.KindContainer,
			wantWritable: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectRuntime(tc.probe)
			if got.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q (DetectedBy=%q)", got.Kind, tc.wantKind, got.DetectedBy)
			}
			if got.Writable != tc.wantWritable {
				t.Errorf("Writable = %v, want %v", got.Writable, tc.wantWritable)
			}
		})
	}
}
