package web

import (
	"testing"
	"time"
)

func TestAuditPresentationUsesActionAndOutcome(t *testing.T) {
	if got := auditAction("control_panel.post", "http", "/runs/id/discard"); got != "Worktree verworfen" {
		t.Fatalf("unexpected action label: %q", got)
	}
	if got := auditAction("mcp.create_task", "mcp_tool", "create_task"); got != "MCP · create_task" {
		t.Fatalf("unexpected MCP label: %q", got)
	}
	if got := auditStatus(`{"status":"400"}`); got != "400" {
		t.Fatalf("unexpected audit status: %q", got)
	}
	if got := auditStatusClass(`{"status":"400"}`); got != "status-failed" {
		t.Fatalf("unexpected failure class: %q", got)
	}
	if got := auditStatusClass(`{"status":"ok"}`); got != "status-succeeded" {
		t.Fatalf("unexpected success class: %q", got)
	}
}

func TestListCursorRoundTripAndRejectsMalformedValues(t *testing.T) {
	created := time.Date(2026, time.September, 16, 8, 30, 1, 123, time.FixedZone("CET", 2*60*60))
	cursor := encodeListCursor(created, "run-123")
	parsed, id, isOlderPage, err := listCursor(cursor)
	if err != nil || !isOlderPage || id != "run-123" || !parsed.Equal(created.UTC()) {
		t.Fatalf("cursor round trip = %v %q %v %v", parsed, id, isOlderPage, err)
	}
	if _, _, _, err := listCursor("not-a-cursor"); err == nil {
		t.Fatal("malformed cursor was accepted")
	}
}

func TestExcerptNormalizesWhitespaceWithoutBreakingRunes(t *testing.T) {
	if got := excerpt("  Ändert\n  Dateien   sicher ", 40); got != "Ändert Dateien sicher" {
		t.Fatalf("normalized excerpt = %q", got)
	}
	if got := excerpt("ÄÖÜabcdef", 3); got != "ÄÖÜ …" {
		t.Fatalf("rune excerpt = %q", got)
	}
}
