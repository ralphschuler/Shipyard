package main

import (
	"os"
	"testing"
)

func TestSetBuildMetadataExposesTheCompiledGoToolchain(t *testing.T) {
	t.Setenv("TASKBOARD_VERSION", "")
	t.Setenv("TASKBOARD_COMMIT_SHA", "")
	t.Setenv("TASKBOARD_BUILD_TIME", "")
	t.Setenv("TASKBOARD_GO_VERSION", "")
	previous := goversion
	goversion = "go1.26.8"
	t.Cleanup(func() { goversion = previous })
	setBuildMetadata()
	if got := os.Getenv("TASKBOARD_GO_VERSION"); got != "go1.26.8" {
		t.Fatalf("TASKBOARD_GO_VERSION = %q, want the compiled toolchain", got)
	}
}

func TestStreamingPathsBypassTheOrdinaryRequestTimeout(t *testing.T) {
	for _, path := range []string{"/events", "/mcp"} {
		if !isStreamingPath(path) {
			t.Fatalf("%s must be treated as streaming", path)
		}
	}
	for _, path := range []string{"/", "/runs", "/mcp/other", "/events/other"} {
		if isStreamingPath(path) {
			t.Fatalf("%s must retain the normal request timeout", path)
		}
	}
}
