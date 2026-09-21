package automation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"taskboard/internal/domain"
	"taskboard/internal/release"
	"taskboard/internal/store"
	"taskboard/internal/workspace"
	"testing"
	"time"
)

type workerReleaseGitHub struct {
	created []release.PullRequestInput
	updated []release.PullRequestInput
	current *release.PullRequest
}

func (g *workerReleaseGitHub) FindPR(context.Context, string, string, string, string, string) (*release.PullRequest, error) {
	if g.current == nil {
		return nil, nil
	}
	copy := *g.current
	return &copy, nil
}

func (g *workerReleaseGitHub) CreatePR(_ context.Context, in release.PullRequestInput) (release.PullRequest, error) {
	g.created = append(g.created, in)
	g.current = &release.PullRequest{Number: 91, URL: "https://github.com/example/release-lifecycle/pull/91", Head: in.Head, Base: in.Base, Body: in.Body}
	return *g.current, nil
}

func (g *workerReleaseGitHub) UpdatePR(_ context.Context, _ int, in release.PullRequestInput) (release.PullRequest, error) {
	g.updated = append(g.updated, in)
	g.current = &release.PullRequest{Number: 91, URL: "https://github.com/example/release-lifecycle/pull/91", Head: in.Head, Base: in.Base, Body: in.Body}
	return *g.current, nil
}

func integrationGitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func integrationGitCommand(t *testing.T, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}

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

func TestProcessRoutesExplicitQAReworkExactlyOnce(t *testing.T) {
	s := workerIntegrationStore(t)
	ctx := context.Background()
	board, err := s.CreateBoardWithTemplate(ctx, "QA rework handoff", "software")
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
	review := integrationColumnByName(t, columns, "Review")
	qa := integrationColumnByName(t, columns, "QA")
	task, err := s.CreateTask(ctx, board.ID, "Explicit QA rework", "test", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, "QA rework delivery "+time.Now().Format("20060102150405.000000000"), "integration", "", "", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{backlog.ID, development.ID, review.ID, qa.ID} {
		if moved, moveErr := s.MoveTaskToColumnID(ctx, task.ID, target, "mcp"); moveErr != nil || !moved {
			t.Fatalf("move task to %s: moved=%t err=%v", target, moved, moveErr)
		}
	}
	rule, err := s.CreateRuleWithActions(ctx, "Process explicit QA rework", board.ID, "task.entered_column", development.ID, agent.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun(ctx, task.ID, agent.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	interaction, err := s.CreateInteraction(ctx, task.ID, agent.ID, run.ID, "qa_release", "worker-qa-rework", "Freigabe für QA", "QA entscheidet", []byte(`{"fields":[{"id":"release_decision","type":"buttons"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ResolveInteractionAndMove(ctx, interaction.ID, "qa", "Ablehnungsgrund", []byte(`{"release_decision":["rework"]}`), ""); err != nil {
		t.Fatal(err)
	}
	worker := &Worker{Store: s, executeRun: func(context.Context, domain.AgentRun) {}}
	worker.Process(ctx)
	worker.Process(ctx)
	runs, err := s.RunsForTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, candidate := range runs {
		if candidate.RuleID == rule.ID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("explicit QA rework created %d delivery runs, want exactly one", count)
	}
	current, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ColumnID != development.ID {
		t.Fatalf("QA rework target = %s, want development %s", current.ColumnID, development.ID)
	}
}

func TestCheckProviderForAgentRejectsUnassignedSecret(t *testing.T) {
	s := workerIntegrationStore(t)
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405000000000")
	agent, err := s.CreateAgent(ctx, "Provider check agent "+suffix, "integration", "", "", "", 1)
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

func TestProcessKeepsTaskCompletedRetryableWhenReleasePublisherIsMissing(t *testing.T) {
	s := workerIntegrationStore(t)
	ctx := context.Background()
	board, err := s.CreateBoardWithTemplate(ctx, "Missing release publisher", "software")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	task, err := s.CreateTask(ctx, board.ID, "Retry release", "test", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, `INSERT INTO automation_events(type,task_id,board_id,payload)
		VALUES('task.completed',$1,$2,'{}'::jsonb)`, task.ID, board.ID); err != nil {
		t.Fatal(err)
	}

	(&Worker{Store: s}).Process(ctx)

	var processedAt *time.Time
	var attempts int
	var lastError string
	if err = s.DB.QueryRow(ctx, `SELECT processed_at,attempts,last_error
		FROM automation_events WHERE task_id=$1 AND type='task.completed'`, task.ID).
		Scan(&processedAt, &attempts, &lastError); err != nil {
		t.Fatal(err)
	}
	if processedAt != nil {
		t.Fatal("task.completed was acknowledged without a release publisher")
	}
	if attempts != 1 {
		t.Fatalf("missing release publisher attempts = %d, want 1", attempts)
	}
	if !strings.Contains(lastError, "Release-Agent blockiert") || !strings.Contains(lastError, "nicht konfiguriert") {
		t.Fatalf("retry error does not expose the blocking reason: %q", lastError)
	}
}

func TestProcessPublishesDoneTaskAndRetriesWithoutDuplicateSideEffects(t *testing.T) {
	s := workerIntegrationStore(t)
	ctx := context.Background()
	t.Setenv("SHIPYARD_SECRET_KEY", "integration-release-secret-key")
	t.Setenv("SHIPYARD_GITHUB_SECRET_ENV", "SHIPYARD_TEST_GITHUB_TOKEN")

	board, err := s.CreateBoardWithTemplate(ctx, "Release lifecycle", "software")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	source := t.TempDir()
	project, err := s.CreateProject(ctx, "Release lifecycle project", "https://github.com/example/release-lifecycle.git", "master", source, []string{board.ID})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, board.ID, "Publish accepted delivery", "release integration", "high", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, "Release lifecycle agent "+time.Now().Format("20060102150405.000000000"), "integration", "", "", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := s.CreateSecret(ctx, "integration", "release-lifecycle-token", "integration token", "SHIPYARD_TEST_GITHUB_TOKEN", "github_pat_integration_secret")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSecretAgents(ctx, "integration", secret.ID, []string{agent.ID}); err != nil {
		t.Fatal(err)
	}

	commit := "0123456789abcdef0123456789abcdef01234567"
	var runID, eventID string
	if err = s.DB.QueryRow(ctx, `INSERT INTO agent_runs(task_id,agent_id,status,prompt_snapshot,workspace_snapshot,target_project_id,source_workspace,accepted_commit_sha,applied_at,diff_summary,gate_status)
		VALUES($1,$2,'succeeded','accepted delivery','managed checkout',$3,$4,$5,now(),'release summary','passed') RETURNING id`, task.ID, agent.ID, project.ID, source, commit).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(ctx, `INSERT INTO automation_events(type,task_id,board_id,payload)
		VALUES('task.completed',$1,$2,'{}'::jsonb) RETURNING id`, task.ID, board.ID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}

	var requests []release.Request
	worker := &Worker{
		Store: s,
		ReleasePublisher: ReleasePublisherFunc(func(_ context.Context, request release.Request) (release.Result, error) {
			requests = append(requests, request)
			return release.Result{PR: release.PullRequest{Number: 42, URL: "https://github.com/example/release-lifecycle/pull/42"}}, nil
		}),
	}
	worker.Process(ctx)
	if len(requests) != 1 {
		t.Fatalf("publisher calls after first processing = %d, want 1", len(requests))
	}
	if requests[0].RunID != runID || requests[0].ProjectID != project.ID || requests[0].CommitSHA != commit {
		t.Fatalf("publisher request lost durable identity: %#v", requests[0])
	}
	if len(requests[0].SecretValues) != 1 || requests[0].SecretValues[0] != "github_pat_integration_secret" {
		t.Fatalf("publisher did not receive the assigned secret")
	}

	var processedAt *time.Time
	if err = s.DB.QueryRow(ctx, "SELECT processed_at FROM automation_events WHERE id=$1", eventID).Scan(&processedAt); err != nil {
		t.Fatal(err)
	}
	if processedAt == nil {
		t.Fatal("successful task.completed event was not acknowledged")
	}
	publication, found, err := s.ReleasePublication(ctx, task.ID, project.ID, runID)
	if err != nil || !found || publication.PRURL != "https://github.com/example/release-lifecycle/pull/42" {
		t.Fatalf("publication = %#v found=%t err=%v", publication, found, err)
	}
	var auditCount, commentCount int
	if err = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE kind='release.pr.published' AND resource_id=$1", task.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(ctx, "SELECT count(*) FROM task_comments WHERE task_id=$1 AND author='Release-Agent'", task.ID).Scan(&commentCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 || commentCount != 1 {
		t.Fatalf("side effects after first processing: audit=%d comments=%d", auditCount, commentCount)
	}

	if _, err = s.DB.Exec(ctx, `INSERT INTO automation_events(type,task_id,board_id,payload)
		VALUES('task.completed',$1,$2,'{}'::jsonb)`, task.ID, board.ID); err != nil {
		t.Fatal(err)
	}
	worker.Process(ctx)
	if len(requests) != 1 {
		t.Fatalf("publisher calls after retry = %d, want 1", len(requests))
	}
	var auditAfterRetry, commentsAfterRetry int
	if err = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE kind='release.pr.published' AND resource_id=$1", task.ID).Scan(&auditAfterRetry); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(ctx, "SELECT count(*) FROM task_comments WHERE task_id=$1 AND author='Release-Agent'", task.ID).Scan(&commentsAfterRetry); err != nil {
		t.Fatal(err)
	}
	if auditAfterRetry != 1 || commentsAfterRetry != 1 {
		t.Fatalf("retry duplicated side effects: audit=%d comments=%d", auditAfterRetry, commentsAfterRetry)
	}
}

func TestProcessPublishesThroughReleasePublishAndGitPusher(t *testing.T) {
	s := workerIntegrationStore(t)
	ctx := context.Background()
	t.Setenv("SHIPYARD_SECRET_KEY", "integration-release-secret-key")
	t.Setenv("SHIPYARD_GITHUB_SECRET_ENV", "SHIPYARD_TEST_GITHUB_TOKEN")

	board, err := s.CreateBoardWithTemplate(ctx, "Release publish adapter", "software")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	source := t.TempDir()
	runGit(t, source, "init", "-b", "master")
	runGit(t, source, "config", "user.name", "Integration Test")
	runGit(t, source, "config", "user.email", "integration@example.invalid")
	if err = os.WriteFile(filepath.Join(source, "README.md"), []byte("release publish integration\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "README.md")
	runGit(t, source, "commit", "-m", "initial")
	bare := filepath.Join(t.TempDir(), "remote.git")
	integrationGitCommand(t, "init", "--bare", bare)
	// Keep the configured remote canonical for the production repository check,
	// while rewriting its transport to the disposable local bare repository.
	runGit(t, source, "remote", "add", "origin", "https://github.com/example/release-lifecycle.git")
	runGit(t, source, "config", "url.file://"+bare+".insteadOf", "https://github.com/example/release-lifecycle.git")
	task, err := s.CreateTask(ctx, board.ID, "Publish through adapters", "release integration", "high", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	branch := taskIntegrationBranch(task.ID)
	runGit(t, source, "checkout", "-b", branch)
	commit := integrationGitOutput(t, source, "rev-parse", "HEAD")
	project, err := s.CreateProject(ctx, "Release adapter project", "https://github.com/example/release-lifecycle.git", "master", source, []string{board.ID})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, "Release adapter agent "+time.Now().Format("20060102150405.000000000"), "integration", "", "", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := s.CreateSecret(ctx, "integration", "release-adapter-token", "integration token", "SHIPYARD_TEST_GITHUB_TOKEN", "github_pat_integration_secret")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSecretAgents(ctx, "integration", secret.ID, []string{agent.ID}); err != nil {
		t.Fatal(err)
	}
	var runID, eventID string
	if err = s.DB.QueryRow(ctx, `INSERT INTO agent_runs(task_id,agent_id,status,prompt_snapshot,workspace_snapshot,target_project_id,source_workspace,accepted_commit_sha,applied_at,diff_summary,gate_status)
		VALUES($1,$2,'succeeded','accepted delivery','managed checkout',$3,$4,$5,now(),'release summary','passed') RETURNING id`, task.ID, agent.ID, project.ID, source, commit).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(ctx, `INSERT INTO automation_events(type,task_id,board_id,payload)
		VALUES('task.completed',$1,$2,'{}'::jsonb) RETURNING id`, task.ID, board.ID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	github := &workerReleaseGitHub{}
	var requests []release.Request
	worker := &Worker{Store: s, ReleasePublisher: ReleasePublisherFunc(func(ctx context.Context, request release.Request) (release.Result, error) {
		requests = append(requests, request)
		return release.Publish(ctx, request, release.GitPusher{}, github)
	})}
	worker.Process(ctx)
	if len(requests) != 1 || requests[0].RunID != runID || requests[0].CommitSHA != commit {
		t.Fatalf("release publish requests = %#v", requests)
	}
	if len(github.created) != 1 || len(github.updated) != 0 {
		t.Fatalf("PR operations after first processing: created=%d updated=%d", len(github.created), len(github.updated))
	}
	if got := integrationGitOutput(t, bare, "show-ref", "--verify", "refs/heads/"+branch); got == "" {
		t.Fatal("accepted branch was not pushed to the assigned bare remote")
	}
	var processedAt *time.Time
	if err = s.DB.QueryRow(ctx, "SELECT processed_at FROM automation_events WHERE id=$1", eventID).Scan(&processedAt); err != nil {
		t.Fatal(err)
	}
	if processedAt == nil {
		t.Fatal("successful release event was not acknowledged")
	}

	if _, err = s.DB.Exec(ctx, `INSERT INTO automation_events(type,task_id,board_id,payload)
		VALUES('task.completed',$1,$2,'{}'::jsonb)`, task.ID, board.ID); err != nil {
		t.Fatal(err)
	}
	worker.Process(ctx)
	if len(requests) != 1 || len(github.created) != 1 || len(github.updated) != 0 {
		t.Fatalf("retry duplicated release side effects: requests=%d created=%d updated=%d", len(requests), len(github.created), len(github.updated))
	}
	publication, found, err := s.ReleasePublication(ctx, task.ID, project.ID, runID)
	if err != nil || !found || publication.PRURL != "https://github.com/example/release-lifecycle/pull/91" {
		t.Fatalf("publication = %#v found=%t err=%v", publication, found, err)
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
	triageAgent, err := s.CreateAgent(ctx, "Triage Agent "+t.Name()+" "+suffix, "integration", "", "", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	deliveryAgent, err := s.CreateAgent(ctx, "Delivery Agent "+t.Name()+" "+suffix, "integration", "", "", "", 1)
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
	agent, err := s.CreateAgent(ctx, "Concurrent apply agent "+time.Now().Format("20060102150405.000000000"), "integration", "", "", "", 1)
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

func TestRecordIntegrationConflictUsesQueueIDsWhenRunCannotBeLoaded(t *testing.T) {
	s := workerIntegrationStore(t)
	ctx := context.Background()
	board, err := s.CreateBoardWithTemplate(ctx, "Queue conflict audit", "software")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	task, err := s.CreateTask(ctx, board.ID, "Conflict task", "exercise conflict audit", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}

	const missingRunID = "00000000-0000-0000-0000-000000000001"
	worker := &Worker{Store: s}
	_ = worker.recordIntegrationConflictByIDs(ctx, missingRunID, task.ID, managedCheckoutProblem("mit Remote-Stand nicht konfliktfrei rebasierbar", "shared.txt", "base=base-sha head=head-sha"))
	updated, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(updated.ColumnName, "Needs action") {
		t.Fatalf("conflict task column = %q, want Needs action", updated.ColumnName)
	}
	comments, err := s.Comments(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "shared.txt") || !strings.Contains(comments[0].Body, "base=base-sha") {
		t.Fatalf("conflict audit comment = %#v", comments)
	}
}

func TestProcessIntegrationQueueReturnsConflictAuditFailure(t *testing.T) {
	s := workerIntegrationStore(t)
	ctx := context.Background()
	remote := filepath.Join(t.TempDir(), "remote.git")
	source := filepath.Join(t.TempDir(), "source")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, t.TempDir(), "clone", remote, source)
	runGit(t, source, "switch", "-c", "master")
	runGit(t, source, "config", "user.name", "Integration Test")
	runGit(t, source, "config", "user.email", "integration@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "shared.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "shared.txt")
	runGit(t, source, "commit", "-m", "initial")
	runGit(t, source, "push", "-u", "origin", "master")

	board, err := s.CreateBoard(ctx, "Queue conflict audit "+time.Now().Format("150405.000000000"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	task, err := s.CreateTask(ctx, board.ID, "Queue conflict", "exercise queue conflict", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	branch, err := ensureTaskBranch(ctx, source, task.ID, "master")
	if err != nil {
		t.Fatal(err)
	}
	branchWorktree := filepath.Join(t.TempDir(), "task-branch")
	runGit(t, source, "worktree", "add", branchWorktree, branch)
	if err := os.WriteFile(filepath.Join(branchWorktree, "shared.txt"), []byte("task\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, branchWorktree, "add", "shared.txt")
	runGit(t, branchWorktree, "commit", "-m", "task change")
	head, err := gitOutput(ctx, branchWorktree, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "worktree", "remove", "--force", branchWorktree)

	other := filepath.Join(t.TempDir(), "other")
	runGit(t, t.TempDir(), "clone", remote, other)
	runGit(t, other, "config", "user.name", "Remote Test")
	runGit(t, other, "config", "user.email", "remote@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "shared.txt"), []byte("remote\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "add", "shared.txt")
	runGit(t, other, "commit", "-m", "remote conflict")
	runGit(t, other, "push", "origin", "master")

	base, err := gitOutput(ctx, source, "rev-parse", "origin/master")
	if err != nil {
		t.Fatal(err)
	}
	const missingRunID = "00000000-0000-0000-0000-000000000002"
	job, err := s.EnqueueIntegration(ctx, domain.IntegrationJob{RepositoryPath: source, RunID: missingRunID, TaskID: task.ID, Branch: branch, DefaultBranch: "master", BaseSHA: base, HeadSHA: head})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.IntegrationJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	w := &Worker{Store: s}
	if err := w.processIntegrationQueue(ctx); err == nil || !strings.Contains(err.Error(), "Run-Protokoll") {
		t.Fatalf("queue conflict audit failure = %v, want visible Run-Protokoll error", err)
	}
	var status, step, lastError string
	if err := s.DB.QueryRow(ctx, "SELECT status,step,last_error FROM repository_integration_queue WHERE id=$1", job.ID).Scan(&status, &step, &lastError); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || step != "conflict" || !strings.Contains(lastError, "Run-Protokoll") {
		t.Fatalf("queue conflict state = status %q step %q error %q", status, step, lastError)
	}
}

func TestProcessIntegrationQueueDeadLettersMissingHead(t *testing.T) {
	s := workerIntegrationStore(t)
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "source")
	runGit(t, t.TempDir(), "init", "-b", "master", source)
	runGit(t, source, "config", "user.name", "Integration Test")
	runGit(t, source, "config", "user.email", "integration@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "base.txt")
	runGit(t, source, "commit", "-m", "initial")
	base, err := gitOutput(ctx, source, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	board, err := s.CreateBoard(ctx, "Queue dead-letter "+time.Now().Format("150405.000000000"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	task, err := s.CreateTask(ctx, board.ID, "Queue dead-letter task", "exercise terminal missing head", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, "Queue dead-letter agent "+time.Now().Format("150405.000000000"), "integration", "", "", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun(ctx, task.ID, agent.ID, "")
	if err != nil {
		t.Fatal(err)
	}

	job, err := s.EnqueueIntegration(ctx, domain.IntegrationJob{
		RepositoryPath: source,
		RunID:          run.ID,
		TaskID:         task.ID,
		Branch:         "task/" + task.ID,
		DefaultBranch:  "master",
		BaseSHA:        base,
		HeadSHA:        "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}
	w := &Worker{Store: s}
	if err := w.processIntegrationQueue(ctx); err == nil || !strings.Contains(err.Error(), "lokal nicht verfügbar") {
		t.Fatalf("missing head queue error = %v, want lokal nicht verfügbar", err)
	}
	var status, step, lastError string
	var attempts int
	if err := s.DB.QueryRow(ctx, "SELECT status,step,last_error,attempts FROM repository_integration_queue WHERE id=$1", job.ID).Scan(&status, &step, &lastError, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || attempts < 1 || !strings.Contains(lastError, "lokal nicht verfügbar") {
		t.Fatalf("missing head dead-letter = status %q step %q attempts %d error %q", status, step, attempts, lastError)
	}
	claimed, err := s.IntegrationJobs(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, claimedJob := range claimed {
		if claimedJob.ID == job.ID {
			t.Fatalf("failed job remained eligible: %#v", claimedJob)
		}
	}
}

func TestProcessIntegrationQueueEndToEndRebasesPushesCreatesPRAndSyncsMerge(t *testing.T) {
	s := workerIntegrationStore(t)
	ctx := context.Background()
	remote := filepath.Join(t.TempDir(), "remote.git")
	source := filepath.Join(t.TempDir(), "source")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, t.TempDir(), "clone", remote, source)
	runGit(t, source, "switch", "-c", "master")
	runGit(t, source, "config", "user.name", "Integration Test")
	runGit(t, source, "config", "user.email", "integration@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "base.txt")
	runGit(t, source, "commit", "-m", "initial")
	runGit(t, source, "push", "-u", "origin", "master")

	board, err := s.CreateBoard(ctx, "Queue E2E "+time.Now().Format("150405.000000000"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	project, err := s.CreateProject(ctx, "Queue E2E project "+time.Now().Format("150405.000000000"), remote, "master", source, []string{board.ID})
	if err != nil {
		t.Fatal(err)
	}
	// The task target is inherited from the board's single project.
	task, err := s.CreateTask(ctx, board.ID, "Queue E2E task", "exercise the durable integration queue", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, "Queue E2E agent "+time.Now().Format("150405.000000000"), "integration", "", "", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateRule(ctx, "Queue E2E rule", board.ID, "task.created", "", agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun(ctx, task.ID, agent.ID, rule.ID)
	if err != nil {
		t.Fatal(err)
	}

	branch, err := ensureTaskBranch(ctx, source, task.ID, project.DefaultBranch)
	if err != nil {
		t.Fatal(err)
	}
	branchWorktree := filepath.Join(t.TempDir(), "task-branch")
	runGit(t, source, "worktree", "add", branchWorktree, branch)
	if err := os.WriteFile(filepath.Join(branchWorktree, "delivery.txt"), []byte("delivery\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, branchWorktree, "add", "delivery.txt")
	runGit(t, branchWorktree, "commit", "-m", "delivery")
	head, err := gitOutput(ctx, branchWorktree, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "worktree", "remove", "--force", branchWorktree)

	// Advance the default branch after the task branch was created. The queue
	// must fetch and rebase before pushing the task branch.
	other := filepath.Join(t.TempDir(), "other")
	runGit(t, t.TempDir(), "clone", remote, other)
	runGit(t, other, "config", "user.name", "Remote Test")
	runGit(t, other, "config", "user.email", "remote@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "remote.txt"), []byte("remote\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "add", "remote.txt")
	runGit(t, other, "commit", "-m", "remote progress")
	runGit(t, other, "push", "origin", "master")

	stateFile := filepath.Join(t.TempDir(), "pr-state")
	fakeGH := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(fakeGH, []byte("#!/bin/sh\n"+
		"case \"$*\" in\n"+"  *'pr list'*) printf '%s\\n' '[]' ;;\n"+"  *'number,url'*) printf '%s\\n' '{\"number\":17,\"url\":\"https://example.invalid/pr/17\"}' ;;\n"+"  *'pr view'*) if grep -q merged \"$PR_STATE\"; then printf '%s\\n' '{\"state\":\"MERGED\",\"mergedAt\":\"2026-09-18T00:00:00Z\"}'; else printf '%s\\n' '{\"state\":\"OPEN\",\"mergedAt\":null}'; fi ;;\n"+"esac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, []byte("open\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PR_STATE", stateFile)
	t.Setenv("PATH", filepath.Dir(fakeGH)+string(os.PathListSeparator)+os.Getenv("PATH"))

	base, err := gitOutput(ctx, source, "rev-parse", "origin/master")
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.EnqueueIntegration(ctx, domain.IntegrationJob{RepositoryPath: source, RunID: run.ID, TaskID: task.ID, Branch: branch, DefaultBranch: project.DefaultBranch, BaseSHA: base, HeadSHA: head})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.IntegrationJobs(ctx, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("first queue claimant got %d jobs: %v", len(claimed), err)
	}
	if duplicate, err := s.IntegrationJobs(ctx, 1); err != nil || len(duplicate) != 0 {
		t.Fatalf("second concurrent claimant got %d jobs: %v", len(duplicate), err)
	}
	if err := s.UpdateIntegration(ctx, job.ID, "queued", "fetch", base, head, "", "", 0, 0); err != nil {
		t.Fatal(err)
	}
	w := &Worker{Store: s}
	w.processIntegrationQueue(ctx)
	var status, step, pushedHead, lastError string
	if err := s.DB.QueryRow(ctx, "SELECT status,step,head_sha,last_error FROM repository_integration_queue WHERE id=$1", job.ID).Scan(&status, &step, &pushedHead, &lastError); err != nil {
		t.Fatal(err)
	}
	if status != "pr_open" || step != "done" || pushedHead == head {
		t.Fatalf("queue did not complete rebase/push/PR: status=%s step=%s head=%s original=%s error=%s", status, step, pushedHead, head, lastError)
	}
	if _, err := gitOutput(ctx, remote, "show", "task/"+task.ID+":remote.txt"); err != nil {
		t.Fatalf("rebased remote change missing from pushed task branch: %v", err)
	}

	// Simulate the provider-confirmed merge in the bare remote, then run the
	// same durable job again. The managed checkout must fast-forward cleanly.
	merger := filepath.Join(t.TempDir(), "merger")
	runGit(t, t.TempDir(), "clone", remote, merger)
	runGit(t, merger, "config", "user.name", "Merge Test")
	runGit(t, merger, "config", "user.email", "merge@example.invalid")
	runGit(t, merger, "fetch", "origin", branch)
	runGit(t, merger, "switch", "master")
	runGit(t, merger, "merge", "--ff-only", "origin/"+branch)
	runGit(t, merger, "push", "origin", "master")
	if err := os.WriteFile(stateFile, []byte("merged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.processIntegrationQueue(ctx)
	if err := s.DB.QueryRow(ctx, "SELECT status FROM repository_integration_queue WHERE id=$1", job.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" {
		t.Fatalf("merged queue job status = %q, want succeeded", status)
	}
	managedHead, err := gitOutput(ctx, source, "rev-parse", "HEAD")
	if err != nil || managedHead != pushedHead {
		t.Fatalf("managed checkout head = %q, want rebased delivery %q (err=%v)", managedHead, pushedHead, err)
	}
	if dirty, err := gitOutput(ctx, source, "status", "--porcelain"); err != nil || dirty != "" {
		t.Fatalf("managed checkout is not clean: %q (%v)", dirty, err)
	}
}

func TestClaudeDeliveryRunProcessesSelfReviewWithArgvPrompt(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is required for Claude delivery lifecycle tests")
	}
	if err := bubblewrapPreflight(context.Background()); err != nil {
		t.Skip(err.Error())
	}
	s := workerIntegrationStore(t)
	comment := "Claude Abschluss"
	script := fakeStdoutProviderScript(t, germanDeliveryCompletion(comment))
	run := startDeliveryLifecycleRun(t, s, deliveryLifecycleOptions{
		adapter:  "claude",
		model:    "claude-sonnet-4-6",
		provider: "claude",
		command:  script,
	})
	assertDeliveryProcessedCompletion(t, s, run, comment)
}

func TestOpenAIDeliveryRunProcessesSelfReviewAndComment(t *testing.T) {
	installDummyBwrap(t)
	t.Setenv("SHIPYARD_SECRET_KEY", "be02-openai-lifecycle-key")
	comment := "OpenAI Abschluss"
	completion := germanDeliveryCompletion(comment)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(openaiCompletionPayload(t, completion))
	}))
	t.Cleanup(server.Close)
	previous := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = previous })

	s := workerIntegrationStore(t)
	run := startDeliveryLifecycleRun(t, s, deliveryLifecycleOptions{
		adapter:   "openai",
		model:     "gpt-test",
		provider:  "openai",
		baseURL:   server.URL,
		secretEnv: "SHIPYARD_BE02_OPENAI_KEY",
		assignKey: true,
	})
	assertDeliveryProcessedCompletion(t, s, run, comment)
}

type deliveryLifecycleOptions struct {
	adapter, model, provider, command, baseURL, secretEnv string
	assignKey                                             bool
}

func startDeliveryLifecycleRun(t *testing.T, s *store.Store, opts deliveryLifecycleOptions) domain.AgentRun {
	t.Helper()
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	t.Setenv("TASKBOARD_WORKSPACE_ROOT", workspaceRoot)
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".shipyard-workspace"), []byte("storage=local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Validate(); err != nil {
		t.Fatalf("workspace preflight: %v", err)
	}

	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, remote, "init", "--bare", "-b", "master")
	seed := t.TempDir()
	runGit(t, seed, "init", "-b", "master")
	runGit(t, seed, "config", "user.name", "Lifecycle Test")
	runGit(t, seed, "config", "user.email", "lifecycle@example.invalid")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("lifecycle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "README.md")
	runGit(t, seed, "commit", "-m", "initial")
	runGit(t, seed, "remote", "add", "origin", remote)
	runGit(t, seed, "push", "-u", "origin", "master")

	board, err := s.CreateBoardWithTemplate(ctx, "BE02 lifecycle "+t.Name(), "software")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteBoard(ctx, board.ID) })
	project, err := s.CreateProject(ctx, "BE02 project "+t.Name(), remote, "master", "", []string{board.ID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteProject(ctx, project.ID) })
	columns, err := s.Columns(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	development := integrationColumnByName(t, columns, "Entwicklung")
	suffix := time.Now().Format("20060102150405.000000000")
	agent, err := s.CreateAgent(ctx, "Delivery Agent "+t.Name()+" "+suffix, "lifecycle", "", "", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteAgent(ctx, agent.ID) })
	if err = s.UpdateAgentAdapter(ctx, agent.ID, opts.adapter); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateAgentSelection(ctx, agent.ID, opts.model, "medium", "{}"); err != nil {
		t.Fatal(err)
	}
	provider, err := s.Provider(ctx, opts.provider)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.SaveProvider(ctx, provider.Provider, provider.Model, provider.Command, provider.SecretEnv, provider.BaseURL, provider.Options, provider.Enabled)
	})
	secretEnv := provider.SecretEnv
	if opts.secretEnv != "" {
		secretEnv = opts.secretEnv
	}
	if err = s.SaveProvider(ctx, opts.provider, provider.Model, opts.command, secretEnv, opts.baseURL, provider.Options, true); err != nil {
		t.Fatal(err)
	}
	if opts.assignKey {
		secret, secretErr := s.CreateSecret(ctx, "lifecycle", "be02-"+suffix, "lifecycle", secretEnv, "test-openai-secret")
		if secretErr != nil {
			t.Fatal(secretErr)
		}
		t.Cleanup(func() { _ = s.DeleteSecret(ctx, "lifecycle", secret.ID) })
		if err = s.SetSecretAgents(ctx, "lifecycle", secret.ID, []string{agent.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.CreateRuleWithActions(ctx, "Start BE02 delivery "+suffix, board.ID, "task.entered_column", development.ID, agent.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, board.ID, "BE02 delivery", "test", "normal", "", "", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetTaskTargets(ctx, task.ID, []string{project.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveTaskToColumnID(ctx, task.ID, development.ID, "mcp"); err != nil {
		t.Fatal(err)
	}
	worker := &Worker{Store: s}
	var completed domain.AgentRun
	waitForWorkerConditionTimeout(t, worker, 20*time.Second, func() bool {
		runs, readErr := s.RunsForTask(ctx, task.ID)
		if readErr != nil || len(runs) == 0 {
			return false
		}
		latest := runs[0]
		if latest.Status == "succeeded" || latest.Status == "failed" {
			completed = latest
			return true
		}
		return false
	})
	return completed
}

func assertDeliveryProcessedCompletion(t *testing.T, s *store.Store, run domain.AgentRun, comment string) {
	t.Helper()
	ctx := context.Background()
	current, err := s.Run(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "succeeded" {
		logs, _ := s.RunLogs(ctx, run.ID)
		var messages []string
		for _, log := range logs {
			messages = append(messages, log.Message)
		}
		t.Fatalf("delivery run status = %q summary=%q error=%q logs=%q", current.Status, current.Summary, current.ErrorMessage, strings.Join(messages, "\n"))
	}
	comments, err := s.Comments(ctx, current.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range comments {
		if entry.Author == "Agent" && entry.Body == comment {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("final agent comment %q was not persisted: %#v", comment, comments)
	}
}

func waitForWorkerConditionTimeout(t *testing.T, worker *Worker, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		worker.Process(context.Background())
		if condition() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("worker condition was not reached within %s", timeout)
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
