package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"taskboard/internal/domain"
	"testing"
	"time"
)

// integrationStore is intentionally opt-in. The repository does not ship a
// disposable PostgreSQL fixture, while these checks are useful in CI and for
// maintainers with a real schema available. Never use the application's
// DATABASE_URL here: integration tests must not mutate a production database.
func integrationStore(t *testing.T) *Store {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("SHIPYARD_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("SHIPYARD_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	t.Cleanup(func() { s.DB.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate integration database: %v", err)
	}
	return s
}

func columnByName(t *testing.T, columns []domain.Column, name string) domain.Column {
	t.Helper()
	for _, column := range columns {
		if column.Name == name {
			return column
		}
	}
	t.Fatalf("column %q not found in %#v", name, columns)
	return domain.Column{}
}

func TestWorkflowIntegrationGermanIDsIgnoreDisplayLabels(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	board, err := s.CreateBoardWithTemplate(ctx, "Integration DE", "software")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	task, err := s.CreateTask(ctx, board.ID, "ID-Transition", "test", "normal", "", "", "integration")
	if err != nil {
		t.Fatal(err)
	}
	columns, err := s.Columns(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	backlog := columnByName(t, columns, "Backlog")
	development := columnByName(t, columns, "Entwicklung")
	if moved, err := s.MoveTaskToColumnID(ctx, task.ID, backlog.ID, "integration"); err != nil || !moved {
		t.Fatalf("Inbox -> Backlog by ID: moved=%t err=%v", moved, err)
	}
	if moved, err := s.MoveTaskToColumnID(ctx, task.ID, development.ID, "integration"); err != nil || !moved {
		t.Fatalf("Backlog -> Entwicklung by ID: moved=%t err=%v", moved, err)
	}
	current, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ColumnID != development.ID || current.ColumnName != "Entwicklung" {
		t.Fatalf("unexpected German destination: %#v", current)
	}
	before := current.ColumnID
	if moved, err := s.MoveTaskToColumnID(ctx, task.ID, development.ID, "integration"); err != nil || moved {
		t.Fatalf("self-transition must be a silent no-op: moved=%t err=%v", moved, err)
	}
	current, err = s.GetTask(ctx, task.ID)
	if err != nil || current.ColumnID != before {
		t.Fatalf("self-transition changed task: %#v err=%v", current, err)
	}
	if _, err := s.MoveTaskToColumnID(ctx, task.ID, "00000000-0000-0000-0000-000000000000", "integration"); err == nil || !strings.Contains(err.Error(), "erlaubte Übergänge") {
		t.Fatalf("unknown ID must explain allowed transitions: %v", err)
	}
	current, err = s.GetTask(ctx, task.ID)
	if err != nil || current.ColumnID != before {
		t.Fatalf("unknown target changed task: %#v err=%v", current, err)
	}
}

func TestWorkflowIntegrationEnglishIDsRemainCompatible(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	board, err := s.CreateBoardWithTemplate(ctx, "Integration EN", "empty")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	inbox, err := s.AddColumn(ctx, board.ID, "Inbox", "inbox")
	if err != nil {
		t.Fatal(err)
	}
	backlog, err := s.AddColumn(ctx, board.ID, "Backlog", "standard")
	if err != nil {
		t.Fatal(err)
	}
	development, err := s.AddColumn(ctx, board.ID, "Development", "standard")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddTransition(ctx, board.ID, inbox.ID, backlog.ID, "Plan"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddTransition(ctx, board.ID, backlog.ID, development.ID, "Start"); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, board.ID, "English ID transition", "test", "normal", "", "", "integration")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, backlog.ID, "integration"); err != nil {
		t.Fatal(err)
	}
	if moved, err := s.MoveTaskToColumnID(ctx, task.ID, development.ID, "integration"); err != nil || !moved {
		t.Fatalf("Backlog -> Development by ID: moved=%t err=%v", moved, err)
	}
}

func TestWorkflowIntegrationTriageRunIsCreatedExactlyOnce(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	board, err := s.CreateBoardWithTemplate(ctx, "Integration triage", "software")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	columns, err := s.Columns(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	backlog := columnByName(t, columns, "Backlog")
	development := columnByName(t, columns, "Entwicklung")
	task, err := s.CreateTask(ctx, board.ID, "Triage once", "test", "normal", "", "", "integration")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, "Triage Agent", "integration", "", "", "", t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateRuleWithActions(ctx, "Triage development", board.ID, "task.entered_column", development.ID, agent.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, backlog.ID, "integration"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, development.ID, "integration"); err != nil {
		t.Fatal(err)
	}
	events, err := s.PendingEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var event domain.AutomationEvent
	for _, candidate := range events {
		if candidate.TaskID == task.ID && candidate.Type == "task.entered_column" {
			event = candidate
		}
	}
	if event.ID == "" {
		t.Fatal("development transition did not create an automation event")
	}
	runs, err := s.CreateRunsForEvent(ctx, event, rule)
	if err != nil || len(runs) != 1 {
		t.Fatalf("first triage scheduling: runs=%d err=%v", len(runs), err)
	}
	if _, err = s.CreateRunsForEvent(ctx, event, rule); !errors.Is(err, ErrAutomationActive) {
		t.Fatalf("duplicate triage scheduling must be rejected as active: %v", err)
	}
	allRuns, err := s.RunsForTask(ctx, task.ID)
	if err != nil || len(allRuns) != 1 {
		t.Fatalf("delivery agent must be started exactly once: runs=%d err=%v", len(allRuns), err)
	}
}
