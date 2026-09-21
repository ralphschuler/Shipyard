package automation

import (
	"strings"
	"taskboard/internal/domain"
	"testing"
)

func passedUnmetCriteriaReviewJSON() string {
	return `{"status":"passed","checklist":[{"check":"Scope/Acceptance","result":"passed","details":"acceptance criteria NOT met; serial login failures still bypass the lock"},{"check":"Diff/Secrets","result":"passed"},{"check":"Tests/Failures","result":"passed"},{"check":"Security/Operational risks","result":"passed"},{"check":"Backward compatibility","result":"passed"}],"tests":"go test ./...","open_risks":"lock bypass"}`
}

func failedFindingsReviewJSON() string {
	return `{"status":"failed","checklist":[{"check":"Scope/Acceptance","result":"failed","details":"acceptance criteria NOT met"},{"check":"Diff/Secrets","result":"passed"},{"check":"Tests/Failures","result":"passed"},{"check":"Security/Operational risks","result":"passed"},{"check":"Backward compatibility","result":"passed"}],"tests":"go test ./...","open_risks":"serial login lock bypass"}`
}

func reviewFence(raw string) []domain.RunLog {
	return []domain.RunLog{{Message: "```taskboard-self-review\n" + raw + "\n```"}}
}

func softwareReviewReworkGraph() (domain.Task, []domain.Transition, []domain.Column) {
	task := domain.Task{ColumnID: "review-id", ColumnName: "Review"}
	columns := []domain.Column{
		{ID: "dev-id", Name: "Entwicklung", Type: "standard", Position: 2},
		{ID: "review-id", Name: "Review", Type: "standard", Position: 3},
		{ID: "done-id", Name: "Erledigt", Type: "done", Position: 4, IsTerminal: true},
	}
	allowed := []domain.Transition{
		{ToColumnID: "dev-id", ActionName: "Überarbeiten"},
		{ToColumnID: "done-id", ActionName: "Abschließen"},
	}
	return task, allowed, columns
}

func TestIsReviewAgentMatchesTriageStylePrefix(t *testing.T) {
	if !isReviewAgent("Review Agent") || !isReviewAgent("Review Agent SEC-06") {
		t.Fatal("Review Agent names must be recognized")
	}
	if isReviewAgent("Delivery Agent") || isReviewAgent("Code review helper") {
		t.Fatal("non-review names must not use Review self-review semantics")
	}
}

func TestRequestedSelfReviewRejectsPassedUnmetCriteriaForDelivery(t *testing.T) {
	_, err := requestedSelfReview(reviewFence(passedUnmetCriteriaReviewJSON()))
	if err == nil {
		t.Fatal("delivery self-review must fail closed when status=passed but acceptance is not met")
	}
	if !strings.Contains(err.Error(), "Akzeptanzkriterien") {
		t.Fatalf("error should name the contradiction, got %v", err)
	}
}

func TestRequestedSelfReviewTreatsPassedUnmetCriteriaAsFailedForReviewAgent(t *testing.T) {
	review, err := requestedSelfReviewForAgent("Review Agent", reviewFence(passedUnmetCriteriaReviewJSON()))
	if err != nil {
		t.Fatalf("review agent contradictory self-review must be treated as failed, not rejected: %v", err)
	}
	if review.Status != "failed" {
		t.Fatalf("status = %q, want failed", review.Status)
	}
	if !reviewFindingsBlockAcceptance(review) {
		t.Fatal("contradictory passed review must block acceptance")
	}
}

func TestRequestedSelfReviewAcceptsFailedFindingsForReviewAgent(t *testing.T) {
	review, err := requestedSelfReviewForAgent("Review Agent", reviewFence(failedFindingsReviewJSON()))
	if err != nil {
		t.Fatalf("review agent failed self-review rejected: %v", err)
	}
	if review.Status != "failed" {
		t.Fatalf("status = %q, want failed", review.Status)
	}
	if err := validateSelfReview("Review Agent", reviewFence(failedFindingsReviewJSON()), nil); err != nil {
		t.Fatalf("review agent failed self-review must satisfy the gate: %v", err)
	}
	if _, err := requestedSelfReview(reviewFence(failedFindingsReviewJSON())); err == nil {
		t.Fatal("delivery self-review must still require status=passed")
	}
}

func TestSelfReviewContradictsPassedIgnoresVerifiedAcceptance(t *testing.T) {
	review := selfReviewExample()
	if selfReviewContradictsPassed(review) {
		t.Fatal("clean accept example must not look like unmet criteria")
	}
	review.Checklist[0].Details = "Scope implemented and acceptance criteria verified."
	if selfReviewContradictsPassed(review) {
		t.Fatal("verified acceptance must still pass")
	}
}

func TestApplyReviewReworkPolicyMovesFindingsToEntwicklung(t *testing.T) {
	task, allowed, columns := softwareReviewReworkGraph()
	review, err := requestedSelfReviewForAgent("Review Agent", reviewFence(failedFindingsReviewJSON()))
	if err != nil {
		t.Fatal(err)
	}
	route, hasRoute, policyErr := applyReviewReworkPolicy(task, review, transitionRequest{}, false, allowed, columns)
	if policyErr != nil || !hasRoute || route.TargetColumnID != "dev-id" || route.Target != "Entwicklung" {
		t.Fatalf("failed review route = %#v hasRoute=%t err=%v, want Entwicklung", route, hasRoute, policyErr)
	}
}

func TestApplyReviewReworkPolicyHonorsExplicitUeberarbeitenTransition(t *testing.T) {
	task, allowed, columns := softwareReviewReworkGraph()
	review, err := requestedSelfReviewForAgent("Review Agent", reviewFence(failedFindingsReviewJSON()))
	if err != nil {
		t.Fatal(err)
	}
	requested := transitionRequest{Target: "Überarbeiten", Comment: "lock still bypassed"}
	route, hasRoute, policyErr := applyReviewReworkPolicy(task, review, requested, true, allowed, columns)
	if policyErr != nil || !hasRoute || route.Target != "Überarbeiten" || route.Comment != "lock still bypassed" {
		t.Fatalf("explicit rework route = %#v hasRoute=%t err=%v", route, hasRoute, policyErr)
	}
}

func TestApplyReviewReworkPolicyPassedReviewDoesNotAutoComplete(t *testing.T) {
	task, allowed, columns := softwareReviewReworkGraph()
	route, hasRoute, err := applyReviewReworkPolicy(task, selfReviewExample(), transitionRequest{TargetColumnID: "done-id", Target: "Erledigt"}, true, allowed, columns)
	if err != nil || hasRoute || route.TargetColumnID != "" {
		t.Fatalf("passed review must not auto-Erledigt: route=%#v hasRoute=%t err=%v", route, hasRoute, err)
	}
	route, hasRoute, err = applyReviewReworkPolicy(task, selfReviewExample(), transitionRequest{}, false, allowed, columns)
	if err != nil || hasRoute {
		t.Fatalf("passed review must stay in Review: route=%#v hasRoute=%t err=%v", route, hasRoute, err)
	}
}

func TestResolveReworkTransitionPrefersUeberarbeiten(t *testing.T) {
	task, allowed, columns := softwareReviewReworkGraph()
	route, ok := resolveReworkTransition(allowed, columns, task)
	if !ok || route.TargetColumnID != "dev-id" || route.Comment != "Überarbeiten" {
		t.Fatalf("rework target = %#v ok=%t, want Entwicklung/Überarbeiten", route, ok)
	}
}

func TestReviewAgentPromptRequiresAutomaticRework(t *testing.T) {
	task, allowed, columns := softwareReviewReworkGraph()
	section := reviewAgentWorkflowSection(allowed, columns, task)
	for _, want := range []string{
		"Never leave defective work sitting in Review",
		"override agent prompt text that forbids automatic rework",
		"target_column_id",
		"dev-id",
		"Entwicklung",
		"do not emit a transition to Erledigt",
	} {
		if !strings.Contains(strings.ToLower(section), strings.ToLower(want)) && !strings.Contains(section, want) {
			t.Fatalf("review workflow section missing %q: %s", want, section)
		}
	}
	prompt := selfReviewPromptSection("Review Agent")
	if !strings.Contains(prompt, "status MUST be failed") || !strings.Contains(prompt, "unmet criteria") {
		t.Fatalf("review self-review prompt missing fail-closed instructions: %s", prompt)
	}
	if !strings.Contains(DefaultReviewAgentPrompt, "Never leave defective work in Review") {
		t.Fatalf("default review prompt missing rework contract: %s", DefaultReviewAgentPrompt)
	}
}

func TestNormalizeBuiltinAgentUpgradesLegacyReviewPrompt(t *testing.T) {
	legacy := domain.Agent{Description: "Code-Review-Agent", Prompt: legacyReviewPromptDE}
	got, changed := normalizeBuiltinAgent(legacy)
	if !changed || got.Prompt != DefaultReviewAgentPrompt || got.Description != "Code review agent" {
		t.Fatalf("legacy German review prompt was not upgraded: %#v changed=%t", got, changed)
	}
	english := domain.Agent{Description: "Code review agent", Prompt: legacyReviewPromptEN}
	got, changed = normalizeBuiltinAgent(english)
	if !changed || got.Prompt != DefaultReviewAgentPrompt {
		t.Fatalf("legacy English review prompt was not upgraded: %#v changed=%t", got, changed)
	}
	custom := domain.Agent{Description: "Code review agent", Prompt: "Leave defects in Review for humans."}
	if _, changed = normalizeBuiltinAgent(custom); changed {
		t.Fatal("custom review prompt must not be rewritten")
	}
	if got := normalizeBuiltinPromptSnapshot(legacyReviewPromptEN); got != DefaultReviewAgentPrompt {
		t.Fatalf("legacy snapshot was not rewritten: %q", got)
	}
}

func TestNegativeReviewAdvancesEscalationStage(t *testing.T) {
	policy := DefaultReworkPolicy()
	first := policy.Select(0, 100)
	second := policy.Select(1, 100)
	if first.Status != "selected" || second.Status != "selected" {
		t.Fatalf("policy stages unavailable: first=%#v second=%#v", first, second)
	}
	if first.Model == second.Model && first.Effort == second.Effort {
		t.Fatalf("rework_count 1 must select a stronger stage than 0: %#v vs %#v", first, second)
	}
}
