// Package version reports the build identity and how the binary is running.
//
// Version, Commit and BuiltAt are injected at build time:
//
//	go build -ldflags "-X github.com/HazelnutParadise/jelly-cms/internal/version.Version=1.2.3 \
//	  -X github.com/HazelnutParadise/jelly-cms/internal/version.Commit=abc1234 \
//	  -X github.com/HazelnutParadise/jelly-cms/internal/version.BuiltAt=2026-01-01T00:00:00Z"
package version

import (
	"os"
	"strings"
)

// Set by -ldflags at build time. The defaults mark an unstamped development build.
var (
	Version = "dev"
	Commit  = "unknown"
	BuiltAt = ""
)

// Runtime kinds reported by Runtime. The update page uses them to decide
// whether the binary may replace itself or only show upgrade instructions.
const (
	KindContainer  = "container"
	KindKubernetes = "kubernetes"
	KindPackage    = "package"
	KindBinary     = "binary"
)

// RuntimeInfo describes how the running process was started.
type RuntimeInfo struct {
	Kind       string
	DetectedBy string
	// Writable reports whether the executable may be replaced in place.
	Writable bool
}

// RuntimeDetector classifies the running process. internal/update sets it in
// its init, because update imports version and version cannot import update
// back. Until update is linked in, Runtime reports a non-writable binary.
var RuntimeDetector func() RuntimeInfo

// Runtime returns how the process runs. It delegates to RuntimeDetector when
// that is registered. Otherwise only JELLY_RUNTIME is honored, and the result
// is a non-writable binary so the binary never claims it can self-replace
// without detection.
func Runtime() RuntimeInfo {
	if RuntimeDetector != nil {
		return RuntimeDetector()
	}
	switch kind := strings.ToLower(strings.TrimSpace(os.Getenv("JELLY_RUNTIME"))); kind {
	case KindContainer, KindKubernetes, KindPackage, KindBinary:
		return RuntimeInfo{Kind: kind, DetectedBy: "env:JELLY_RUNTIME", Writable: false}
	}
	return RuntimeInfo{Kind: KindBinary, DetectedBy: "fallback:update-not-linked", Writable: false}
}
