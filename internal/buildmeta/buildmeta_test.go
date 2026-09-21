package buildmeta

import (
	"strings"
	"testing"
)

func TestGoVersionReportsAGoToolchain(t *testing.T) {
	version := GoVersion()
	if !strings.HasPrefix(version, "go1.") {
		t.Fatalf("GoVersion() = %q, want a go1.x toolchain identifier", version)
	}
}
