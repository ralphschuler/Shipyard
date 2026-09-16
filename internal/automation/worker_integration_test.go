package automation

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"taskboard/internal/domain"
	"taskboard/internal/store"
	"testing"
	"time"
)

func workerIntegrationStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("SHIPYARD_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("SHIPYARD_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	t.Cleanup(func() { s.DB.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate integration database: %v", err)
	}
	return s
}

func TestProcessStartsDeliveryAgentExactlyOnceAndRejectsUnknownTarget(t *testing.T) {
	s := workerIntegrationStore(t)
	ctx := context.Background()

	board, err := s.CreateBoardWithTemplate(ctx, "Orchestration integration", "software")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	columns, err := s.Columns(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	backlog := integrationColumnByName(t, columns, "Backlog")
	development := integrationColumnByName(t, columns, "Entwicklung")
	agent, err := s.CreateAgent(ctx, "Orchestration Triage", "integration", "", "", "", t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRuleWithActions(ctx, "Start delivery once", board.ID, "task.entered_column", development.ID, agent.ID, "", ""); err != nil {
		t.Fatal(err)
	}

	var starts atomic.Int32
	started := make(chan string, 1)
	worker := &Worker{
		Store: s,
		executeRun: func(runContext context.Context, run domain.AgentRun) {
			starts.Add(1)
			_ = s.SetRunStatus(runContext, run.ID, "running", "test spy claimed delivery", "")
			started <- run.ID
		},
	}
	task, err := s.CreateTask(ctx, board.ID, "Successful triage", "test", "normal", "", "", "integration")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, backlog.ID, "integration"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, development.ID, "integration"); err != nil {
		t.Fatal(err)
	}
	worker.Process(ctx)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery agent was not started")
	}
	worker.Process(ctx)
	if got := starts.Load(); got != 1 {
		t.Fatalf("successful triage must start exactly one delivery agent, got %d", got)
	}
	starts.Store(0)

	unknownTask, err := s.CreateTask(ctx, board.ID, "Unknown target", "test", "normal", "", "", "integration")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, unknownTask.ID, backlog.ID, "integration"); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetTask(ctx, unknownTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, unknownTask.ID, "00000000-0000-0000-0000-000000000000", "integration"); err == nil || !strings.Contains(err.Error(), "erlaubte Übergänge") {
		t.Fatalf("unknown target must return deterministic diagnosis: %v", err)
	}
	worker.Process(ctx)
	after, err := s.GetTask(ctx, unknownTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ColumnID != before.ColumnID || starts.Load() != 0 {
		t.Fatalf("unknown target changed state or started delivery: before=%s after=%s starts=%d", before.ColumnID, after.ColumnID, starts.Load())
	}
}

func integrationColumnByName(t *testing.T, columns []domain.Column, name string) domain.Column {
	t.Helper()
	for _, column := range columns {
		if column.Name == name {
			return column
		}
	}
	t.Fatalf("column %q not found", name)
	return domain.Column{}
}
