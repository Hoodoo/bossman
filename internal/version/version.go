// Package version holds the build version, set with -ldflags at build time.
// A plain "go install …@<v>" sets no ldflags; init then falls back to the
// module version Go records in the binary.
package version

import "runtime/debug"

var Version = "0.0.0-dev"

func init() {
	if Version != "0.0.0-dev" {
		return
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		Version = bi.Main.Version
	}
}
