package store

import (
	"context"
	"testing"
)

func TestDashboardFilteredUnboundedIncludesTelemetrySeries(t *testing.T) {
	s := integrationStore(t)

	dashboard, err := s.DashboardFiltered(context.Background(), nil, nil, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(dashboard.TelemetrySeries) == 0 {
		t.Fatal("unbounded dashboard omitted telemetry series")
	}
}

func TestDashboardFilteredUnboundedIncludesTasksWithoutAgentRuns(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	board, err := s.CreateBoardWithTemplate(ctx, "Dashboard task population", "software")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	columns, err := s.Columns(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) == 0 {
		t.Fatal("dashboard test board has no workflow columns")
	}
	before, err := s.DashboardFiltered(ctx, nil, nil, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, board.ID, "Task without agent run", "", "normal", "", "", "mcp"); err != nil {
		t.Fatal(err)
	}

	dashboard, err := s.DashboardFiltered(ctx, nil, nil, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if dashboard.Total != before.Total+1 {
		t.Fatal("unbounded dashboard omitted a task without an agent run")
	}
}
