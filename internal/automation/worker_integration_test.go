package automation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	runGit(t, repository, "config", "user.name", "Integration Test")
	runGit(t, repository, "config", "user.email", "integration@example.invalid")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("integration\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "README.md")
	runGit(t, repository, "commit", "-m", "initial")

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
	triageCount := filepath.Join(t.TempDir(), "triage-count")
	deliveryCount := filepath.Join(t.TempDir(), "delivery-count")
	triageScript := fakeProviderScript(t, triageCount, "```taskboard-transition\n"+fmt.Sprintf(`{"target_column_id":"%s"}`, development.ID)+"\n```\n")
	unknownScript := fakeProviderScript(t, triageCount, "```taskboard-transition\n{\"target_column_id\":\"00000000-0000-0000-0000-000000000000\"}\n```\n")
	deliveryScript := fakeProviderScript(t, deliveryCount, "```taskboard-self-review\n{\"status\":\"passed\",\"checklist\":[{\"check\":\"Scope/Akzeptanz\",\"result\":\"ok\"},{\"check\":\"Diff/Secrets\",\"result\":\"ok\"},{\"check\":\"Tests/Fehler\",\"result\":\"ok\"},{\"check\":\"Sicherheits-/Betriebsrisiken\",\"result\":\"ok\"},{\"check\":\"Rückwärtskompatibilität\",\"result\":\"ok\"}],\"tests\":\"integration\",\"open_risks\":\"none\"}\n```\n")
	suffix := time.Now().Format("20060102150405.000000000")
	triageAgent, err := s.CreateAgent(ctx, "Triage Agent "+t.Name()+" "+suffix, "integration", "", "", "", repository, 1)
	if err != nil {
		t.Fatal(err)
	}
	deliveryAgent, err := s.CreateAgent(ctx, "Delivery Agent "+t.Name()+" "+suffix, "integration", "", "", "", repository, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, "UPDATE agents SET adapter='codex' WHERE id=$1", triageAgent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, "UPDATE agents SET adapter='claude' WHERE id=$1", deliveryAgent.ID); err != nil {
		t.Fatal(err)
	}
	codex, err := s.Provider(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	claude, err := s.Provider(ctx, "claude")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.SaveProvider(ctx, codex.Provider, codex.Model, codex.Command, codex.SecretEnv, codex.BaseURL, codex.Options, codex.Enabled)
		_ = s.SaveProvider(ctx, claude.Provider, claude.Model, claude.Command, claude.SecretEnv, claude.BaseURL, claude.Options, claude.Enabled)
	})
	if err = s.SaveProvider(ctx, "codex", codex.Model, triageScript, codex.SecretEnv, codex.BaseURL, codex.Options, true); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveProvider(ctx, "claude", claude.Model, deliveryScript, claude.SecretEnv, claude.BaseURL, claude.Options, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRuleWithActions(ctx, "Run triage", board.ID, "task.entered_column", backlog.ID, triageAgent.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRuleWithActions(ctx, "Start delivery once", board.ID, "task.entered_column", development.ID, deliveryAgent.ID, "", ""); err != nil {
		t.Fatal(err)
	}

	worker := &Worker{Store: s}
	task, err := s.CreateTask(ctx, board.ID, "Successful triage", "test", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, backlog.ID, "mcp"); err != nil {
		t.Fatal(err)
	}
	waitForWorkerCondition(t, worker, func() bool {
		count := readCount(t, deliveryCount)
		current, readErr := s.GetTask(ctx, task.ID)
		return readErr == nil && current.ColumnID == development.ID && count == 1
	})
	if got := readCount(t, deliveryCount); got != 1 {
		t.Fatalf("successful triage must start exactly one delivery agent, got %d", got)
	}

	unknownTask, err := s.CreateTask(ctx, board.ID, "Unknown target", "test", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, unknownTask.ID, backlog.ID, "mcp"); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveProvider(ctx, "codex", codex.Model, unknownScript, codex.SecretEnv, codex.BaseURL, codex.Options, true); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetTask(ctx, unknownTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitForWorkerCondition(t, worker, func() bool { return readCount(t, triageCount) >= 2 })
	after, err := s.GetTask(ctx, unknownTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ColumnID != before.ColumnID || readCount(t, deliveryCount) != 1 {
		t.Fatalf("unknown target changed state or started delivery: before=%s after=%s starts=%d", before.ColumnID, after.ColumnID, readCount(t, deliveryCount))
	}
}

func fakeProviderScript(t *testing.T, countPath, transition string, target ...string) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "provider.sh")
	output := transition
	if len(target) > 0 {
		output = fmt.Sprintf(output, target[0])
	}
	contents := "#!/bin/sh\ncount=0\nif [ -f " + shellQuoteForTest(countPath) + " ]; then count=$(cat " + shellQuoteForTest(countPath) + "); fi\nprintf '%s' $((count + 1)) > " + shellQuoteForTest(countPath) + "\nprevious=''\nfor argument in \"$@\"; do\n  if [ \"$previous\" = \"--output-last-message\" ]; then printf '%s' " + shellQuoteForTest(output) + " > \"$argument\"; fi\n  previous=\"$argument\"\ndone\nprintf '%s' " + shellQuoteForTest(output) + "\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

func shellQuoteForTest(value string) string {
	return "'" + value + "'"
}

func readCount(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	value, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func waitForWorkerCondition(t *testing.T, worker *Worker, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		worker.Process(context.Background())
		if condition() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("worker condition was not reached within 10 seconds")
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
