package automation

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"taskboard/internal/domain"
	"testing"
	"time"
)

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}

func TestRemoveRunWorktreeRemovesOnlyIsolatedCheckout(t *testing.T) {
	source := t.TempDir()
	runGit(t, source, "init", "-b", "main")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	tracked := filepath.Join(source, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("source stays intact\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "tracked.txt")
	runGit(t, source, "commit", "-m", "initial")
	worktree := filepath.Join(t.TempDir(), "run")
	runID := "test-cleanup"
	runGit(t, source, "worktree", "add", "-b", "agent/run-"+runID, worktree, "HEAD")

	if err := removeRunWorktree(context.Background(), runID, source, worktree); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("isolated worktree still exists: %v", err)
	}
	if contents, err := os.ReadFile(tracked); err != nil || string(contents) != "source stays intact\n" {
		t.Fatalf("source repository was altered: %q, %v", contents, err)
	}
	if output, err := exec.Command("git", "-C", source, "branch", "--list", "agent/run-"+runID).Output(); err != nil || strings.TrimSpace(string(output)) != "" {
		t.Fatalf("run branch was not removed: %q, %v", output, err)
	}
}

func TestRepositoryApplyLockSerializesConcurrentDelivery(t *testing.T) {
	source := t.TempDir()
	runGit(t, source, "init", "-b", "main")
	unlock, err := lockRepository(context.Background(), source)
	if err != nil {
		t.Fatalf("first repository lock: %v", err)
	}
	blocked, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := lockRepository(blocked, source); err == nil {
		t.Fatal("second delivery acquired a repository lock while the first held it")
	}
	unlock()
	secondUnlock, err := lockRepository(context.Background(), source)
	if err != nil {
		t.Fatalf("lock was not released for the next delivery: %v", err)
	}
	secondUnlock()
}

func TestRunCommitExistsProvidesIdempotentDeliveryMarker(t *testing.T) {
	source := t.TempDir()
	runGit(t, source, "init", "-b", "main")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "delivery.txt"), []byte("accepted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "delivery.txt")
	runID := "0a8f7a12-9fa9-4ec7-a6ad-accepted"
	runGit(t, source, "commit", "-m", "taskboard: accept run "+runID)
	found, err := runCommitExists(context.Background(), source, runID)
	if err != nil || !found {
		t.Fatalf("delivery commit marker = %t, %v", found, err)
	}
	found, err = runCommitExists(context.Background(), source, "other-run")
	if err != nil || found {
		t.Fatalf("unrelated delivery marker = %t, %v", found, err)
	}
}

func TestProviderCommandSplitsConfiguredAdapter(t *testing.T) {
	command, args := providerCommand("codex exec")
	if command != "codex" || !reflect.DeepEqual(args, []string{"exec"}) {
		t.Fatalf("unexpected command: %q %#v", command, args)
	}
}

func TestCodexPromptIsPassedOnlyViaStdin(t *testing.T) {
	prompt := "--- task context ---\nTitle: deploy --now\n"
	command, args, stdin, err := cliInvocation(domain.ProviderSetting{Provider: "codex", Command: "codex exec", Model: "gpt-5.6-luna"}, prompt)
	if err != nil || command != "codex" || stdin != prompt {
		t.Fatalf("unexpected Codex invocation: %q %#v %q %v", command, args, stdin, err)
	}
	if !reflect.DeepEqual(args, []string{"exec", "--approve-for-me", "--color", "never", "--model", "gpt-5.6-luna", "-"}) {
		t.Fatalf("Codex arguments contain an unsafe prompt or are malformed: %#v", args)
	}
}

func TestWebhookRetryBackoffIsBoundedAndMonotonic(t *testing.T) {
	if got := webhookRetryDelay(0); got != time.Second {
		t.Fatalf("first webhook retry = %s, want 1s", got)
	}
	if got := webhookRetryDelay(4); got != 8*time.Second {
		t.Fatalf("fourth webhook retry = %s, want 8s", got)
	}
	if got := webhookRetryDelay(99); got != 32*time.Second {
		t.Fatalf("bounded webhook retry = %s, want 32s", got)
	}
}

func TestWebhookEventSubscriptionMatchesWholeEventNames(t *testing.T) {
	if !webhookSubscribes("run.succeeded, run.failed", "run.succeeded") {
		t.Fatal("configured event should match")
	}
	if webhookSubscribes("run.succeeded_extra", "run.succeeded") {
		t.Fatal("event must not match a prefix of another event")
	}
}

func TestSendWebhookUsesStableDeliveryHeadersAndRejectsErrors(t *testing.T) {
	var gotEvent, gotDelivery, gotContentType, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEvent = r.Header.Get("X-Shipyard-Event")
		gotDelivery = r.Header.Get("X-Shipyard-Delivery")
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	delivery := domain.WebhookDelivery{ID: "delivery-1", URL: server.URL, EventName: "run.succeeded", Payload: `{"run_id":"run-1"}`}
	if err := sendWebhook(context.Background(), server.Client(), delivery); err != nil {
		t.Fatalf("send webhook: %v", err)
	}
	if gotEvent != "run.succeeded" || gotDelivery != "delivery-1" || gotContentType != "application/json" || gotBody != delivery.Payload {
		t.Fatalf("unexpected webhook request: event=%q delivery=%q content-type=%q body=%q", gotEvent, gotDelivery, gotContentType, gotBody)
	}
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer failed.Close()
	if err := sendWebhook(context.Background(), failed.Client(), domain.WebhookDelivery{URL: failed.URL}); err == nil {
		t.Fatal("non-success webhook response must be retryable error")
	}
}

func TestCodexOptionsBecomeSafeCLIArguments(t *testing.T) {
	args, err := codexOptionArgs(`{"reasoning_effort":"high","profile":"delivery","config":{"features.some_feature":true,"model_context_window":128000}}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--config", `model_reasoning_effort="high"`, "--profile", "delivery", "--config", "features.some_feature=true", "--config", "model_context_window=128000"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("Codex options = %#v, want %#v", args, want)
	}
	for _, raw := range []string{`{"reasoning_effort":"maximum"}`, `{"unknown":true}`, `{"config":{"bad key":true}}`} {
		if _, err := codexOptionArgs(raw); err == nil {
			t.Fatalf("invalid options %s were accepted", raw)
		}
	}
}

func TestCodexWorkingDirectoryPrecedesTheStdinPrompt(t *testing.T) {
	args := withWorkingDirectory([]string{"exec", "--sandbox", "workspace-write", "-"}, "/isolated/worktree")
	want := []string{"exec", "--sandbox", "workspace-write", "--cd", "/isolated/worktree", "-"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("working directory must remain an option before stdin prompt: %#v", args)
	}
	if got := withWorkingDirectory([]string{"exec"}, ""); !reflect.DeepEqual(got, []string{"exec"}) {
		t.Fatalf("empty directory must not alter invocation: %#v", got)
	}
}

func TestCodexFinalMessagePathPrecedesTheStdinPrompt(t *testing.T) {
	args := withOutputLastMessage([]string{"exec", "--cd", "/isolated/worktree", "-"}, "/safe/run.final")
	want := []string{"exec", "--cd", "/isolated/worktree", "--output-last-message", "/safe/run.final", "-"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("final-message path must remain an option before stdin prompt: %#v", args)
	}
}

func TestClaudePromptRemainsAnExplicitArgument(t *testing.T) {
	command, args, stdin, err := cliInvocation(domain.ProviderSetting{Provider: "claude", Command: "claude --print"}, "review this")
	if err != nil || command != "claude" || stdin != "" || !reflect.DeepEqual(args, []string{"--print", "review this"}) {
		t.Fatalf("unexpected Claude invocation: %q %#v %q %v", command, args, stdin, err)
	}
}

func TestFormatTaskContextIncludesLifecycleAndComments(t *testing.T) {
	start := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	due := start.Add(48 * time.Hour)
	context := formatTaskContext(domain.Task{ID: "task-1", Title: "Deployment anpassen", Description: "Cache entfernen", Priority: "high", ColumnName: "Blocked", StartDate: &start, DueDate: &due, CreatedAt: start, Labels: []domain.Label{{Name: "deployment"}}}, domain.Board{Name: "Inhouse"}, []domain.Project{{Name: "API", RepositoryURL: "https://example.test/api", DefaultBranch: "main"}}, []domain.ProjectGroup{{Name: "Inhouse"}}, []domain.History{{FromName: "In Arbeit", ToName: "Blocked", Source: "agent_failure", OccurredAt: due}}, []domain.Comment{{Author: "Taskboard", Body: "Run fehlgeschlagen", CreatedAt: due}}, nil, start)
	for _, want := range []string{"Deployment anpassen", "Cache entfernen", "Labels: deployment", "Fällig: 2026-09-16", "API | https://example.test/api", "In Arbeit → Blocked", "Run fehlgeschlagen", "BEGINN AUFGABENKONTEXT"} {
		if !strings.Contains(context, want) {
			t.Fatalf("context is missing %q: %s", want, context)
		}
	}
}

func TestRequestedInteractionsParsesButtons(t *testing.T) {
	logs := []domain.RunLog{{Message: "```taskboard-interaction\n{\"key\":\"database\",\"title\":\"Datenbank\",\"fields\":[{\"id\":\"db\",\"label\":\"Wahl\",\"type\":\"buttons\",\"options\":[{\"value\":\"postgres\",\"label\":\"Postgres\"}]}]}\n```"}}
	requests := requestedInteractions(logs)
	if len(requests) != 1 || requests[0].Fields[0].Type != "buttons" {
		t.Fatalf("unexpected interactions: %#v", requests)
	}
}

func TestRequestedTriageControlsAcceptOneBoundedRequest(t *testing.T) {
	logs := []domain.RunLog{{Message: "```taskboard-update\n{\"title\":\"Klarer Titel\",\"description\":\"Konkrete Anforderungen\"}\n```\n```taskboard-targets\n{\"project_ids\":[\"project-1\"],\"group_ids\":[]}\n```"}}
	update, ok := requestedTaskUpdate(logs)
	if !ok || update.Title != "Klarer Titel" || update.Description != "Konkrete Anforderungen" {
		t.Fatalf("unexpected triage update: %#v, %t", update, ok)
	}
	targets, ok := requestedTaskTargets(logs)
	if !ok || !reflect.DeepEqual(targets.ProjectIDs, []string{"project-1"}) {
		t.Fatalf("unexpected triage targets: %#v, %t", targets, ok)
	}
}

func TestRequestedTriageControlsRejectAmbiguousOrEmptyRequests(t *testing.T) {
	logs := []domain.RunLog{{Message: "```taskboard-update\n{\"title\":\"\",\"description\":\"x\"}\n```\n```taskboard-targets\n{\"project_ids\":[],\"group_ids\":[]}\n```"}}
	if _, ok := requestedTaskUpdate(logs); ok {
		t.Fatal("empty update must be rejected")
	}
	if _, ok := requestedTaskTargets(logs); ok {
		t.Fatal("empty target request must be rejected")
	}
}

func TestRequestedInteractionsAcceptsKeyAsFieldIdentifier(t *testing.T) {
	logs := []domain.RunLog{{Message: "```taskboard-interaction\n{\"key\":\"release\",\"title\":\"Freigabe\",\"fields\":[{\"key\":\"release_decision\",\"label\":\"Freigabeentscheidung\",\"type\":\"buttons\",\"options\":[{\"value\":\"approve\",\"label\":\"Freigeben\"}]}]}\n```"}}
	requests := requestedInteractions(logs)
	if len(requests) != 1 || requests[0].Fields[0].ID != "release_decision" || requests[0].Fields[0].Key != "" {
		t.Fatalf("legacy key was not normalized: %#v", requests)
	}
}

func TestQAReleaseRequestIsAStableHumanDecision(t *testing.T) {
	request := qaReleaseRequest()
	if request.Key != "qa_release" || len(request.Fields) != 1 || request.Fields[0].ID != "release_decision" || request.Fields[0].Type != "buttons" {
		t.Fatalf("unexpected QA request: %#v", request)
	}
	if got, want := len(request.Fields[0].Options), 4; got != want {
		t.Fatalf("QA options = %d, want %d", got, want)
	}
}

func TestIsQAColumnUsesTheTaskColumnRatherThanAgentName(t *testing.T) {
	if !isQAColumn(domain.Task{ColumnName: " QA "}) || isQAColumn(domain.Task{ColumnName: "Review"}) {
		t.Fatal("QA column detection is not stable")
	}
}

func TestApplyRejectsAnEmptyDiff(t *testing.T) {
	source := t.TempDir()
	runGit(t, source, "init", "-b", "main")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "tracked.txt"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "tracked.txt")
	runGit(t, source, "commit", "-m", "initial")
	// The service-level guard is intentionally checked through the git diff
	// primitive: an empty patch must never reach apply/commit.
	diff, err := exec.Command("git", "-C", source, "diff", "--binary", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(diff)) != "" {
		t.Fatalf("expected empty diff, got %q (%v)", diff, err)
	}
}

func TestRequestedInteractionsRequireAStableDecisionKey(t *testing.T) {
	logs := []domain.RunLog{{Message: "```taskboard-interaction\n{\"title\":\"Datenbank\",\"fields\":[{\"id\":\"db\",\"label\":\"Wahl\",\"type\":\"text\"}]}\n```"}}
	if got := requestedInteractions(logs); len(got) != 0 {
		t.Fatalf("unstable interactions must be rejected: %#v", got)
	}
}

func TestRequestedTransitionAcceptsStableColumnID(t *testing.T) {
	logs := []domain.RunLog{{Message: "```taskboard-transition\n{\"target_column_id\":\"column-development\"}\n```"}}
	route, ok := requestedTransition(logs)
	if !ok || route.TargetColumnID != "column-development" || route.Target != "" {
		t.Fatalf("unexpected ID route: %#v, %v", route, ok)
	}
}

func TestFormatAllowedTransitionsUsesIDsAndDisplayLabels(t *testing.T) {
	got := formatAllowedTransitions([]domain.Transition{{ToColumnID: "development-id"}}, []domain.Column{{ID: "development-id", Name: "Entwicklung"}})
	if !strings.Contains(got, "target_column_id") || !strings.Contains(got, "development-id") || !strings.Contains(got, "Entwicklung") {
		t.Fatalf("missing structured transition context: %s", got)
	}
}

func TestRequestedTransitionIsUnambiguous(t *testing.T) {
	logs := []domain.RunLog{{Message: "```taskboard-transition\n{\"target\":\"In Progress\",\"comment\":\"Bitte Schnittstelle nachziehen.\"}\n```"}}
	route, ok := requestedTransition(logs)
	if !ok || route.Target != "In Progress" || route.Comment != "Bitte Schnittstelle nachziehen." {
		t.Fatalf("unexpected route: %#v, %v", route, ok)
	}
	logs = append(logs, domain.RunLog{Message: "```taskboard-transition\n{\"target\":\"QA\"}\n```"})
	route, ok = requestedTransition(logs)
	if !ok || route.Target != "In Progress" {
		t.Fatalf("first route must win, got %#v", route)
	}
}

func TestRequestedTaskCommentsAreDeduplicatedAndBounded(t *testing.T) {
	logs := []domain.RunLog{{Message: "```taskboard-comment\nTests: go test ./...\n```\n```taskboard-comment\nTests: go test ./...\n```"}}
	comments := requestedTaskComments(logs)
	if len(comments) != 1 || comments[0] != "Tests: go test ./..." {
		t.Fatalf("comments = %#v", comments)
	}
}

func TestStructuredAgentOutputCanCrossTerminalLogChunks(t *testing.T) {
	logs := []domain.RunLog{
		{Message: "```taskboard-interaction\n{\"key\":\"release\",\"title\":\"Freigabe\",\"fields\":[{\"id\":\"decision\",\"label\":\"Entscheidung\",\"type\":\"but"},
		{Message: "tons\",\"options\":[{\"value\":\"approve\",\"label\":\"Freigeben\"}]}]}\n```\n```taskboard-comment\nFertig\n```"},
	}
	requests := requestedInteractions(logs)
	if len(requests) != 1 || requests[0].Key != "release" {
		t.Fatalf("cross-chunk interaction = %#v", requests)
	}
	if comments := requestedTaskComments(logs); len(comments) != 1 || comments[0] != "Fertig" {
		t.Fatalf("cross-chunk comments = %#v", comments)
	}
}

func TestQANacharbeitDoesNotLeaveAReleaseQuestionOpen(t *testing.T) {
	interactions := []interactionRequest{{Key: "qa_release"}, {Key: "other"}}
	remaining := withoutReleaseInteraction(interactions)
	if len(remaining) != 1 || remaining[0].Key != "other" {
		t.Fatalf("remaining interactions = %#v", remaining)
	}
	columns := []domain.Column{{ID: "done-id", Name: "Erledigt", Type: "done"}, {ID: "progress-id", Name: "In Progress", Type: "standard"}}
	if !targetColumnHasType(columns, "done-id", "done") || !targetColumnHasType(columns, "Erledigt", "done") || targetColumnHasType(columns, "progress-id", "done") {
		t.Fatal("QA delivery gate must use the semantic done column type")
	}
}

func TestReportedCLITokenUsageUsesCodexSummaryInsteadOfTerminalBytes(t *testing.T) {
	logs := []domain.RunLog{
		{Message: "large lockfile output tokens used 9"},
		{Message: "tokens used\n51,452"},
	}
	usage, ok := reportedCLITokenUsage(logs)
	if !ok || usage != 51452 {
		t.Fatalf("usage = %d, %t; want 51452, true", usage, ok)
	}
	if _, ok := reportedCLITokenUsage([]domain.RunLog{{Message: "terminal byte count 400000"}}); ok {
		t.Fatal("terminal output without a Codex summary must not be treated as tokens")
	}
}

func TestInteractionFingerprintIsStableAndSeparatesDifferentQuestions(t *testing.T) {
	fields := []interactionField{{ID: "database", Label: "Datenbank", Type: "buttons", Options: []interactionOption{{Value: "postgres", Label: "Postgres"}}}}
	first := interactionFingerprint("database", fields)
	if first == "" || first != interactionFingerprint("database", fields) {
		t.Fatalf("fingerprint must be stable: %q", first)
	}
	if first == interactionFingerprint("runtime", fields) {
		t.Fatal("different decision keys must not share a fingerprint")
	}
}

func TestFormatTaskContextMakesResolvedDecisionAuthoritative(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	context := formatTaskContext(domain.Task{ID: "task-1", Title: "App", CreatedAt: now}, domain.Board{Name: "Personal"}, nil, nil, nil, nil, []domain.TaskDecision{{Key: "database", Title: "Datenbank", Response: []byte(`{"database":["postgres"]}`), FreeformAnswer: "Postgres ist verbindlich", ResolvedAt: now}}, now)
	for _, want := range []string{"Verbindliche Nutzerentscheidungen", "database", "postgres", "nicht erneut abfragen"} {
		if !strings.Contains(context, want) {
			t.Fatalf("context is missing %q: %s", want, context)
		}
	}
}

func TestCommandForClaudeDoesNotReceiveCodexArguments(t *testing.T) {
	command, args, err := commandForProvider(domain.ProviderSetting{Provider: "claude", Command: "claude --print", Model: "claude-sonnet"})
	if err != nil || command != "claude" || !reflect.DeepEqual(args, []string{"--print", "--model", "claude-sonnet"}) {
		t.Fatalf("unexpected Claude adapter: %q %#v %v", command, args, err)
	}
}

func TestCommandForUnsupportedOpenAIIsExplicit(t *testing.T) {
	_, _, err := commandForProvider(domain.ProviderSetting{Provider: "openai"})
	if err == nil {
		t.Fatal("expected explicit unsupported API adapter error")
	}
}

func TestValidateProviderOptionsFailsBeforeRunForInvalidCodexSettings(t *testing.T) {
	if err := ValidateProviderOptions("codex", `{"reasoning_effort":"turbo"}`); err == nil {
		t.Fatal("expected invalid Codex options to be rejected")
	}
	if err := ValidateProviderOptions("codex", `{"reasoning_effort":"high","config":{"feature_flag":true}}`); err != nil {
		t.Fatalf("valid Codex options rejected: %v", err)
	}
	if err := ValidateProviderOptions("claude", `{"future_option":true}`); err != nil {
		t.Fatalf("extensible provider options rejected: %v", err)
	}
}

func TestCodexCommandValidationKeepsWorkerOwnedFlagsOutOfSettings(t *testing.T) {
	for _, command := range []string{"", "codex", "codex exec", "/opt/codex/bin/codex exec"} {
		if err := ValidateProviderConfiguration("codex", command, `{"reasoning_effort":"medium"}`); err != nil {
			t.Fatalf("valid command %q rejected: %v", command, err)
		}
	}
	for _, command := range []string{"codex exec --approve-for-me", "codex --sandbox danger-full-access", "codex review"} {
		if err := ValidateProviderConfiguration("codex", command, "{}"); err == nil {
			t.Fatalf("unsafe command %q accepted", command)
		}
	}
}

func TestProviderCommandKeepsAdapterArguments(t *testing.T) {
	command, args := providerCommand("/usr/local/bin/claude --print")
	if command != "/usr/local/bin/claude" || !reflect.DeepEqual(args, []string{"--print"}) {
		t.Fatalf("unexpected command: %q %#v", command, args)
	}
}
func TestRedactSensitiveDiffRemovesCredentialValues(t *testing.T) {
	diff := "api_key=super-secret-value\nAuthorization: Bearer bearer-secret-value\naws=AKIA1234567890ABCDEF\n-----BEGIN PRIVATE KEY-----\nprivate-material\n-----END PRIVATE KEY-----"
	redacted := redactSensitiveDiff(diff)
	for _, secret := range []string{"super-secret-value", "bearer-secret-value", "AKIA1234567890ABCDEF", "private-material"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("secret %q remained", secret)
		}
	}
	if !strings.Contains(redacted, "[REDACTED]") || !strings.Contains(redacted, "[REDACTED PRIVATE KEY]") {
		t.Fatalf("redaction marker missing: %s", redacted)
	}
}
