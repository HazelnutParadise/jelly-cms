package update

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/version"
)

// Probe is everything runtime detection looks at, injected so it can be tested
// without touching the real filesystem or environment.
type Probe struct {
	// Env holds the environment variables detection may consult.
	Env map[string]string
	// Files maps a path that exists to its content ("" for bare markers such
	// as /.dockerenv).
	Files map[string]string
	// Executable is the path os.Executable reports.
	Executable string
	// ExecDirWritable reports whether the executable's directory accepts writes.
	ExecDirWritable bool
}

// packagePrefixes are install locations owned by a package manager. A binary
// there must be upgraded through that manager, not replaced in place.
var packagePrefixes = []string{
	"/opt/homebrew/",
	"/usr/local/Cellar/",
	"/usr/local/Homebrew/",
	"/home/linuxbrew/.linuxbrew/",
}

// DetectRuntime classifies the process from the probe: JELLY_RUNTIME wins,
// then Kubernetes and container markers, then package-manager prefixes, else a
// standalone binary. Writable is true only for a standalone binary whose
// directory is writable.
func DetectRuntime(p Probe) version.RuntimeInfo {
	if kind := strings.ToLower(strings.TrimSpace(p.Env["JELLY_RUNTIME"])); isKnownKind(kind) {
		return info(kind, "env:JELLY_RUNTIME", p)
	}
	cgroup := p.Files["/proc/1/cgroup"]
	// Kubernetes first: its pods also carry the Docker markers below.
	if p.Env["KUBERNETES_SERVICE_HOST"] != "" {
		return info(version.KindKubernetes, "env:KUBERNETES_SERVICE_HOST", p)
	}
	if strings.Contains(cgroup, "kubepods") {
		return info(version.KindKubernetes, "file:/proc/1/cgroup (kubepods)", p)
	}
	if _, ok := p.Files["/.dockerenv"]; ok {
		return info(version.KindContainer, "file:/.dockerenv", p)
	}
	if strings.Contains(cgroup, "docker") {
		return info(version.KindContainer, "file:/proc/1/cgroup (docker)", p)
	}
	if strings.Contains(p.Executable, "/Cellar/") {
		return info(version.KindPackage, "path:/Cellar/", p)
	}
	for _, prefix := range packagePrefixes {
		if strings.HasPrefix(p.Executable, prefix) {
			return info(version.KindPackage, "path:"+prefix, p)
		}
	}
	return info(version.KindBinary, "default", p)
}

func isKnownKind(kind string) bool {
	switch kind {
	case version.KindContainer, version.KindKubernetes, version.KindPackage, version.KindBinary:
		return true
	}
	return false
}

// info builds the result for kind. Only a standalone binary in a writable
// directory may replace itself.
func info(kind, detectedBy string, p Probe) version.RuntimeInfo {
	return version.RuntimeInfo{
		Kind:       kind,
		DetectedBy: detectedBy,
		Writable:   kind == version.KindBinary && p.ExecDirWritable,
	}
}

func init() {
	version.RuntimeDetector = func() version.RuntimeInfo {
		return DetectRuntime(hostProbe())
	}
}

// hostProbe reads the real process state for DetectRuntime.
func hostProbe() Probe {
	p := Probe{Env: map[string]string{}, Files: map[string]string{}}
	for _, key := range []string{"JELLY_RUNTIME", "KUBERNETES_SERVICE_HOST"} {
		if v, ok := os.LookupEnv(key); ok {
			p.Env[key] = v
		}
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		p.Files["/.dockerenv"] = ""
	}
	if b, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		p.Files["/proc/1/cgroup"] = string(b)
	}
	if exe, err := os.Executable(); err == nil {
		p.Executable = exe
		p.ExecDirWritable = dirWritable(filepath.Dir(exe))
	}
	return p
}

// dirWritable reports whether a file can be created in dir. It creates and
// removes a short-lived probe file, which is the portable check.
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".jelly-write-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}
