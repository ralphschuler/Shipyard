// Package buildmeta exposes the Go toolchain that compiled the running binary.
package buildmeta

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// GoVersion returns the toolchain that compiled this binary. debug.ReadBuildInfo
// is the source of truth used by `go version -m`; runtime.Version is the fallback
// when build info has been stripped.
func GoVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if version := strings.TrimSpace(info.GoVersion); version != "" {
			return version
		}
	}
	return runtime.Version()
}
