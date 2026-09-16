package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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
	task, err := s.CreateTask(ctx, board.ID, "ID-Transition", "test", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	columns, err := s.Columns(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	backlog := columnByName(t, columns, "Backlog")
	development := columnByName(t, columns, "Entwicklung")
	if moved, err := s.MoveTaskToColumnID(ctx, task.ID, backlog.ID, "mcp"); err != nil || !moved {
		t.Fatalf("Inbox -> Backlog by ID: moved=%t err=%v", moved, err)
	}
	if moved, err := s.MoveTaskToColumnID(ctx, task.ID, development.ID, "mcp"); err != nil || !moved {
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
	if moved, err := s.MoveTaskToColumnID(ctx, task.ID, development.ID, "mcp"); err != nil || moved {
		t.Fatalf("self-transition must be a silent no-op: moved=%t err=%v", moved, err)
	}
	current, err = s.GetTask(ctx, task.ID)
	if err != nil || current.ColumnID != before {
		t.Fatalf("self-transition changed task: %#v err=%v", current, err)
	}
	if _, err := s.MoveTaskToColumnID(ctx, task.ID, "00000000-0000-0000-0000-000000000000", "mcp"); err == nil || !strings.Contains(err.Error(), "erlaubte Übergänge") {
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
	task, err := s.CreateTask(ctx, board.ID, "English ID transition", "test", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, backlog.ID, "mcp"); err != nil {
		t.Fatal(err)
	}
	if moved, err := s.MoveTaskToColumnID(ctx, task.ID, development.ID, "mcp"); err != nil || !moved {
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
	task, err := s.CreateTask(ctx, board.ID, "Triage once", "test", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, "Triage Agent "+t.Name()+" "+time.Now().Format("20060102150405.000000000"), "integration", "", "", "", t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateRuleWithActions(ctx, "Triage development", board.ID, "task.entered_column", development.ID, agent.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, backlog.ID, "mcp"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, development.ID, "mcp"); err != nil {
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

func TestAutomationFingerprintClaimSerializesConcurrentTransportRetries(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	board, err := s.CreateBoardWithTemplate(ctx, "Fingerprint race", "software")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	columns, err := s.Columns(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	backlog := columnByName(t, columns, "Backlog")
	task, err := s.CreateTask(ctx, board.ID, "Fingerprint race task", "test", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, "Fingerprint race agent "+time.Now().Format("20060102150405.000000000"), "integration", "", "", "", t.TempDir(), 4)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateRuleWithActions(ctx, "Fingerprint race rule", board.ID, "task.entered_column", backlog.ID, agent.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	events := make([]domain.AutomationEvent, 2)
	for i := range events {
		err = s.DB.QueryRow(ctx, `INSERT INTO automation_events(type,task_id,board_id,payload)
			VALUES('task.entered_column',$1,$2,jsonb_build_object('target_column_id',$3::text,'delivery_id',$4::text))
			RETURNING id,type,task_id,board_id,payload,occurred_at`, task.ID, board.ID, backlog.ID, fmt.Sprintf("transport-%d", i)).
			Scan(&events[i].ID, &events[i].Type, &events[i].TaskID, &events[i].BoardID, &events[i].Payload, &events[i].OccurredAt)
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, len(events))
	for _, event := range events {
		wg.Add(1)
		go func(event domain.AutomationEvent) {
			defer wg.Done()
			_, callErr := s.CreateRunsForEvent(ctx, event, rule)
			results <- callErr
		}(event)
	}
	wg.Wait()
	close(results)
	created := 0
	for callErr := range results {
		if callErr == nil {
			created++
			continue
		}
		if !errors.Is(callErr, ErrAutomationActive) && !errors.Is(callErr, ErrNoRunCreated) {
			t.Fatalf("unexpected concurrent claim error: %v", callErr)
		}
	}
	if created != 1 {
		t.Fatalf("concurrent semantic retries started %d batches, want 1", created)
	}
	var runs, claims int
	if err = s.DB.QueryRow(ctx, "SELECT count(*) FROM agent_runs WHERE task_id=$1", task.ID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(ctx, "SELECT count(*) FROM automation_event_claims WHERE task_id=$1", task.ID).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || claims != 1 {
		t.Fatalf("durable deduplication created runs=%d claims=%d, want 1/1", runs, claims)
	}
}

func TestAutomationFingerprintClaimPersistsStatusAttemptsAndBlock(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	board, err := s.CreateBoardWithTemplate(ctx, "Fingerprint status", "software")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	columns, err := s.Columns(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	backlog := columnByName(t, columns, "Backlog")
	task, err := s.CreateTask(ctx, board.ID, "Fingerprint status task", "test", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, "Fingerprint status agent "+time.Now().Format("20060102150405.000000000"), "integration", "", "", "", t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateRuleWithActions(ctx, "Fingerprint status rule", board.ID, "task.entered_column", backlog.ID, agent.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var event domain.AutomationEvent
	err = s.DB.QueryRow(ctx, `INSERT INTO automation_events(type,task_id,board_id,payload)
		VALUES('task.entered_column',$1,$2,jsonb_build_object('target_column_id',$3::text,'transport_id','restart-test'))
		RETURNING id,type,task_id,board_id,payload,occurred_at`, task.ID, board.ID, backlog.ID).
		Scan(&event.ID, &event.Type, &event.TaskID, &event.BoardID, &event.Payload, &event.OccurredAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRunsForEvent(ctx, event, rule); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, "UPDATE agent_run_batches SET status='failed' WHERE task_id=$1", task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, "UPDATE agent_runs SET status='failed' WHERE task_id=$1", task.ID); err != nil {
		t.Fatal(err)
	}
	batches, err := s.DB.Query(ctx, "SELECT id FROM agent_run_batches WHERE task_id=$1", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var batchID string
	if batches.Next() {
		err = batches.Scan(&batchID)
	}
	batches.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RefreshRunBatch(ctx, batchID); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = s.DB.QueryRow(ctx, "SELECT status FROM automation_event_claims WHERE event_id=$1", event.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("claim status after persisted batch failure = %q, want failed", status)
	}
	restarted, err := Open(ctx, s.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.DB.Close() })
	if err = restarted.DB.QueryRow(ctx, "SELECT status FROM automation_event_claims WHERE event_id=$1", event.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("claim status after opening a replacement worker store = %q, want failed", status)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if _, err = s.RecordEventFailure(ctx, event.ID, fmt.Sprintf("failure-%d", attempt+1)); err != nil {
			t.Fatal(err)
		}
	}
	abandoned, err := s.AbandonEvent(ctx, event, "attempt limit")
	if err != nil || !abandoned {
		t.Fatalf("abandon after attempt limit: abandoned=%t err=%v", abandoned, err)
	}
	var attempts int
	if err = s.DB.QueryRow(ctx, "SELECT status,attempts FROM automation_event_claims WHERE event_id=$1", event.ID).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "blocked" || attempts != 3 {
		t.Fatalf("terminal claim = status %q attempts %d, want blocked/3", status, attempts)
	}
	if _, err = s.CreateRunsForEvent(ctx, event, rule); !errors.Is(err, ErrNoRunCreated) {
		t.Fatalf("blocked claim must prevent a restart, got %v", err)
	}
}

func TestWorkflowIntegrationQAReviewReturnRequiresNewAppliedDelivery(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	board, err := s.CreateBoardWithTemplate(ctx, "Generation-bound return", "software")
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
	review := columnByName(t, columns, "Review")
	task, err := s.CreateTask(ctx, board.ID, "Generation-bound return", "test", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, "Generation-bound agent "+time.Now().Format("20060102150405.000000000"), "integration", "", "", "", t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateRuleWithActions(ctx, "Generation-bound rule", board.ID, "task.entered_column", development.ID, agent.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, backlog.ID, "mcp"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, development.ID, "mcp"); err != nil {
		t.Fatal(err)
	}
	events, err := s.PendingEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var developmentEvent domain.AutomationEvent
	for _, event := range events {
		if event.TaskID == task.ID && event.Type == "task.entered_column" && string(event.Payload) != "" {
			var payload map[string]any
			if json.Unmarshal(event.Payload, &payload) == nil && payload["target_column_id"] == development.ID {
				developmentEvent = event
			}
		}
	}
	if developmentEvent.ID == "" {
		t.Fatal("development event not found")
	}
	runs, err := s.CreateRunsForEvent(ctx, developmentEvent, rule)
	if err != nil || len(runs) != 1 {
		t.Fatalf("create delivery run: runs=%d err=%v", len(runs), err)
	}
	if _, err = s.DB.Exec(ctx, "UPDATE agent_runs SET applied_at=now(),accepted_commit_sha='applied-generation' WHERE id=$1", runs[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, review.ID, "mcp"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, development.ID, "mcp"); err != nil {
		t.Fatal(err)
	}
	assertLatestReturnPayload(t, s, ctx, task.ID, true)

	// The same applied delivery must not make a second unchanged return
	// eligible. It is older than the first concrete return generation.
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, review.ID, "mcp"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, development.ID, "mcp"); err != nil {
		t.Fatal(err)
	}
	assertLatestReturnPayload(t, s, ctx, task.ID, false)
}

func TestWorkflowIntegrationAcceptedDeliveryCommitPersistence(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	board, err := s.CreateBoardWithTemplate(ctx, "Accepted commit persistence", "software")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	columns, err := s.Columns(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	development := columnByName(t, columns, "Entwicklung")
	task, err := s.CreateTask(ctx, board.ID, "Accepted commit persistence", "test", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, "Accepted commit agent "+time.Now().Format("20060102150405.000000000"), "integration", "", "", "", t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateRuleWithActions(ctx, "Accepted commit rule", board.ID, "task.entered_column", development.ID, agent.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, development.ID, "mcp"); err != nil {
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
		t.Fatalf("create delivery run: runs=%d err=%v", len(runs), err)
	}
	run := runs[0]
	if _, err = s.DB.Exec(ctx, `UPDATE agent_runs SET status='succeeded',gate_status='passed',source_workspace=$2 WHERE id=$1`, run.ID, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	const acceptedSHA = "0123456789abcdef0123456789abcdef01234567"
	applied, err := s.MarkRunApplied(ctx, run.ID, acceptedSHA)
	if err != nil || !applied {
		t.Fatalf("mark run applied: applied=%t err=%v", applied, err)
	}
	delivery, err := s.RunDelivery(ctx, run.ID)
	if err != nil || delivery.AcceptedCommitSHA != acceptedSHA || delivery.AppliedAt == nil {
		t.Fatalf("persisted delivery = %#v, err=%v", delivery, err)
	}
	commits, err := s.AcceptedRunCommitSHAs(ctx, deliverySource(t, s, ctx, run.ID))
	if err != nil || len(commits) != 1 || commits[0] != acceptedSHA {
		t.Fatalf("accepted commit trust boundary = %#v, err=%v", commits, err)
	}
}

func deliverySource(t *testing.T, s *Store, ctx context.Context, runID string) string {
	t.Helper()
	source, err := s.RunSource(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func assertLatestReturnPayload(t *testing.T, s *Store, ctx context.Context, taskID string, wantChange bool) {
	t.Helper()
	var payload []byte
	if err := s.DB.QueryRow(ctx, `SELECT payload FROM automation_events WHERE task_id=$1 AND payload->>'qa_return'='true' ORDER BY occurred_at DESC LIMIT 1`, taskID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var value struct {
		ChangeAvailable  bool   `json:"change_available"`
		ReturnGeneration string `json:"return_generation"`
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	if value.ChangeAvailable != wantChange || value.ReturnGeneration == "" {
		t.Fatalf("return payload = %#v, want change_available=%t and a generation", value, wantChange)
	}
}
