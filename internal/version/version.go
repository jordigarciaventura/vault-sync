// Package version exposes build metadata injected by GoReleaser via -ldflags.
package version

import "runtime/debug"

// Set at build time with -ldflags "-X .../internal/version.Version=v1.2.3 ...".
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Current returns the running version, falling back to module build info for
// binaries installed with `go install`.
func Current() string {
	if Version != "dev" && Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
