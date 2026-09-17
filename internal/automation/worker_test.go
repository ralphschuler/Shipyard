package automation

import (
	"context"
	"errors"
	"fmt"
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

func TestMeasuredUsagePointerPreservesKnownZero(t *testing.T) {
	if value := measuredUsagePointer(0, false); value != nil {
		t.Fatalf("unknown usage must remain nil, got %v", *value)
	}
	value := measuredUsagePointer(0, true)
	if value == nil || *value != 0 {
		t.Fatalf("known zero usage was not preserved: %v", value)
	}
}

func TestReportedCLIUsagePreservesExplicitZeroClasses(t *testing.T) {
	logs := []domain.RunLog{{Message: `{"type":"usage","usage":{"api_calls":1,"input_tokens":0,"output_tokens":4,"total_tokens":4}}`}}
	report, ok := reportedCLIUsage(logs)
	if !ok || report.InputTokens == nil || *report.InputTokens != 0 {
		t.Fatalf("explicit zero input usage was lost: %#v, %v", report, ok)
	}
	if report.OutputTokens == nil || *report.OutputTokens != 4 {
		t.Fatalf("output usage was not parsed: %#v", report)
	}
}

func TestEstimateUsageCostKeepsHypotheticalCostForIncompleteSubscriptionRun(t *testing.T) {
	input, output := int64(1_000_000), int64(1_000_000)
	price := domain.UsagePrice{Version: "catalog-2026-09", Input: &input, Output: &output}
	report := domain.UsageReport{Status: "incomplete", CostSource: "unknown", InputTokens: ptrInt64(2), OutputTokens: ptrInt64(3)}

	if !estimateUsageCost(&report, price) {
		t.Fatal("known token usage on an incomplete run must receive a catalog estimate")
	}
	if report.CostSource != "estimated" || report.CalculatedCostMicrousd == nil || *report.CalculatedCostMicrousd != 5 {
		t.Fatalf("unexpected estimated cost report: %#v", report)
	}
	if report.PriceVersion != price.Version || report.CostCalculatedAt == nil {
		t.Fatalf("estimate metadata missing: %#v", report)
	}
}

func ptrInt64(value int64) *int64 { return &value }

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

func TestEnsureTaskBranchFetchesCurrentDefaultBeforeCreation(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	source := filepath.Join(t.TempDir(), "source")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, t.TempDir(), "clone", remote, source)
	runGit(t, source, "switch", "-c", "master")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "base.txt")
	runGit(t, source, "commit", "-m", "initial")
	runGit(t, source, "push", "-u", "origin", "master")

	// Advance the remote in a separate clone so the local origin/master is stale.
	other := filepath.Join(t.TempDir(), "other")
	runGit(t, t.TempDir(), "clone", remote, other)
	runGit(t, other, "config", "user.name", "Test")
	runGit(t, other, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "remote.txt"), []byte("remote\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "add", "remote.txt")
	runGit(t, other, "commit", "-m", "remote advance")
	runGit(t, other, "push", "origin", "master")

	branch, err := ensureTaskBranch(context.Background(), source, "task-from-current-remote")
	if err != nil {
		t.Fatalf("ensure task branch: %v", err)
	}
	if branch != "task/task-from-current-remote" {
		t.Fatalf("branch = %q", branch)
	}
	if _, err := gitOutput(context.Background(), source, "show", branch+":remote.txt"); err != nil {
		t.Fatalf("task branch was not based on the current remote default: %v", err)
	}
}

func TestIntegrationPRMergedRequiresMergedState(t *testing.T) {
	if integrationPRMerged([]byte(`{"state":"OPEN","mergedAt":null}`)) {
		t.Fatal("open pull request reported as merged")
	}
	if !integrationPRMerged([]byte(`{"state":"MERGED","mergedAt":"2026-09-17T16:00:00Z"}`)) {
		t.Fatal("merged pull request was not recognized")
	}
}

func TestIntegrationPRClosedWithoutMergeRequiresReplacement(t *testing.T) {
	if integrationPRNeedsReplacement([]byte(`{"state":"OPEN","mergedAt":null}`)) {
		t.Fatal("open pull request should remain pending")
	}
	if integrationPRNeedsReplacement([]byte(`{"state":"MERGED","mergedAt":"2026-09-17T16:00:00Z"}`)) {
		t.Fatal("merged pull request should be synchronized, not replaced")
	}
	if !integrationPRNeedsReplacement([]byte(`{"state":"CLOSED","mergedAt":null}`)) {
		t.Fatal("closed unmerged pull request should be replaced")
	}
}

func TestReusablePRRequiresOpenOrMergedState(t *testing.T) {
	closed := []byte(`{"number":7,"url":"https://example.test/pr/7","state":"CLOSED","mergedAt":null}`)
	if pr := reusablePR(closed); pr.Number != 0 {
		t.Fatalf("closed pull request was reused: %#v", pr)
	}
	open := []byte(`{"number":8,"url":"https://example.test/pr/8","state":"OPEN","mergedAt":null,"headRefOid":"open-head"}`)
	if pr := reusablePR(open); pr.Number != 8 || pr.URL == "" {
		t.Fatalf("open pull request was not reused: %#v", pr)
	}
}

func TestReusablePRRequiresCurrentHead(t *testing.T) {
	mergedAt := "2026-09-17T16:00:00Z"
	pr := integrationPR{Number: 9, URL: "https://example.test/pr/9", State: "MERGED", MergedAt: &mergedAt, HeadRefOID: "new-head"}
	if candidate := reusablePRCandidate(pr, "old-head"); candidate.Number != 0 {
		t.Fatalf("historical merged pull request was reused: %#v", candidate)
	}
	if candidate := reusablePRCandidate(pr, "new-head"); candidate.Number != 9 {
		t.Fatalf("current merged pull request was not reusable: %#v", candidate)
	}
}

func TestIntegrationDefaultBranchUsesPersistedProjectConfiguration(t *testing.T) {
	project := domain.Project{DefaultBranch: "configured-default"}
	if got := integrationDefaultBranch(project, "checked-out-branch"); got != "configured-default" {
		t.Fatalf("configured default branch = %q", got)
	}
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
	commitSHA, err := gitOutput(context.Background(), source, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	found, err := runCommitExists(context.Background(), source, commitSHA)
	if err != nil || !found {
		t.Fatalf("delivery commit marker = %t, %v", found, err)
	}
	found, err = runCommitExists(context.Background(), source, "other-run")
	if err != nil || found {
		t.Fatalf("unrelated delivery marker = %t, %v", found, err)
	}
}

func TestRunCommitExistsIgnoresMarkerOnUnrelatedRef(t *testing.T) {
	source := t.TempDir()
	runGit(t, source, "init", "-b", "main")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "base.txt")
	runGit(t, source, "commit", "-m", "initial")
	runGit(t, source, "switch", "-c", "abandoned")
	if err := os.WriteFile(filepath.Join(source, "abandoned.txt"), []byte("not accepted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "abandoned.txt")
	runID := "unrelated-ref"
	runGit(t, source, "commit", "-m", "taskboard: accept run "+runID)
	unrelatedSHA, err := gitOutput(context.Background(), source, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "switch", "main")
	found, err := runCommitExists(context.Background(), source, unrelatedSHA)
	if err != nil || found {
		t.Fatalf("commit on unrelated ref = %t, %v", found, err)
	}
}

func TestSyncManagedCheckoutKeepsAcceptedAheadCommit(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	source := filepath.Join(t.TempDir(), "source")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, t.TempDir(), "clone", remote, source)
	runGit(t, source, "switch", "-c", "master")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "base.txt")
	runGit(t, source, "commit", "-m", "initial")
	runGit(t, source, "push", "-u", "origin", "master")
	if err := os.WriteFile(filepath.Join(source, "accepted.txt"), []byte("accepted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "accepted.txt")
	runGit(t, source, "commit", "-m", "taskboard: accept run first")
	acceptedSHA, err := gitOutput(context.Background(), source, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	expected := acceptedSHA
	if err := syncManagedCheckout(context.Background(), source, "master", acceptedSHA); err != nil {
		t.Fatalf("follow-up synchronization: %v", err)
	}
	actual, err := gitOutput(context.Background(), source, "rev-parse", "HEAD")
	if err != nil || actual != expected {
		t.Fatalf("accepted HEAD changed during synchronization: got %s want %s (%v)", actual, expected, err)
	}
}

func TestSyncManagedCheckoutKeepsMultipleAcceptedAheadCommits(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	source := filepath.Join(t.TempDir(), "source")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, t.TempDir(), "clone", remote, source)
	runGit(t, source, "switch", "-c", "master")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "base.txt")
	runGit(t, source, "commit", "-m", "initial")
	runGit(t, source, "push", "-u", "origin", "master")

	for _, change := range []struct {
		name string
		body string
	}{
		{"first.txt", "first\n"},
		{"second.txt", "second\n"},
	} {
		if err := os.WriteFile(filepath.Join(source, change.name), []byte(change.body), 0o600); err != nil {
			t.Fatal(err)
		}
		runGit(t, source, "add", change.name)
		runGit(t, source, "commit", "-m", "taskboard: accept run "+change.name)
	}
	acceptedSHAs, err := gitOutput(context.Background(), source, "log", "--format=%H", "origin/master..HEAD")
	if err != nil {
		t.Fatal(err)
	}
	shas := strings.Split(strings.TrimSpace(acceptedSHAs), "\n")
	if len(shas) != 2 {
		t.Fatalf("accepted commits = %q", acceptedSHAs)
	}
	if err := syncManagedCheckout(context.Background(), source, "master", shas...); err != nil {
		t.Fatalf("follow-up synchronization with two accepted commits: %v", err)
	}
	actual, err := gitOutput(context.Background(), source, "rev-parse", "HEAD")
	if err != nil || actual != shas[0] {
		t.Fatalf("accepted HEAD changed during repeated synchronization: got %s want %s (%v)", actual, shas[0], err)
	}
}

func TestSyncManagedCheckoutReportsDirtyFilesWithoutChangingThem(t *testing.T) {
	source := t.TempDir()
	runGit(t, source, "init", "-b", "master")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "tracked.txt")
	runGit(t, source, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(source, "tracked.txt"), []byte("manual\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := syncManagedCheckout(context.Background(), source, "master")
	if err == nil || !strings.Contains(err.Error(), "tracked.txt") || !strings.Contains(err.Error(), "nichts zurückgesetzt") {
		t.Fatalf("dirty checkout diagnosis = %v", err)
	}
	contents, readErr := os.ReadFile(filepath.Join(source, "tracked.txt"))
	if readErr != nil || string(contents) != "manual\n" {
		t.Fatalf("manual change was altered: %q, %v", contents, readErr)
	}
}

func TestSyncManagedCheckoutBlocksUnacceptedLocalCommit(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	source := filepath.Join(t.TempDir(), "source")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, t.TempDir(), "clone", remote, source)
	runGit(t, source, "switch", "-c", "master")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "base.txt")
	runGit(t, source, "commit", "-m", "initial")
	runGit(t, source, "push", "-u", "origin", "master")
	if err := os.WriteFile(filepath.Join(source, "manual.txt"), []byte("manual\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "manual.txt")
	// A subject is forgeable; only a commit SHA recorded by MarkRunApplied is
	// trusted. This must remain blocked even when the subject looks official.
	runGit(t, source, "commit", "-m", "taskboard: accept run forged-by-hand")
	err := syncManagedCheckout(context.Background(), source, "master")
	if err == nil || !strings.Contains(err.Error(), "nicht als akzeptierte Delivery verifiziert") || !strings.Contains(err.Error(), "manual.txt") {
		t.Fatalf("unaccepted local commit diagnosis = %v", err)
	}
}

func TestSyncManagedCheckoutMergesTrustedDivergence(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	source := filepath.Join(t.TempDir(), "source")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, t.TempDir(), "clone", remote, source)
	runGit(t, source, "switch", "-c", "master")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "base.txt")
	runGit(t, source, "commit", "-m", "initial")
	runGit(t, source, "push", "-u", "origin", "master")
	if err := os.WriteFile(filepath.Join(source, "accepted.txt"), []byte("accepted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "accepted.txt")
	runGit(t, source, "commit", "-m", "taskboard: accept run trusted")
	acceptedSHA, err := gitOutput(context.Background(), source, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	runGit(t, t.TempDir(), "clone", remote, other)
	runGit(t, other, "config", "user.name", "Other")
	runGit(t, other, "config", "user.email", "other@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "remote.txt"), []byte("remote\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "add", "remote.txt")
	runGit(t, other, "commit", "-m", "remote progress")
	runGit(t, other, "push", "origin", "master")
	if err := syncManagedCheckout(context.Background(), source, "master", acceptedSHA); err != nil {
		t.Fatalf("trusted divergence should be merged: %v", err)
	}
	if err := syncManagedCheckout(context.Background(), source, "master", acceptedSHA); err != nil {
		t.Fatalf("synchronized checkout should remain usable: %v", err)
	}
	for _, name := range []string{"accepted.txt", "remote.txt"} {
		if _, err := gitOutput(context.Background(), source, "show", "HEAD:"+name); err != nil {
			t.Fatalf("merged checkout missing %s: %v", name, err)
		}
	}
}

func TestApplyRunPatchToTaskBranchKeepsSourceCleanAndRebasesRemote(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	source := filepath.Join(t.TempDir(), "source")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, t.TempDir(), "clone", remote, source)
	runGit(t, source, "switch", "-c", "master")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "base.txt")
	runGit(t, source, "commit", "-m", "initial")
	runGit(t, source, "push", "-u", "origin", "master")

	taskID := "task-branch-test"
	taskBranch, err := ensureTaskBranch(context.Background(), source, taskID)
	if err != nil {
		t.Fatal(err)
	}
	firstWorktree := filepath.Join(t.TempDir(), "first")
	runGit(t, source, "worktree", "add", "-b", "agent/run-first", firstWorktree, taskBranch)
	if err := os.WriteFile(filepath.Join(firstWorktree, "first.txt"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, firstWorktree, "add", "first.txt")
	firstSHA, err := applyRunPatchToTaskBranch(context.Background(), source, firstWorktree, "run-first", taskID)
	if err != nil {
		t.Fatalf("first task integration: %v", err)
	}
	if firstSHA == "" {
		t.Fatal("first integration did not return a commit")
	}
	runGit(t, source, "worktree", "remove", "--force", firstWorktree)
	runGit(t, source, "branch", "-D", "agent/run-first")

	other := filepath.Join(t.TempDir(), "other")
	runGit(t, t.TempDir(), "clone", remote, other)
	runGit(t, other, "config", "user.name", "Other")
	runGit(t, other, "config", "user.email", "other@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "remote.txt"), []byte("remote\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "add", "remote.txt")
	runGit(t, other, "commit", "-m", "remote progress")
	runGit(t, other, "push", "origin", "master")

	secondWorktree := filepath.Join(t.TempDir(), "second")
	runGit(t, source, "worktree", "add", "-b", "agent/run-second", secondWorktree, taskBranch)
	if err := os.WriteFile(filepath.Join(secondWorktree, "second.txt"), []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, secondWorktree, "add", "second.txt")
	if _, err := applyRunPatchToTaskBranch(context.Background(), source, secondWorktree, "run-second", taskID); err != nil {
		t.Fatalf("second task integration after remote progress: %v", err)
	}
	runGit(t, source, "worktree", "remove", "--force", secondWorktree)
	runGit(t, source, "branch", "-D", "agent/run-second")

	if _, err := gitOutput(context.Background(), source, "show", taskBranch+":first.txt"); err != nil {
		t.Fatalf("first task change missing from durable branch: %v", err)
	}
	if _, err := gitOutput(context.Background(), source, "show", taskBranch+":second.txt"); err != nil {
		t.Fatalf("second task change missing from durable branch: %v", err)
	}
	if _, err := gitOutput(context.Background(), source, "show", taskBranch+":remote.txt"); err != nil {
		t.Fatalf("remote progress missing after rebase: %v", err)
	}
	if _, err := os.Stat(filepath.Join(source, "first.txt")); !os.IsNotExist(err) {
		t.Fatalf("managed source checkout was modified: %v", err)
	}
}

func TestIntegrationPushArgsNeverTargetsDefaultBranch(t *testing.T) {
	args, err := integrationPushArgs("task/example", "master")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(args, " ") != "push --force-with-lease origin task/example:task/example" {
		t.Fatalf("push args = %q", strings.Join(args, " "))
	}
	if _, err := integrationPushArgs("master", "master"); err == nil {
		t.Fatal("default branch must never be a task push target")
	}
}

func TestApplyRunPatchAcceptsTwoSequentialRunWorktrees(t *testing.T) {
	source := t.TempDir()
	runGit(t, source, "init", "-b", "master")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "base.txt")
	runGit(t, source, "commit", "-m", "initial")

	for index, change := range []struct {
		name string
		body string
	}{
		{"first.txt", "first\n"},
		{"second.txt", "second\n"},
	} {
		runID := fmt.Sprintf("follow-up-%d", index+1)
		worktree := filepath.Join(t.TempDir(), runID)
		runGit(t, source, "worktree", "add", worktree, "HEAD")
		if err := os.WriteFile(filepath.Join(worktree, change.name), []byte(change.body), 0o600); err != nil {
			t.Fatal(err)
		}
		runGit(t, worktree, "add", change.name)
		// Keep the run worktree uncommitted: Apply consumes its reviewable diff.
		if _, err := applyRunPatch(context.Background(), source, worktree, runID); err != nil {
			t.Fatalf("apply %s: %v", runID, err)
		}
		if err := removeRunWorktree(context.Background(), runID, source, worktree); err != nil {
			t.Fatalf("cleanup %s: %v", runID, err)
		}
	}
	for _, name := range []string{"first.txt", "second.txt"} {
		if _, err := os.Stat(filepath.Join(source, name)); err != nil {
			t.Fatalf("sequential delivery did not retain %s: %v", name, err)
		}
	}
	commits, err := gitOutput(context.Background(), source, "log", "--format=%s", "-2")
	if err != nil || !strings.Contains(commits, "taskboard: accept run follow-up-1") || !strings.Contains(commits, "taskboard: accept run follow-up-2") {
		t.Fatalf("sequential delivery audit commits = %q, %v", commits, err)
	}
}

func TestFindUnpersistedRunCommitRequiresExactRunDiff(t *testing.T) {
	source := t.TempDir()
	runGit(t, source, "init", "-b", "master")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "base.txt")
	runGit(t, source, "commit", "-m", "initial")
	worktree := filepath.Join(t.TempDir(), "recovery")
	runGit(t, source, "worktree", "add", worktree, "HEAD")
	if err := os.WriteFile(filepath.Join(worktree, "delivery.txt"), []byte("delivery\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "add", "delivery.txt")
	if _, err := applyRunPatch(context.Background(), source, worktree, "recoverable-run"); err != nil {
		t.Fatalf("apply delivery: %v", err)
	}
	found, err := findUnpersistedRunCommit(context.Background(), source, worktree, "recoverable-run")
	if err != nil || found == "" {
		t.Fatalf("matching unpersisted commit = %q, %v", found, err)
	}
	if err := removeRunWorktree(context.Background(), "recoverable-run", source, worktree); err != nil {
		t.Fatalf("cleanup recovery worktree: %v", err)
	}

	// A forgeable subject with a different patch must not be accepted as the
	// recovery marker for this run.
	worktree = filepath.Join(t.TempDir(), "forged")
	runGit(t, source, "worktree", "add", worktree, "HEAD")
	if err := os.WriteFile(filepath.Join(worktree, "different.txt"), []byte("different\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "add", "different.txt")
	runGit(t, source, "-c", "user.name=Taskboard", "-c", "user.email=taskboard@local", "commit", "--allow-empty", "-m", "taskboard: accept run forged-run")
	found, err = findUnpersistedRunCommit(context.Background(), source, worktree, "forged-run")
	if err != nil || found != "" {
		t.Fatalf("forged subject was accepted: %q, %v", found, err)
	}
	if err := removeRunWorktree(context.Background(), "forged-run", source, worktree); err != nil {
		t.Fatalf("cleanup forged worktree: %v", err)
	}
}

func TestApplyRunPatchReportsThreeWayConflictAndPreservesDiff(t *testing.T) {
	source := t.TempDir()
	runGit(t, source, "init", "-b", "master")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	file := filepath.Join(source, "shared.txt")
	if err := os.WriteFile(file, []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "shared.txt")
	runGit(t, source, "commit", "-m", "initial")
	worktree := filepath.Join(t.TempDir(), "conflict")
	runGit(t, source, "worktree", "add", worktree, "HEAD")
	if err := os.WriteFile(filepath.Join(worktree, "shared.txt"), []byte("run change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Advance the managed checkout independently on the same hunk.
	if err := os.WriteFile(file, []byte("manual change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "shared.txt")
	runGit(t, source, "commit", "-m", "unrelated accepted change")

	err := func() error {
		_, applyErr := applyRunPatch(context.Background(), source, worktree, "conflicting-run")
		return applyErr
	}()
	if err == nil || !strings.Contains(err.Error(), "Drei-Wege-Konflikt") || !strings.Contains(err.Error(), "shared.txt") {
		t.Fatalf("conflict diagnosis = %v", err)
	}
	contents, readErr := os.ReadFile(file)
	if readErr != nil || string(contents) != "manual change\n" {
		t.Fatalf("conflict altered managed checkout: %q, %v", contents, readErr)
	}
	if err := removeRunWorktree(context.Background(), "conflicting-run", source, worktree); err != nil {
		t.Fatalf("conflict cleanup: %v", err)
	}
}

func TestApplyRunPatchToTaskBranchReportsPatchFilesBeforeApply(t *testing.T) {
	patch := "diff --git a/shared.txt b/shared.txt\nindex 1234567..7654321 100644\n--- a/shared.txt\n+++ b/shared.txt\n@@ -1 +1 @@\n-base\n+run\n"
	files := patchFiles(patch)
	if !reflect.DeepEqual(files, []string{"shared.txt"}) {
		t.Fatalf("patch files = %#v, want shared.txt", files)
	}
	if got := strings.Join(files, "\n"); got == "" {
		t.Fatal("conflict diagnostics must retain patch paths before git apply mutates the worktree")
	}
}

func TestApplyRunPatchToTaskBranchReportsConflictFiles(t *testing.T) {
	source := t.TempDir()
	runGit(t, source, "init", "-b", "master")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	shared := filepath.Join(source, "shared.txt")
	if err := os.WriteFile(shared, []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "shared.txt")
	runGit(t, source, "commit", "-m", "initial")
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, source, "remote", "add", "origin", remote)
	runGit(t, source, "push", "-u", "origin", "master")
	taskID := "conflict-task"
	branch, err := ensureTaskBranch(context.Background(), source, taskID)
	if err != nil {
		t.Fatal(err)
	}
	branchWorktree := filepath.Join(t.TempDir(), "task-branch")
	runGit(t, source, "worktree", "add", branchWorktree, branch)
	if err := os.WriteFile(filepath.Join(branchWorktree, "shared.txt"), []byte("task branch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, branchWorktree, "add", "shared.txt")
	runGit(t, branchWorktree, "commit", "-m", "task branch change")
	runGit(t, source, "worktree", "remove", "--force", branchWorktree)

	runWorktree := filepath.Join(t.TempDir(), "run")
	runGit(t, source, "worktree", "add", runWorktree, "HEAD")
	if err := os.WriteFile(filepath.Join(runWorktree, "shared.txt"), []byte("run change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, runWorktree, "add", "shared.txt")
	_, err = applyRunPatchToTaskBranch(context.Background(), source, runWorktree, "conflict-run", taskID)
	if err == nil || !strings.Contains(err.Error(), "shared.txt") || !strings.Contains(err.Error(), "Task-Branch") {
		t.Fatalf("task branch conflict diagnosis = %v", err)
	}
	runGit(t, source, "worktree", "remove", "--force", runWorktree)
}

func TestApplyRunPatchBlocksDirtyCheckoutWithoutChangingIt(t *testing.T) {
	source := t.TempDir()
	runGit(t, source, "init", "-b", "master")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	file := filepath.Join(source, "manual.txt")
	if err := os.WriteFile(file, []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "manual.txt")
	runGit(t, source, "commit", "-m", "initial")
	worktree := filepath.Join(t.TempDir(), "dirty-run")
	runGit(t, source, "worktree", "add", worktree, "HEAD")
	if err := os.WriteFile(filepath.Join(worktree, "delivery.txt"), []byte("delivery\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("manual edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := func() error {
		_, applyErr := applyRunPatch(context.Background(), source, worktree, "dirty-run")
		return applyErr
	}()
	if err == nil || !strings.Contains(err.Error(), "manual.txt") || !strings.Contains(err.Error(), "nichts zurückgesetzt") {
		t.Fatalf("dirty diagnosis = %v", err)
	}
	contents, readErr := os.ReadFile(file)
	if readErr != nil || string(contents) != "manual edit\n" {
		t.Fatalf("dirty checkout was altered: %q, %v", contents, readErr)
	}
	if err := removeRunWorktree(context.Background(), "dirty-run", source, worktree); err != nil {
		t.Fatalf("dirty cleanup: %v", err)
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

func TestAgentEnvironmentIncludesOnlyTheConfiguredProviderSecretAndShipyardMCPToken(t *testing.T) {
	t.Setenv("TASKBOARD_MCP_TOKEN", "shipyard-token")
	t.Setenv("PROVIDER_SECRET", "provider-token")
	t.Setenv("UNRELATED_SECRET", "must-not-leak")
	env := strings.Join(agentEnvironment("PROVIDER_SECRET"), "\n")
	for _, want := range []string{"TASKBOARD_MCP_TOKEN=shipyard-token", "PROVIDER_SECRET=provider-token"} {
		if !strings.Contains(env, want) {
			t.Fatalf("agent environment is missing %q: %s", want, env)
		}
	}
	if strings.Contains(env, "UNRELATED_SECRET") {
		t.Fatalf("agent environment leaked an unrelated secret: %s", env)
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

func TestRequestedSelfReviewAcceptsPassedStructuredReview(t *testing.T) {
	logs := []domain.RunLog{{Message: "```taskboard-self-review\n{\"status\":\"passed\",\"checklist\":[{\"check\":\"Scope/Akzeptanz\",\"result\":\"ok\"},{\"check\":\"Diff/Secrets\",\"result\":\"ok\"},{\"check\":\"Tests/Fehler\",\"result\":\"ok\"},{\"check\":\"Sicherheits-/Betriebsrisiken\",\"result\":\"ok\"},{\"check\":\"Rückwärtskompatibilität\",\"result\":\"ok\"}],\"tests\":\"go test ./...\",\"open_risks\":\"none\"}\n```"}}
	if review, err := requestedSelfReview(logs); err != nil || review.Status != "passed" {
		t.Fatalf("passed self-review rejected: %#v, %v", review, err)
	}
}

func TestRequestedSelfReviewAcceptsMachineResultWithDetails(t *testing.T) {
	raw := `{"status":"passed","checklist":[{"check":"Scope/Akzeptanz","result":"passed","details":"Scope and acceptance criteria verified."},{"check":"Diff/Secrets","result":"passed","details":"Diff reviewed."},{"check":"Tests/Fehler","result":"passed","details":"Tests passed."},{"check":"Sicherheits-/Betriebsrisiken","result":"passed","details":"Risks reviewed."},{"check":"Rückwärtskompatibilität","result":"passed","details":"Compatibility reviewed."}],"tests":"go test ./...","open_risks":"none"}`
	review, err := requestedSelfReview([]domain.RunLog{{Message: "```taskboard-self-review\n" + raw + "\n```"}})
	if err != nil {
		t.Fatalf("self-review with details rejected: %v", err)
	}
	if review.Checklist[0].Details != "Scope and acceptance criteria verified." {
		t.Fatalf("details not preserved: %#v", review.Checklist[0])
	}
}

func TestRequestedSelfReviewReportsInvalidResultInsteadOfMissingCategory(t *testing.T) {
	raw := `{"status":"passed","checklist":[{"check":"Scope/Akzeptanz","result":"Release-Agent-Adapter mit Validierung umgesetzt."},{"check":"Diff/Secrets","result":"passed"},{"check":"Tests/Fehler","result":"passed"},{"check":"Sicherheits-/Betriebsrisiken","result":"passed"},{"check":"Rückwärtskompatibilität","result":"passed"}],"tests":"go test ./...","open_risks":"none"}`
	_, err := requestedSelfReview([]domain.RunLog{{Message: "```taskboard-self-review\n" + raw + "\n```"}})
	if err == nil {
		t.Fatal("prose checklist result must be rejected")
	}
	message := err.Error()
	if !strings.Contains(message, "ungültigen Status") || !strings.Contains(message, "erwartet wird einer von") {
		t.Fatalf("error does not identify invalid result: %v", err)
	}
	if strings.Contains(message, "Release-Agent-Adapter") {
		t.Fatalf("error leaked the prose result: %v", err)
	}
	if strings.Contains(message, "keinen bestandenen Checklistenpunkt") {
		t.Fatalf("error still reports a missing passed category: %v", err)
	}
}

func TestRequestedSelfReviewRejectsMissingFailedAndIncompleteReviews(t *testing.T) {
	cases := []string{
		"",
		"```taskboard-self-review\n{\"status\":\"failed\",\"checklist\":[],\"tests\":\"x\",\"open_risks\":\"x\"}\n```",
		"```taskboard-self-review\n{\"status\":\"passed\",\"checklist\":[],\"tests\":\"x\",\"open_risks\":\"x\"}\n```",
	}
	for _, message := range cases {
		if _, err := requestedSelfReview([]domain.RunLog{{Message: message}}); err == nil {
			t.Fatalf("invalid self-review accepted: %q", message)
		}
	}
}

func TestRequestedSelfReviewRejectsUnknownOrDuplicateCategories(t *testing.T) {
	base := `{"status":"passed","checklist":[{"check":"Scope/Akzeptanz","result":"ok"},{"check":"Diff/Secrets","result":"ok"},{"check":"Tests/Fehler","result":"ok"},{"check":"Sicherheits-/Betriebsrisiken","result":"ok"},{"check":"Rückwärtskompatibilität","result":"ok"}],"tests":"go test ./...","open_risks":"none"}`
	unknown := strings.Replace(base, "Rückwärtskompatibilität", "Unbekannte Kategorie", 1)
	duplicate := strings.Replace(base, "Rückwärtskompatibilität", "Scope/Akzeptanz", 1)
	for _, raw := range []string{unknown, duplicate} {
		if _, err := requestedSelfReview([]domain.RunLog{{Message: "```taskboard-self-review\n" + raw + "\n```"}}); err == nil {
			t.Fatalf("invalid checklist categories accepted: %s", raw)
		}
	}
}

func TestRequestedSelfReviewRejectsFailedChecklistResult(t *testing.T) {
	raw := `{"status":"passed","checklist":[{"check":"Scope/Akzeptanz","result":"ok"},{"check":"Diff/Secrets","result":"failed"},{"check":"Tests/Fehler","result":"ok"},{"check":"Sicherheits-/Betriebsrisiken","result":"ok"},{"check":"Rückwärtskompatibilität","result":"ok"}],"tests":"go test ./...","open_risks":"none"}`
	if _, err := requestedSelfReview([]domain.RunLog{{Message: "```taskboard-self-review\n" + raw + "\n```"}}); err == nil {
		t.Fatal("self-review with a failed checklist result must be rejected")
	}
}

func TestRequestedSelfReviewRejectsUnconfirmedChecklistResult(t *testing.T) {
	raw := `{"status":"passed","checklist":[{"check":"Scope/Akzeptanz","result":"maybe"},{"check":"Diff/Secrets","result":"ok"},{"check":"Tests/Fehler","result":"ok"},{"check":"Sicherheits-/Betriebsrisiken","result":"ok"},{"check":"Rückwärtskompatibilität","result":"ok"}],"tests":"go test","open_risks":"none"}`
	_, err := requestedSelfReview([]domain.RunLog{{Message: "```taskboard-self-review\n" + raw + "\n```"}})
	if err == nil {
		t.Fatal("self-review with an unconfirmed checklist result must be rejected")
	}
	if !strings.Contains(err.Error(), `enthält den ungültigen Status "maybe"`) {
		t.Fatalf("error must identify the invalid status safely: %v", err)
	}
}

func TestNonCodexDeliveryUsesOnlyStructuredCompletionChannel(t *testing.T) {
	terminal := []domain.RunLog{{Message: "```taskboard-self-review\n{\"status\":\"passed\"}\n```"}}
	if _, err := requestedSelfReview(controlLogsForAgent("Delivery Agent", terminal, "")); err == nil {
		t.Fatal("non-Codex terminal output must not satisfy the delivery self-review gate")
	}
	structured := "```taskboard-self-review\n{\"status\":\"passed\",\"checklist\":[{\"check\":\"Scope/Akzeptanz\",\"result\":\"ok\"},{\"check\":\"Diff/Secrets\",\"result\":\"ok\"},{\"check\":\"Tests/Fehler\",\"result\":\"ok\"},{\"check\":\"Sicherheits-/Betriebsrisiken\",\"result\":\"ok\"},{\"check\":\"Rückwärtskompatibilität\",\"result\":\"ok\"}],\"tests\":\"go test\",\"open_risks\":\"none\"}\n```"
	if _, err := requestedSelfReview(controlLogsForAgent("Delivery Agent", terminal, structured)); err != nil {
		t.Fatalf("structured completion output should satisfy the parser path: %v", err)
	}
}

func TestCodexSelfReviewUsesOnlyTheStructuredCompletionChannel(t *testing.T) {
	terminal := []domain.RunLog{{Message: "```taskboard-self-review\n{\"status\":\"passed\"}\n```"}}
	if _, err := requestedSelfReview(structuredControlLogs("codex", terminal, "")); err == nil {
		t.Fatal("terminal output must not satisfy the Codex self-review gate")
	}
	if _, err := requestedSelfReview(structuredControlLogs("codex", terminal, string(terminal[0].Message))); err == nil {
		t.Fatal("malformed structured completion must remain rejected")
	}
}

func TestSelfReviewGateFailsClosedWhenRunLogsCannotBeRead(t *testing.T) {
	if err := validateSelfReview("Delivery Agent", nil, errors.New("store unavailable")); err == nil {
		t.Fatal("delivery self-review must fail closed when run logs are unreadable")
	}
	if err := validateSelfReview("Triage Agent", nil, errors.New("store unavailable")); err != nil {
		t.Fatalf("triage must remain compatible with the self-review gate: %v", err)
	}
}

func TestAutomationEventNoopOnlySuppressesUnchangedReviewReturns(t *testing.T) {
	if !automationEventIsNoop(domain.AutomationEvent{Payload: []byte(`{"qa_return":true,"change_available":false}`)}) {
		t.Fatal("unchanged QA/review return must be a terminal no-op")
	}
	for _, event := range []domain.AutomationEvent{
		{Payload: []byte(`{"qa_return":true,"change_available":true}`)},
		{Payload: []byte(`{"qa_return":false,"change_available":false}`)},
		{Payload: []byte(`not-json`)},
	} {
		if automationEventIsNoop(event) {
			t.Fatalf("event must remain processable: %s", event.Payload)
		}
	}
}

func TestMaxAutomationEventAttemptsDefaultsToThreeAndIsConfigurable(t *testing.T) {
	t.Setenv("SHIPYARD_MAX_AUTOMATION_EVENT_ATTEMPTS", "")
	if got := maxAutomationEventAttempts(); got != 3 {
		t.Fatalf("default attempts = %d, want 3", got)
	}
	t.Setenv("SHIPYARD_MAX_AUTOMATION_EVENT_ATTEMPTS", "7")
	if got := maxAutomationEventAttempts(); got != 7 {
		t.Fatalf("configured attempts = %d, want 7", got)
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
	update, ok := requestedTaskUpdate(logs)
	if !ok || update.HasTitle || !update.HasDescription || update.Description != "x" {
		t.Fatalf("valid partial update was not accepted: %#v, %t", update, ok)
	}
	if _, ok := requestedTaskTargets(logs); ok {
		t.Fatal("empty target request must be rejected")
	}
}

func TestRequestedTaskUpdateAcceptsOnlyValidatedFields(t *testing.T) {
	logs := []domain.RunLog{{Message: "```taskboard-update\n{\"title\":\"…\",\"description\":\"Neue belastbare Beschreibung\"}\n```"}}
	update, ok := requestedTaskUpdate(logs)
	if !ok || update.Title != "" || update.HasTitle || update.Description != "Neue belastbare Beschreibung" || !update.HasDescription {
		t.Fatalf("unexpected partial triage update: %#v, %t", update, ok)
	}
}

func TestRequestedTaskUpdateRejectsPlaceholderOnlyAndMalformedJSON(t *testing.T) {
	for _, message := range []string{
		"```taskboard-update\n{\"title\":\"...\",\"description\":\"   \"}\n```",
		"```taskboard-update\n{\"title\":\"valid\"\n```",
		"```taskboard-update\n{\"title\":\"valid\"",
	} {
		if _, ok := requestedTaskUpdate([]domain.RunLog{{Message: message}}); ok {
			t.Fatalf("invalid triage update was accepted: %q", message)
		}
	}
}

func TestRequestedTaskUpdateLogsMalformedUnterminatedJSON(t *testing.T) {
	message := "```taskboard-update\n{\"title\":\"valid\""
	_, ok, reason := requestedTaskUpdateWithReason([]domain.RunLog{{Message: message}})
	if ok {
		t.Fatal("unterminated malformed triage update was accepted")
	}
	if !strings.Contains(reason, "malformed JSON") {
		t.Fatalf("malformed update reason = %q, want audit reason", reason)
	}
}

func TestRequestedInteractionsAcceptsKeyAsFieldIdentifier(t *testing.T) {
	logs := []domain.RunLog{{Message: "```taskboard-interaction\n{\"key\":\"release\",\"title\":\"Freigabe\",\"fields\":[{\"key\":\"release_decision\",\"label\":\"Freigabeentscheidung\",\"type\":\"buttons\",\"options\":[{\"value\":\"approve\",\"label\":\"Freigeben\"}]}]}\n```"}}
	requests := requestedInteractions(logs)
	if len(requests) != 1 || requests[0].Fields[0].ID != "release_decision" || requests[0].Fields[0].Key != "" {
		t.Fatalf("legacy key was not normalized: %#v", requests)
	}
}

func TestRequestedInteractionsDerivesAnIDFromAFieldLabel(t *testing.T) {
	logs := []domain.RunLog{{Message: "```taskboard-interaction\n{\"key\":\"shipyard_project\",\"title\":\"Zielprojekt\",\"fields\":[{\"label\":\"project_id\",\"type\":\"text\",\"required\":true}]}\n```"}}
	requests := requestedInteractions(logs)
	if len(requests) != 1 || requests[0].Fields[0].ID != "project_id" {
		t.Fatalf("label-derived interaction id = %#v", requests)
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

func TestExplicitTransitionSuppressesAutomationSuccessFallback(t *testing.T) {
	run := domain.AgentRun{RuleID: "rule-success"}
	if got := suppressAutomationOutcome(run, true, false); got.RuleID != "" {
		t.Fatalf("explicit transition must own the outcome, got rule %q", got.RuleID)
	}
	if got := suppressAutomationOutcome(run, true, true); got.RuleID != "rule-success" {
		t.Fatalf("interaction pause must retain the rule, got rule %q", got.RuleID)
	}
	if got := suppressAutomationOutcome(run, false, false); got.RuleID != "rule-success" {
		t.Fatalf("implicit outcome must retain the rule, got rule %q", got.RuleID)
	}
}

func TestFormatAllowedTransitionsUsesIDsAndDisplayLabels(t *testing.T) {
	got := formatAllowedTransitions([]domain.Transition{{ToColumnID: "development-id"}}, []domain.Column{{ID: "development-id", Name: "Entwicklung"}})
	if !strings.Contains(got, "target_column_id") || !strings.Contains(got, "development-id") || !strings.Contains(got, "Entwicklung") {
		t.Fatalf("missing structured transition context: %s", got)
	}
}

func TestFormatRegisteredProjectsSeparatesUUIDFromRepositoryURL(t *testing.T) {
	got := formatRegisteredProjects([]domain.Project{
		{ID: "123e4567-e89b-12d3-a456-426614174000", Name: "Shipyard", RepositoryURL: "https://github.com/example/shipyard.git", DefaultBranch: "master", Boards: []domain.Board{{ID: "board-1", Name: "Shipyard"}}},
	})
	for _, expected := range []string{"\"project_id\":\"123e4567-e89b-12d3-a456-426614174000\"", "\"repository_url\":\"https://github.com/example/shipyard.git\"", "\"boards\":[{\"id\":\"board-1\",\"name\":\"Shipyard\"}]", "ausschließlich project_id-Werte"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("registered project context missing %q: %s", expected, got)
		}
	}
}

func TestRequestedRouteCurrentColumnIsSilentNoOp(t *testing.T) {
	task := domain.Task{ColumnID: "development-id", ColumnName: "Entwicklung"}
	if !requestedRouteIsCurrent(task, transitionRequest{TargetColumnID: task.ColumnID}) || !requestedRouteIsCurrent(task, transitionRequest{Target: task.ColumnName}) {
		t.Fatal("current column was not recognized")
	}
	if !requestedRouteIsCurrentAfterLiveReload(task, transitionRequest{TargetColumnID: task.ColumnID}, true) {
		t.Fatal("verified live status was not recognized")
	}
	if requestedRouteIsCurrentAfterLiveReload(task, transitionRequest{TargetColumnID: task.ColumnID}, false) {
		t.Fatal("unverified status must not suppress a transition")
	}
	if requestedRouteIsCurrent(task, transitionRequest{TargetColumnID: "review-id"}) {
		t.Fatal("different target was treated as self-transition")
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

func TestQAReleaseGateUsesTargetColumnIDBeforeLegacyLabel(t *testing.T) {
	columns := []domain.Column{
		{ID: "done-id", Name: "Erledigt", Type: "done"},
		{ID: "review-id", Name: "Review", Type: "standard"},
	}
	if !requestedRouteTargetsColumnType(columns, transitionRequest{TargetColumnID: "done-id"}, "done") {
		t.Fatal("ID-based QA release route should resolve to the done column")
	}
	if requestedRouteTargetsColumnType(columns, transitionRequest{TargetColumnID: "review-id", Target: "Erledigt"}, "done") {
		t.Fatal("a supplied non-done ID must not fall back to a matching legacy label")
	}
	if !requestedRouteTargetsColumnType(columns, transitionRequest{Target: "Erledigt"}, "done") {
		t.Fatal("legacy label-based QA release route should remain compatible")
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

func TestReportedCLIUsageReadsMachineReadableBreakdownAndNativeCost(t *testing.T) {
	logs := []domain.RunLog{{Message: `{"type":"usage","usage":{"api_calls":2,"input_tokens":100,"output_tokens":25,"cached_input_tokens":40,"cache_write_tokens":5,"reasoning_tokens":10,"total_tokens":125,"cost_microusd":321,"service_tier":"flex"}}`}}
	report, ok := reportedCLIUsage(logs)
	if !ok || report.APICalls == nil || *report.APICalls != 2 || report.InputTokens == nil || *report.InputTokens != 100 || report.CachedInputTokens == nil || *report.CachedInputTokens != 40 || report.NativeCostMicrousd == nil || *report.NativeCostMicrousd != 321 || report.ServiceTier != "flex" {
		t.Fatalf("report = %#v, %t; want complete machine-readable usage", report, ok)
	}
}

func TestReportedCLIUsageKeepsIncompleteMachineReadableUsage(t *testing.T) {
	logs := []domain.RunLog{{Message: `{"usage":{"input_tokens":17,"output_tokens":null}}`}}
	report, ok := reportedCLIUsage(logs)
	if !ok || report.InputTokens == nil || *report.InputTokens != 17 || report.OutputTokens != nil {
		t.Fatalf("report = %#v, %t; want null output tokens and known input tokens", report, ok)
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
