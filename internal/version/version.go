// Package version carries build metadata injected with -ldflags.
package version

import "runtime"

var (
	// Version is the release tag, or "dev" for a local build.
	Version = "dev"
	// Commit is the short git SHA.
	Commit = "none"
	// BuildDate is the UTC build timestamp.
	BuildDate = "unknown"
)

// Go reports the toolchain that produced the binary.
func Go() string { return runtime.Version() }
