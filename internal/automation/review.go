package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"taskboard/internal/domain"
)

// DefaultReviewAgentPrompt is the built-in Code Review template. Defective
// work must be returned on the board's rework edge; a clean accept stays in
// Review for a human QA step instead of auto-completing.
const DefaultReviewAgentPrompt = "Review the change critically against the task acceptance criteria and document concrete issues. Do not create a push, merge, or release. If defects block acceptance, emit self-review status=failed and exactly one taskboard-transition to the board's rework target (Entwicklung / \"Überarbeiten\"). Never leave defective work in Review for a human by default. If the review passes, emit status=passed and do not auto-transition to Erledigt."

const (
	legacyReviewPromptDE = "Prüfe die Änderung kritisch und dokumentiere konkrete Probleme. Erstelle keinen Push, Merge oder Release."
	legacyReviewPromptEN = "Review the change critically and document concrete issues. Do not create a push, merge, or release."
)

var selfReviewFailedResultValues = []string{
	"failed", "fail", "not_met", "not met", "unmet", "nicht erfüllt", "nicht erfuellt",
	"nicht bestanden", "fehlgeschlagen",
}

// unmetAcceptancePhrase catches the SEC-06 failure mode: status=passed while
// the evidence says acceptance was not met. Nearby "acceptance"/"criteria"
// language is required so phrases such as "no unmet risks" stay valid.
var unmetAcceptancePhrase = regexp.MustCompile(`(?i)((acceptance\s+criteria|akzeptanzkriterien|akzeptanz(?:kriterium)?|scope/acceptance|scope/akzeptanz).{0,80}(not met|unmet|nicht erfüllt|nicht erfuellt|nicht bestanden|not satisfied)|(not met|unmet|nicht erfüllt|nicht erfuellt|nicht bestanden).{0,80}(acceptance|akzeptanz|criteria|kriterien))`)

func isReviewAgent(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "review agent" || strings.HasPrefix(n, "review agent ")
}

func isLegacyReviewPrompt(prompt string) bool {
	switch strings.TrimSpace(prompt) {
	case legacyReviewPromptDE, legacyReviewPromptEN:
		return true
	default:
		return false
	}
}

func reviewSelfReviewPromptSection() string {
	labels := make([]string, len(selfReviewChecklist))
	for i, check := range selfReviewChecklist {
		labels[i] = check.PromptLabel
	}
	allowedResults := append(append([]string{}, selfReviewResultValues...), selfReviewFailedResultValues...)
	return "\n\nBefore the final comment or any handoff, output exactly one valid ```taskboard-self-review block. Use status=passed only when acceptance is fully met. If findings block acceptance, status MUST be failed (or equivalent not-passed); status=passed with unmet criteria in details is invalid. Use exactly these five checklist categories: " +
		englishList(labels, "and") +
		". Each checklist result MUST be exactly one of: " +
		englishList(allowedResults, "or") +
		". Put human-readable evidence in the optional details field; never put a sentence in result. On defects you MUST also emit exactly one ```taskboard-transition to the board's rework target. Example of a clean accept: ```taskboard-self-review\n" +
		selfReviewExampleJSON() +
		"\n``` If this block is missing or invalid, nothing is applied and no transition is executed. A valid failed review still applies the rework transition."
}

func reviewAgentWorkflowSection(allowed []domain.Transition, columns []domain.Column, task domain.Task) string {
	var b strings.Builder
	b.WriteString("\n\n--- REVIEW WORKFLOW (platform rules; they override agent prompt text that forbids automatic rework) ---\n")
	b.WriteString("You are the Review agent. Do not create a push, merge, or release.\n")
	b.WriteString("Inspect the Delivery worktree already attached to this run. It is read-only: do not modify files or implement fixes there. Rework is a board transition, not an edit of that tree.\n")
	b.WriteString("If this review finds defects or unmet acceptance criteria: emit self-review status=failed and exactly one taskboard-transition to the board's rework target. Never leave defective work sitting in Review for a human decision.\n")
	b.WriteString("If this review passes: emit self-review status=passed. Do not emit a transition to Erledigt or Done; leave the task in Review for human QA unless a SuccessColumn is configured.\n")
	if route, ok := resolveReworkTransition(allowed, columns, task); ok {
		fmt.Fprintf(&b, "Rework target for this board: {\"target_column_id\":%q,\"target\":%q,\"label\":%q}\n", route.TargetColumnID, route.Target, route.Comment)
	}
	b.WriteString("--- END REVIEW WORKFLOW ---\n")
	return b.String()
}

func selfReviewContradictsPassed(review taskboardSelfReview) bool {
	if strings.ToLower(strings.TrimSpace(review.Status)) != "passed" {
		return false
	}
	parts := []string{string(review.Tests), string(review.OpenRisks)}
	for _, item := range review.Checklist {
		parts = append(parts, item.Details, item.Result, item.Check)
	}
	return unmetAcceptancePhrase.MatchString(strings.Join(parts, "\n"))
}

func reviewFindingsBlockAcceptance(review taskboardSelfReview) bool {
	status := strings.ToLower(strings.TrimSpace(review.Status))
	if status == "failed" || status == "fail" || status == "not_passed" {
		return true
	}
	return selfReviewContradictsPassed(review)
}

func validFailedSelfReviewResult(result string) bool {
	for _, allowed := range selfReviewFailedResultValues {
		if result == allowed {
			return true
		}
	}
	return false
}

func requestedSelfReviewForAgent(agentName string, logs []domain.RunLog) (taskboardSelfReview, error) {
	matches := selfReviewFence.FindAllStringSubmatch(joinRunLogs(logs), -1)
	if len(matches) == 0 {
		return taskboardSelfReview{}, errors.New("kein taskboard-self-review-Block gefunden")
	}
	reviewAgent := isReviewAgent(agentName)
	var lastErr error
	for index := len(matches) - 1; index >= 0; index-- {
		var review taskboardSelfReview
		if err := json.Unmarshal([]byte(matches[index][1]), &review); err != nil {
			lastErr = errors.New("taskboard-self-review ist kein gültiges JSON")
			continue
		}
		if err := validateSelfReviewDocument(review, reviewAgent); err != nil {
			lastErr = err
			continue
		}
		if reviewAgent && selfReviewContradictsPassed(review) {
			review.Status = "failed"
		}
		return review, nil
	}
	if lastErr != nil {
		return taskboardSelfReview{}, lastErr
	}
	return taskboardSelfReview{}, errors.New("kein gültiger taskboard-self-review-Block gefunden")
}

func validateSelfReviewDocument(review taskboardSelfReview, reviewAgent bool) error {
	status := strings.ToLower(strings.TrimSpace(review.Status))
	failedReview := reviewAgent && (status == "failed" || status == "fail" || status == "not_passed")
	if !failedReview && status != "passed" {
		return errors.New("taskboard-self-review muss status=passed enthalten")
	}
	if !failedReview && !reviewAgent && selfReviewContradictsPassed(review) {
		return errors.New("taskboard-self-review status=passed widerspricht nicht erfüllten Akzeptanzkriterien")
	}
	requiredChecks := make(map[string]bool, len(selfReviewChecklist))
	for _, check := range selfReviewChecklist {
		requiredChecks[check.ID] = false
	}
	if len(review.Checklist) != len(requiredChecks) {
		return errors.New("taskboard-self-review benötigt genau die fünf Pflicht-Checklistenpunkte")
	}
	for _, item := range review.Checklist {
		check := canonicalSelfReviewCheck(item.Check)
		if strings.TrimSpace(item.Check) == "" || strings.TrimSpace(item.Result) == "" {
			return errors.New("taskboard-self-review enthält einen unvollständigen Checklistenpunkt")
		}
		result := strings.ToLower(strings.TrimSpace(item.Result))
		if failedReview {
			if !validSelfReviewResult(result) && !validFailedSelfReviewResult(result) {
				return fmt.Errorf("taskboard-self-review Checklistenpunkt %q enthält den ungültigen Status %s (Länge %d); erwartet wird einer von: %s", item.Check, safeSelfReviewResultForError(item.Result), len(strings.TrimSpace(item.Result)), strings.Join(append(append([]string{}, selfReviewResultValues...), selfReviewFailedResultValues...), ", "))
			}
		} else if !validSelfReviewResult(result) {
			return fmt.Errorf("taskboard-self-review Checklistenpunkt %q enthält den ungültigen Status %s (Länge %d); erwartet wird einer von: %s", item.Check, safeSelfReviewResultForError(item.Result), len(strings.TrimSpace(item.Result)), strings.Join(selfReviewResultValues, ", "))
		}
		if _, required := requiredChecks[check]; !required {
			return fmt.Errorf("taskboard-self-review enthält keine gültige Pflichtkategorie %q", item.Check)
		}
		if requiredChecks[check] {
			return fmt.Errorf("taskboard-self-review enthält die Pflichtkategorie %q doppelt", item.Check)
		}
		requiredChecks[check] = true
	}
	for check, present := range requiredChecks {
		if !present {
			return fmt.Errorf("taskboard-self-review fehlt die Pflichtkategorie %q", selfReviewPromptLabel(check))
		}
	}
	if len(review.Tests) == 0 || string(review.Tests) == "null" || len(review.OpenRisks) == 0 || string(review.OpenRisks) == "null" {
		return errors.New("taskboard-self-review benötigt Testnachweise und offene Risiken")
	}
	return nil
}

func resolveReworkTransition(allowed []domain.Transition, columns []domain.Column, task domain.Task) (transitionRequest, bool) {
	byID := make(map[string]domain.Column, len(columns))
	var current domain.Column
	for _, column := range columns {
		byID[column.ID] = column
		if column.ID == task.ColumnID || (task.ColumnID == "" && strings.EqualFold(strings.TrimSpace(column.Name), strings.TrimSpace(task.ColumnName))) {
			current = column
		}
	}
	var fallback transitionRequest
	foundFallback := false
	for _, transition := range allowed {
		target, ok := byID[transition.ToColumnID]
		if !ok || strings.EqualFold(strings.TrimSpace(target.Type), "done") || target.IsTerminal {
			continue
		}
		route := transitionRequest{
			TargetColumnID: transition.ToColumnID,
			Target:         target.Name,
			Comment:        strings.TrimSpace(transition.ActionName),
		}
		if isPreferredReworkTarget(transition, target) {
			return route, true
		}
		if current.ID != "" && target.Position < current.Position && !foundFallback {
			fallback, foundFallback = route, true
			continue
		}
		if !foundFallback {
			fallback, foundFallback = route, true
		}
	}
	return fallback, foundFallback
}

func isPreferredReworkTarget(transition domain.Transition, target domain.Column) bool {
	action := strings.ToLower(strings.TrimSpace(transition.ActionName))
	name := strings.ToLower(strings.TrimSpace(target.Name))
	switch action {
	case "überarbeiten", "uberarbeiten", "nacharbeit anfordern":
		return true
	}
	switch name {
	case "entwicklung", "in progress":
		return true
	}
	return false
}

func isPreferredReworkLabel(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "entwicklung", "in progress", "überarbeiten", "uberarbeiten", "nacharbeit anfordern":
		return true
	default:
		return false
	}
}

func isReworkRoute(columns []domain.Column, task domain.Task, route transitionRequest, allowed []domain.Transition) bool {
	if requestedRouteTargetsColumnType(columns, route, "done") {
		return false
	}
	if isPreferredReworkLabel(route.Target) {
		return true
	}
	resolved, ok := resolveReworkTransition(allowed, columns, task)
	if !ok {
		return false
	}
	if route.TargetColumnID != "" {
		return route.TargetColumnID == resolved.TargetColumnID
	}
	return strings.EqualFold(strings.TrimSpace(route.Target), resolved.Target)
}

// applyReviewReworkPolicy is the worker's Review outcome rule: findings return
// to the rework column, a clean accept never auto-completes, and a missing
// rework edge fails closed instead of leaving defective work in Review.
func applyReviewReworkPolicy(task domain.Task, review taskboardSelfReview, route transitionRequest, hasRoute bool, allowed []domain.Transition, columns []domain.Column) (transitionRequest, bool, error) {
	if !reviewFindingsBlockAcceptance(review) {
		if hasRoute && requestedRouteTargetsColumnType(columns, route, "done") {
			return transitionRequest{}, false, nil
		}
		return route, hasRoute, nil
	}
	if hasRoute && isReworkRoute(columns, task, route, allowed) {
		return route, true, nil
	}
	if resolved, ok := resolveReworkTransition(allowed, columns, task); ok {
		if route.Comment != "" {
			resolved.Comment = route.Comment
		}
		return resolved, true, nil
	}
	return transitionRequest{}, false, errors.New("Review hat Mängel gefunden, aber es gibt keine erlaubte Nacharbeits-Transition")
}
