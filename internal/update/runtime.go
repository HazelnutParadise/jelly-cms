package update

import "github.com/HazelnutParadise/jelly-cms/internal/version"

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

// DetectRuntime classifies the process from the probe: JELLY_RUNTIME wins,
// then container and Kubernetes markers, then package-manager prefixes, else a
// standalone binary. Writable is true only for a standalone binary whose
// directory is writable.
//
// TODO(ticket 08): implement; version.Runtime should build the real Probe and
// call this.
func DetectRuntime(p Probe) version.RuntimeInfo {
	return version.RuntimeInfo{}
}
