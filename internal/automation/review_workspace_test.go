package automation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"taskboard/internal/domain"
	"taskboard/internal/sandbox"
	"testing"
)

func TestAttachReviewSeesUncommittedDeliveryChangesWithoutAcceptedCommit(t *testing.T) {
	ctx := context.Background()
	source, worktree, start := deliveryWorktreeFixture(t, "uncommitted")
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("from delivery\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := gitOutput(ctx, source, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	attached, err := attachReviewToDeliveryWorktree(ctx, source, domain.DeliveryWorktree{
		RunID: "delivery-uncommitted", WorktreePath: worktree, SourceWorkspace: source, StartSHA: start,
	})
	if err != nil {
		t.Fatal(err)
	}
	if attached.Path != worktree {
		t.Fatalf("review path = %s, want delivery worktree %s", attached.Path, worktree)
	}
	body, err := os.ReadFile(filepath.Join(attached.Path, "feature.txt"))
	if err != nil || string(body) != "from delivery\n" {
		t.Fatalf("review did not see the uncommitted delivery file: %q %v", body, err)
	}
	if _, statErr := os.Stat(filepath.Join(source, "feature.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("delivery file leaked into the managed checkout")
	}
	after, err := gitOutput(ctx, worktree, "rev-parse", "HEAD")
	if err != nil || after != before {
		t.Fatalf("review attachment created a commit: before %s after %s err %v", before, after, err)
	}
	status, err := gitOutput(ctx, worktree, "status", "--porcelain")
	if err != nil || !strings.Contains(status, "feature.txt") {
		t.Fatalf("uncommitted delivery change was not preserved: %q %v", status, err)
	}
	summary, err := deliveryChangeSummary(ctx, attached.Path, attached.DiffBase)
	if err != nil || !strings.Contains(summary, "feature.txt") {
		t.Fatalf("review diff summary = %q (%v)", summary, err)
	}
}

func TestAttachReviewSeesBranchOnlyDeliveryCommit(t *testing.T) {
	ctx := context.Background()
	source, worktree, start := deliveryWorktreeFixture(t, "branch-only")
	if err := os.WriteFile(filepath.Join(worktree, "branch.txt"), []byte("committed on the run branch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "add", "branch.txt")
	runGit(t, worktree, "commit", "-m", "delivery branch commit")

	attached, err := attachReviewToDeliveryWorktree(ctx, source, domain.DeliveryWorktree{
		RunID: "delivery-branch", WorktreePath: worktree, SourceWorkspace: source, StartSHA: start,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(attached.Path, "branch.txt"))
	if err != nil || string(body) != "committed on the run branch\n" {
		t.Fatalf("review did not see the branch commit: %q %v", body, err)
	}
	if _, statErr := os.Stat(filepath.Join(source, "branch.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("branch-only delivery file was written to the managed checkout")
	}
	summary, err := deliveryChangeSummary(ctx, attached.Path, attached.DiffBase)
	if err != nil || !strings.Contains(summary, "branch.txt") {
		t.Fatalf("branch diff summary = %q (%v)", summary, err)
	}
}

func TestAttachReviewRejectsMissingOrEmptyDeliveryWorktree(t *testing.T) {
	ctx := context.Background()
	source, worktree, start := deliveryWorktreeFixture(t, "empty")
	_, err := attachReviewToDeliveryWorktree(ctx, source, domain.DeliveryWorktree{
		RunID: "missing", WorktreePath: filepath.Join(t.TempDir(), "gone"), SourceWorkspace: source, StartSHA: start,
	})
	if !errors.Is(err, errReviewWorktreeMissing) || !strings.Contains(err.Error(), "erneut ausführen") {
		t.Fatalf("missing worktree error = %v", err)
	}
	_, err = attachReviewToDeliveryWorktree(ctx, source, domain.DeliveryWorktree{
		RunID: "master", WorktreePath: source, SourceWorkspace: source, StartSHA: start,
	})
	if !errors.Is(err, errReviewWorktreeMissing) {
		t.Fatalf("managed checkout was accepted as a review workspace: %v", err)
	}
	_, err = attachReviewToDeliveryWorktree(ctx, source, domain.DeliveryWorktree{
		RunID: "clean", WorktreePath: worktree, SourceWorkspace: source, StartSHA: start,
	})
	if !errors.Is(err, errReviewNoTaskDiff) || !strings.Contains(err.Error(), "aufgabenspezifische") {
		t.Fatalf("empty delivery diff error = %v", err)
	}
}

func TestAttachReviewRejectsForeignRepository(t *testing.T) {
	ctx := context.Background()
	source, worktree, start := deliveryWorktreeFixture(t, "foreign")
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	runGit(t, other, "init", "-b", "master")
	runGit(t, other, "config", "user.name", "Test")
	runGit(t, other, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "other.txt"), []byte("other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "add", "other.txt")
	runGit(t, other, "commit", "-m", "other")

	_, err := attachReviewToDeliveryWorktree(ctx, other, domain.DeliveryWorktree{
		RunID: "foreign", WorktreePath: worktree, SourceWorkspace: source, StartSHA: start,
	})
	if !errors.Is(err, errReviewWorktreeSource) {
		t.Fatalf("foreign worktree error = %v", err)
	}
}

func TestReviewAttachmentSandboxIsReadOnly(t *testing.T) {
	writable := sandbox.Profile{Name: "strict", Mounts: []string{"worktree"}, NetworkMode: "none", WriteMode: "worktree", Active: true}
	got := reviewAttachmentSandbox(writable)
	if got.WriteMode != "readonly" || got.NetworkMode != "none" || got.Name != "strict" {
		t.Fatalf("writable profile was not constrained: %#v", got)
	}
	already := reviewAttachmentSandbox(sandbox.Profile{Name: "qa-readonly", NetworkMode: "qa-network", WriteMode: "readonly"})
	if already.WriteMode != "readonly" || already.NetworkMode != "qa-network" {
		t.Fatalf("readonly profile changed: %#v", already)
	}
}

func TestReviewDiffFallbackUsesDeliveryWorktree(t *testing.T) {
	delivery := domain.DeliveryWorktree{RunID: "d1", WorktreePath: "/runs/delivery", StartSHA: "abc"}
	path, base, ok := reviewDiffFallback("", "", true, delivery, true)
	if !ok || path != "/runs/delivery" || base != "abc" {
		t.Fatalf("fallback = %s %s %t", path, base, ok)
	}
	path, base, ok = reviewDiffFallback("/runs/review", "own", true, delivery, true)
	if !ok || path != "/runs/review" || base != "own" {
		t.Fatalf("private worktree was replaced: %s %s %t", path, base, ok)
	}
	if _, _, ok = reviewDiffFallback("", "", false, delivery, true); ok {
		t.Fatal("non-review run used the delivery worktree")
	}
}

func deliveryWorktreeFixture(t *testing.T, name string) (source, worktree, start string) {
	t.Helper()
	source = t.TempDir()
	runGit(t, source, "init", "-b", "master")
	runGit(t, source, "config", "user.name", "Test")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "base.txt")
	runGit(t, source, "commit", "-m", "base")
	start, err := gitOutput(context.Background(), source, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	worktree = filepath.Join(t.TempDir(), name)
	runGit(t, source, "worktree", "add", "-b", "agent/run-"+name, worktree, "HEAD")
	return source, worktree, start
}
