package automation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"taskboard/internal/domain"
	"taskboard/internal/sandbox"
)

var (
	errReviewDeliveryMissing = errors.New("Kein erfolgreicher Delivery-Lauf für diese Aufgabe gefunden. Bitte Delivery erneut ausführen, bevor Review starten kann.")
	errReviewWorktreeMissing = errors.New("Der Delivery-Worktree für diese Aufgabe ist nicht mehr verfügbar. Bitte Delivery erneut ausführen, bevor Review die Änderungen prüfen kann.")
	errReviewNoTaskDiff      = errors.New("Review findet keine aufgabenspezifische Änderung im Delivery-Worktree. Bitte Delivery erneut ausführen.")
	errReviewWorktreeSource  = errors.New("Der Delivery-Worktree gehört nicht zum Projekt-Checkout dieses Reviews. Bitte Delivery erneut ausführen.")
	errReviewStartMissing    = errors.New("Der Delivery-Start-Commit ist nicht mehr verfügbar. Bitte Delivery erneut ausführen.")
)

type reviewWorkspace struct {
	Path          string
	DiffBase      string
	DeliveryRunID string
}

// prepareReviewWorkspace points a Review run at the latest succeeded Delivery
// worktree. It does not commit, apply, or create a checkout from master.
func (w *Worker) prepareReviewWorkspace(ctx context.Context, run domain.AgentRun) (reviewWorkspace, error) {
	delivery, found, err := w.Store.LatestSucceededDeliveryWorktree(ctx, run.TaskID, run.TargetProject)
	if err != nil {
		return reviewWorkspace{}, fmt.Errorf("Delivery-Lauf konnte nicht geladen werden: %w", err)
	}
	if !found {
		return reviewWorkspace{}, errReviewDeliveryMissing
	}
	source := strings.TrimSpace(run.WorkspaceSnapshot)
	if source == "" {
		source = strings.TrimSpace(delivery.SourceWorkspace)
	}
	return attachReviewToDeliveryWorktree(ctx, source, delivery)
}

// attachReviewToDeliveryWorktree validates that path is a linked worktree of
// the Review run's project repository and that it still contains this
// Delivery's uncommitted or branch-only changes.
func attachReviewToDeliveryWorktree(ctx context.Context, reviewSource string, delivery domain.DeliveryWorktree) (reviewWorkspace, error) {
	reviewSource = filepath.Clean(strings.TrimSpace(reviewSource))
	path := filepath.Clean(strings.TrimSpace(delivery.WorktreePath))
	if reviewSource == "" || path == "" || !filepath.IsAbs(reviewSource) || !filepath.IsAbs(path) {
		return reviewWorkspace{}, errReviewWorktreeMissing
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() || samePath(path, reviewSource) {
		return reviewWorkspace{}, errReviewWorktreeMissing
	}
	root, err := gitRepositoryRoot(ctx, path)
	if err != nil || samePath(root, path) {
		return reviewWorkspace{}, errReviewWorktreeMissing
	}
	sourceCommon, sourceErr := absoluteGitCommonDir(ctx, reviewSource)
	rootCommon, rootErr := absoluteGitCommonDir(ctx, root)
	if sourceErr != nil || rootErr != nil || filepath.Clean(sourceCommon) != filepath.Clean(rootCommon) {
		return reviewWorkspace{}, errReviewWorktreeSource
	}
	if !worktreePathRegistered(ctx, root, path) {
		return reviewWorkspace{}, errReviewWorktreeMissing
	}
	base, err := reviewDiffBase(ctx, reviewSource, path, delivery.StartSHA)
	if err != nil {
		return reviewWorkspace{}, err
	}
	summary, err := deliveryChangeSummary(ctx, path, base)
	if err != nil {
		return reviewWorkspace{}, fmt.Errorf("Delivery-Diff konnte nicht gelesen werden: %w", err)
	}
	if strings.TrimSpace(summary) == "" {
		return reviewWorkspace{}, errReviewNoTaskDiff
	}
	return reviewWorkspace{Path: path, DiffBase: base, DeliveryRunID: delivery.RunID}, nil
}

func reviewDiffBase(ctx context.Context, source, worktree, startSHA string) (string, error) {
	startSHA = strings.TrimSpace(startSHA)
	if startSHA != "" {
		resolved, err := gitOutput(ctx, worktree, "rev-parse", "--verify", startSHA+"^{commit}")
		if err != nil {
			return "", errReviewStartMissing
		}
		return resolved, nil
	}
	sourceHEAD, err := gitOutput(ctx, source, "rev-parse", "HEAD")
	if err != nil {
		return "", errReviewWorktreeMissing
	}
	base, err := gitOutput(ctx, worktree, "merge-base", "HEAD", sourceHEAD)
	if err != nil || strings.TrimSpace(base) == "" {
		return sourceHEAD, nil
	}
	return strings.TrimSpace(base), nil
}

func deliveryChangeSummary(ctx context.Context, worktree, base string) (string, error) {
	stat, err := exec.CommandContext(ctx, "git", "-C", worktree, "diff", "--stat", base).Output()
	if err != nil {
		return "", err
	}
	summary := strings.TrimSpace(string(stat))
	untracked, untrackedErr := gitOutput(ctx, worktree, "ls-files", "--others", "--exclude-standard")
	if untrackedErr != nil {
		return "", untrackedErr
	}
	untracked = strings.TrimSpace(untracked)
	if untracked != "" {
		if summary != "" {
			summary += "\n"
		}
		summary += untracked
	}
	return summary, nil
}

func worktreePathRegistered(ctx context.Context, source, path string) bool {
	for _, candidate := range listedWorktreePaths(ctx, source) {
		if samePath(candidate, path) {
			return true
		}
	}
	return false
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if left == right {
		return true
	}
	leftResolved, leftErr := filepath.EvalSymlinks(left)
	rightResolved, rightErr := filepath.EvalSymlinks(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(leftResolved) == filepath.Clean(rightResolved)
}

// reviewAttachmentSandbox makes the shared Delivery worktree read-only for
// Review. Writable built-in profiles cannot change WriteMode (ValidateProfile
// rejects that), so they are replaced by the built-in qa-readonly profile.
// An already-readonly profile is left unchanged, including a custom network
// policy. A custom writable profile keeps its network mode and only switches
// WriteMode.
func reviewAttachmentSandbox(policy sandbox.Profile) sandbox.Profile {
	if policy.WriteMode == "readonly" {
		return policy
	}
	if builtin, err := sandbox.Get(policy.Name); err == nil && builtin.WriteMode != "readonly" {
		if readonly, getErr := sandbox.Get("qa-readonly"); getErr == nil {
			return readonly
		}
	}
	policy.WriteMode = "readonly"
	return policy
}

func (w *Worker) reviewDiffWorkspace(ctx context.Context, runID, startSHA string) (string, string, bool) {
	run, err := w.Store.Run(ctx, runID)
	if err != nil {
		return "", "", false
	}
	agent, err := w.Store.GetAgent(ctx, run.AgentID)
	if err != nil || !isReviewAgent(agent.Name) {
		return "", "", false
	}
	delivery, found, err := w.Store.LatestSucceededDeliveryWorktree(ctx, run.TaskID, run.TargetProject)
	if err != nil || !found {
		return "", "", false
	}
	path, base, ok := reviewDiffFallback("", startSHA, true, delivery, true)
	if !ok {
		return "", "", false
	}
	info, statErr := os.Stat(path)
	if statErr != nil || !info.IsDir() {
		return "", "", false
	}
	return path, base, true
}

// reviewDiffFallback lets a Review run page show the Delivery patch when the
// Review run itself has no private worktree to clean up.
func reviewDiffFallback(ownPath, ownStart string, reviewAgent bool, delivery domain.DeliveryWorktree, found bool) (string, string, bool) {
	if strings.TrimSpace(ownPath) != "" {
		return ownPath, ownStart, true
	}
	if !reviewAgent || !found || strings.TrimSpace(delivery.WorktreePath) == "" {
		return "", "", false
	}
	base := strings.TrimSpace(ownStart)
	if base == "" {
		base = strings.TrimSpace(delivery.StartSHA)
	}
	return delivery.WorktreePath, base, true
}
