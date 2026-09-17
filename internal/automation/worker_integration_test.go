package automation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

func TestCheckProviderForAgentRejectsUnassignedSecret(t *testing.T) {
	s := workerIntegrationStore(t)
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405000000000")
	agent, err := s.CreateAgent(ctx, "Provider check agent "+suffix, "integration", "", "", "", t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteAgent(ctx, agent.ID) })
	provider, err := s.Provider(ctx, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveProvider(ctx, "openai", provider.Model, provider.Command, "SHIPYARD_PROVIDER_TEST_TOKEN", provider.BaseURL, provider.Options, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.SaveProvider(ctx, provider.Provider, provider.Model, provider.Command, provider.SecretEnv, provider.BaseURL, provider.Options, provider.Enabled)
	})

	_, err = (&Worker{Store: s}).CheckProviderForAgent(ctx, "openai", agent.ID)
	if err == nil || !strings.Contains(err.Error(), "kein aktives Secret") {
		t.Fatalf("unassigned agent provider check error = %v", err)
	}
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

func TestApplyConcurrentRetriesObservePersistedAppliedState(t *testing.T) {
	s := workerIntegrationStore(t)
	ctx := context.Background()
	source := t.TempDir()
	runGit(t, source, "init", "-b", "master")
	runGit(t, source, "config", "user.name", "Integration Test")
	runGit(t, source, "config", "user.email", "integration@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "base.txt")
	runGit(t, source, "commit", "-m", "initial")

	board, err := s.CreateBoardWithTemplate(ctx, "Concurrent apply", "software")
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
	agent, err := s.CreateAgent(ctx, "Concurrent apply agent "+time.Now().Format("20060102150405.000000000"), "integration", "", "", "", source, 1)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateRuleWithActions(ctx, "Concurrent apply rule", board.ID, "task.entered_column", development.ID, agent.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, board.ID, "Concurrent apply", "test", "normal", "", "", "mcp")
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
		t.Fatalf("create delivery run: runs=%d err=%v", len(runs), err)
	}
	run := runs[0]
	worktree := filepath.Join(t.TempDir(), "run")
	runGit(t, source, "worktree", "add", worktree, "HEAD")
	if err := os.WriteFile(filepath.Join(worktree, "delivery.txt"), []byte("delivery\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "add", "delivery.txt")
	if _, err = s.DB.Exec(ctx, `UPDATE agent_runs SET status='succeeded',gate_status='passed',source_workspace=$2,worktree_path=$3 WHERE id=$1`, run.ID, source, worktree); err != nil {
		t.Fatal(err)
	}

	worker := &Worker{Store: s}
	results := make(chan error, 2)
	var group sync.WaitGroup
	group.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer group.Done()
			results <- worker.Apply(ctx, run.ID)
		}()
	}
	group.Wait()
	close(results)

	var success, alreadyApplied int
	for applyErr := range results {
		if applyErr == nil {
			success++
		} else if strings.Contains(applyErr.Error(), "bereits übernommen") {
			alreadyApplied++
		} else {
			t.Fatalf("unexpected concurrent apply error: %v", applyErr)
		}
	}
	if success != 1 || alreadyApplied != 1 {
		t.Fatalf("concurrent apply outcomes: success=%d already-applied=%d", success, alreadyApplied)
	}
	content, err := gitOutput(ctx, source, "show", taskIntegrationBranch(task.ID)+":delivery.txt")
	if err != nil || content != "delivery" {
		t.Fatalf("delivery was not committed to the task branch: %q (%v)", content, err)
	}
	if _, err := os.Stat(filepath.Join(source, "delivery.txt")); !os.IsNotExist(err) {
		t.Fatalf("managed source checkout was modified: %v", err)
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
