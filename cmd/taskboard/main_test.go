package main

import "testing"

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
