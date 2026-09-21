package automation

import (
	"context"
	"strings"
	"taskboard/internal/domain"
	"testing"
)

func TestReworkPolicyFromJSONUsesConfiguredStages(t *testing.T) {
	policy, err := ReworkPolicyFromJSON(`{"version":"custom-v1","stages":[{"model":"nova","effort":"medium"},{"model":"nova","effort":"high"}],"human_escalation_after":4}`)
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	if policy.Stages[1].Model != "nova" || policy.Stages[1].Effort != "high" {
		t.Fatalf("configured stages = %#v", policy.Stages)
	}
	if len(policy.Capabilities["nova"]) != 0 {
		t.Fatalf("stages must not self-confirm capabilities: %#v", policy.Capabilities)
	}
	if got := policy.Select(1, 100); got.Status != "paused" {
		t.Fatalf("unconfirmed custom stage = %#v, want paused", got)
	}
}

func TestReworkPolicyFromJSONLoadsBudgetAndTariffs(t *testing.T) {
	policy, err := ReworkPolicyFromJSON(`{"stages":[{"model":"gpt-test","effort":"high"}],"budget_microusd":0,"estimated_cost_microusd":{"gpt-test/high":25}}`)
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	if !policy.BudgetConfigured || policy.BudgetMicrousd != 0 {
		t.Fatalf("zero budget was not stored: %#v", policy)
	}
	if !policy.TariffsConfigured || policy.EstimatedCostMicrousd["gpt-test/high"] != 25 {
		t.Fatalf("tariff was not stored: %#v", policy.EstimatedCostMicrousd)
	}
}

func TestReworkPolicyFromJSONRejectsNegativeBudget(t *testing.T) {
	if _, err := ReworkPolicyFromJSON(`{"stages":[{"model":"gpt-test","effort":"high"}],"budget_microusd":-1}`); err == nil {
		t.Fatal("negative budget must be rejected")
	}
}

func TestReworkPolicyNegativeBudgetMeansUnlimited(t *testing.T) {
	decision := DefaultReworkPolicy().Select(1, -1)
	if decision.Status != "selected" || decision.Effort != "high" {
		t.Fatalf("unlimited budget decision = %#v, want selected high", decision)
	}
}

func TestReworkPolicySelectsConfiguredSequence(t *testing.T) {
	policy := DefaultReworkPolicy()
	want := []ModelEffort{{"luna", "medium"}, {"luna", "high"}, {"luna", "xhigh"}, {"terra", "medium"}, {"terra", "high"}, {"terra", "xhigh"}, {"soul", "high"}}
	for rework, expected := range want {
		decision := policy.Select(rework, 100)
		if decision.Status != "selected" || decision.Model != expected.Model || decision.Effort != expected.Effort {
			t.Fatalf("rework %d = %#v, want %s/%s", rework, decision, expected.Model, expected.Effort)
		}
	}
}

func TestReworkPolicyFallsBackWithoutEscalatingCapability(t *testing.T) {
	policy := DefaultReworkPolicy()
	policy.Capabilities = map[string][]string{"luna": {"medium", "high"}}
	decision := policy.Select(2, 100)
	if decision.Status != "selected" || decision.Model != "luna" || decision.Effort != "high" || decision.Fallback != "luna/high" {
		t.Fatalf("decision = %#v, want safe luna/high fallback", decision)
	}
}

func TestReworkPolicyStopsAtHumanThresholdAndBudget(t *testing.T) {
	policy := DefaultReworkPolicy()
	if decision := policy.Select(policy.HumanEscalationAfter, 100); decision.Status != "human" {
		t.Fatalf("human decision = %#v", decision)
	}
	if decision := policy.Select(0, 0); decision.Status != "budget" {
		t.Fatalf("budget decision = %#v", decision)
	}
}

func TestReworkPolicyPausesWhenTariffIsMissing(t *testing.T) {
	policy := DefaultReworkPolicy()
	policy.EstimatedCostMicrousd = map[string]int64{}
	if decision := policy.Select(0, 50); decision.Status != "paused" || !strings.Contains(decision.Reason, "cost tariff") {
		t.Fatalf("missing tariff decision = %#v", decision)
	}
}

func confirmedDiscovery() CapabilityDiscovery {
	return CapabilityDiscovery{
		Models:  []string{"gpt-test"},
		Efforts: []string{"high"},
		Source:  "test discovery",
	}
}

func TestResolveReworkSelectionBlocksZeroBudget(t *testing.T) {
	policy, err := ReworkPolicyFromJSON(`{"stages":[{"model":"gpt-test","effort":"high"}],"budget_microusd":0,"estimated_cost_microusd":{"gpt-test/high":25}}`)
	if err != nil {
		t.Fatal(err)
	}
	decision := ResolveReworkSelection(policy, domain.ReworkPolicyState{}, 1, confirmedDiscovery())
	if decision.Status != "budget" {
		t.Fatalf("zero budget = %#v, want budget stop", decision)
	}
	if decision.BudgetLimitMicrousd != 0 || decision.EstimatedCostMicrousd != 25 {
		t.Fatalf("snapshot missing limit/cost: %#v", decision)
	}
	if !strings.Contains(decision.BudgetDecision, "limit_microusd=0") || !strings.Contains(decision.BudgetDecision, "estimated_cost_microusd=25") {
		t.Fatalf("budget decision snapshot = %q", decision.BudgetDecision)
	}
	if strings.Contains(decision.BudgetDecision, "-1") {
		t.Fatalf("budget decision leaked unlimited sentinel: %q", decision.BudgetDecision)
	}
}

func TestResolveReworkSelectionRejectsUnconfirmedCapability(t *testing.T) {
	policy, err := ReworkPolicyFromJSON(`{"stages":[{"model":"not-in-provider-discovery","effort":"high"}],"budget_microusd":100,"estimated_cost_microusd":{"not-in-provider-discovery/high":10}}`)
	if err != nil {
		t.Fatal(err)
	}
	decision := ResolveReworkSelection(policy, domain.ReworkPolicyState{}, 1, confirmedDiscovery())
	if decision.Status != "paused" {
		t.Fatalf("unconfirmed capability = %#v, want paused", decision)
	}
	if !strings.Contains(decision.Reason, "not-in-provider-discovery") && !strings.Contains(decision.Reason, "unsupported") && !strings.Contains(decision.Reason, "unconfirmed") {
		t.Fatalf("capability rejection reason = %q", decision.Reason)
	}
}

func TestResolveReworkSelectionUsesStoredBudgetInsteadOfUnlimited(t *testing.T) {
	policy, err := ReworkPolicyFromJSON(`{"stages":[{"model":"gpt-test","effort":"high"}],"estimated_cost_microusd":{"gpt-test/high":40}}`)
	if err != nil {
		t.Fatal(err)
	}
	stored := domain.ReworkPolicyState{BudgetLimitMicrousd: 100, EstimatedCostMicrousd: map[string]int64{"gpt-test/high": 40}}
	decision := ResolveReworkSelection(policy, stored, 1, confirmedDiscovery())
	if decision.Status != "selected" || decision.BudgetLimitMicrousd != 100 || decision.EstimatedCostMicrousd != 40 {
		t.Fatalf("stored budget selection = %#v", decision)
	}
	if !strings.Contains(decision.BudgetDecision, "allowed") || strings.Contains(decision.BudgetDecision, "-1") {
		t.Fatalf("budget decision = %q", decision.BudgetDecision)
	}
	stored.BudgetLimitMicrousd = 10
	if got := ResolveReworkSelection(policy, stored, 1, confirmedDiscovery()); got.Status != "budget" {
		t.Fatalf("exhausted stored budget = %#v", got)
	}
}

func TestResolveReworkSelectionKeepsHumanThresholdBeforeDiscovery(t *testing.T) {
	policy, err := ReworkPolicyFromJSON(`{"stages":[{"model":"gpt-test","effort":"high"}],"human_escalation_after":2,"budget_microusd":100}`)
	if err != nil {
		t.Fatal(err)
	}
	decision := ResolveReworkSelection(policy, domain.ReworkPolicyState{}, 2, CapabilityDiscovery{Error: "discovery unavailable"})
	if decision.Status != "human" {
		t.Fatalf("human threshold = %#v", decision)
	}
}

func TestResolveReworkSelectionPausesWhenDiscoveryIsUncertain(t *testing.T) {
	policy, err := ReworkPolicyFromJSON(`{"stages":[{"model":"gpt-test","effort":"high"}],"budget_microusd":100,"estimated_cost_microusd":{"gpt-test/high":10}}`)
	if err != nil {
		t.Fatal(err)
	}
	decision := ResolveReworkSelection(policy, domain.ReworkPolicyState{}, 1, CapabilityDiscovery{Error: "Discovery nicht erreichbar"})
	if decision.Status != "paused" {
		t.Fatalf("uncertain discovery = %#v", decision)
	}
}

func TestWorkerEvaluateReworkSelectionLifecycle(t *testing.T) {
	providerCalls := 0
	w := &Worker{
		discoverCapabilities: func(context.Context, domain.ProviderSetting) CapabilityDiscovery {
			providerCalls++
			return confirmedDiscovery()
		},
	}
	provider := domain.ProviderSetting{Provider: "codex"}

	budgetDecision, err := w.evaluateReworkSelection(context.Background(), domain.Task{ReworkCount: 1}, domain.Agent{
		EscalationPolicy: `{"stages":[{"model":"gpt-test","effort":"high"}],"budget_microusd":0,"estimated_cost_microusd":{"gpt-test/high":25}}`,
	}, provider)
	if err != nil || budgetDecision.Status != "budget" {
		t.Fatalf("zero budget worker selection = %#v err=%v", budgetDecision, err)
	}

	capabilityDecision, err := w.evaluateReworkSelection(context.Background(), domain.Task{ReworkCount: 1}, domain.Agent{
		EscalationPolicy: `{"stages":[{"model":"not-in-provider-discovery","effort":"high"}],"budget_microusd":100,"estimated_cost_microusd":{"not-in-provider-discovery/high":10}}`,
	}, provider)
	if err != nil || capabilityDecision.Status != "paused" {
		t.Fatalf("capability worker selection = %#v err=%v", capabilityDecision, err)
	}

	selected, err := w.evaluateReworkSelection(context.Background(), domain.Task{ReworkCount: 1}, domain.Agent{
		EscalationPolicy: `{"version":"delivery-v1","stages":[{"model":"gpt-test","effort":"high"}],"budget_microusd":100,"estimated_cost_microusd":{"gpt-test/high":25}}`,
	}, provider)
	if err != nil || selected.Status != "selected" || selected.Model != "gpt-test" || selected.BudgetLimitMicrousd != 100 || selected.EstimatedCostMicrousd != 25 {
		t.Fatalf("allowed worker selection = %#v err=%v", selected, err)
	}
	if selected.BudgetDecision != "allowed limit_microusd=100 estimated_cost_microusd=25" {
		t.Fatalf("selection snapshot = %q", selected.BudgetDecision)
	}
	if providerCalls == 0 {
		t.Fatal("worker selection must consult provider discovery")
	}
}

func TestWorkerEvaluateReworkSelectionAdvancesConfiguredStagesUntilCap(t *testing.T) {
	w := &Worker{discoverCapabilities: func(context.Context, domain.ProviderSetting) CapabilityDiscovery {
		return CapabilityDiscovery{
			Models:  []string{"gpt-a", "gpt-b"},
			Efforts: []string{"medium", "high", "xhigh"},
			Source:  "test discovery",
		}
	}}
	provider := domain.ProviderSetting{Provider: "codex"}
	policy := `{"version":"ladder-v1","stages":[{"model":"gpt-a","effort":"medium"},{"model":"gpt-a","effort":"high"},{"model":"gpt-b","effort":"xhigh"}],"human_escalation_after":4,"budget_microusd":100,"estimated_cost_microusd":{"gpt-a/medium":10,"gpt-a/high":20,"gpt-b/xhigh":40}}`
	agent := domain.Agent{EscalationPolicy: policy}
	want := []struct {
		count                 int
		status, model, effort string
	}{
		{1, "selected", "gpt-a", "medium"},
		{2, "selected", "gpt-a", "high"},
		{3, "selected", "gpt-b", "xhigh"},
		{4, "human", "", ""},
	}
	for _, test := range want {
		decision, err := w.evaluateReworkSelection(context.Background(), domain.Task{ReworkCount: test.count}, agent, provider)
		if err != nil {
			t.Fatalf("rework %d: %v", test.count, err)
		}
		if decision.Status != test.status || decision.ReworkNumber != test.count {
			t.Fatalf("rework %d = %#v, want status=%s number=%d", test.count, decision, test.status, test.count)
		}
		if test.status == "selected" && (decision.Model != test.model || decision.Effort != test.effort) {
			t.Fatalf("rework %d selected %#v, want %s/%s", test.count, decision, test.model, test.effort)
		}
	}
	capped, err := w.evaluateReworkSelection(context.Background(), domain.Task{ReworkCount: 2}, domain.Agent{
		EscalationPolicy: `{"version":"cap-v1","stages":[{"model":"gpt-a","effort":"medium"},{"model":"gpt-a","effort":"high"}],"human_escalation_after":7,"budget_microusd":100,"estimated_cost_microusd":{"gpt-a/medium":10,"gpt-a/high":20}}`,
	}, provider)
	if err != nil || capped.Status != "selected" || capped.Model != "gpt-a" || capped.Effort != "high" {
		t.Fatalf("further rework must stay on last stage: %#v err=%v", capped, err)
	}
	overBudget, err := w.evaluateReworkSelection(context.Background(), domain.Task{ReworkCount: 2}, domain.Agent{
		EscalationPolicy: `{"version":"budget-v1","stages":[{"model":"gpt-a","effort":"medium"},{"model":"gpt-a","effort":"high"}],"budget_microusd":15,"estimated_cost_microusd":{"gpt-a/medium":10,"gpt-a/high":20}}`,
	}, provider)
	if err != nil || overBudget.Status != "budget" {
		t.Fatalf("over-budget later stage = %#v err=%v, want budget stop", overBudget, err)
	}
}

func TestResolveAgentRunSelectionUsesPolicyStageNotHardcodedZero(t *testing.T) {
	w := &Worker{discoverCapabilities: func(context.Context, domain.ProviderSetting) CapabilityDiscovery {
		return CapabilityDiscovery{Models: []string{"gpt-a", "gpt-b"}, Efforts: []string{"medium", "high"}, Source: "test discovery"}
	}}
	provider := domain.ProviderSetting{Provider: "codex"}
	agent := domain.Agent{
		Model:            "gpt-a",
		ReasoningEffort:  "medium",
		EscalationPolicy: `{"version":"delivery-v1","stages":[{"model":"gpt-a","effort":"medium"},{"model":"gpt-b","effort":"high"}],"budget_microusd":100,"estimated_cost_microusd":{"gpt-a/medium":10,"gpt-b/high":40}}`,
	}
	model, effort, selection, _, err := w.resolveAgentRunSelection(context.Background(), domain.Task{}, nil, agent, provider)
	if err != nil || model != "gpt-a" || effort != "medium" || selection.Stage != "0" || selection.PolicyVersion != "agent-selection" {
		t.Fatalf("initial run selection = %s/%s %#v err=%v", model, effort, selection, err)
	}
	model, effort, selection, decision, err := w.resolveAgentRunSelection(context.Background(), domain.Task{ReworkCount: 1}, nil, agent, provider)
	if err != nil || decision.Status != "selected" || model != "gpt-a" || effort != "medium" {
		t.Fatalf("first rework selection = %s/%s %#v err=%v", model, effort, decision, err)
	}
	if selection.Stage != "1" || selection.PolicyVersion != "delivery-v1" {
		t.Fatalf("first rework snapshot = %#v, want stage 1 from delivery-v1", selection)
	}
	model, effort, selection, decision, err = w.resolveAgentRunSelection(context.Background(), domain.Task{ReworkCount: 2}, nil, agent, provider)
	if err != nil || decision.Status != "selected" || model != "gpt-b" || effort != "high" || selection.Stage != "2" {
		t.Fatalf("second rework selection = %s/%s %#v stage=%s err=%v", model, effort, decision, selection.Stage, err)
	}
}

func TestOverlayProbeUnknownModelAndZeroBudgetDoesNotSelect(t *testing.T) {
	policy, err := ReworkPolicyFromJSON(`{"stages":[{"model":"not-in-provider-discovery","effort":"high"}],"budget_microusd":0}`)
	if err != nil {
		t.Fatalf("policy with zero budget and unknown model must still parse: %v", err)
	}
	w := &Worker{discoverCapabilities: func(context.Context, domain.ProviderSetting) CapabilityDiscovery {
		return confirmedDiscovery()
	}}
	decision, err := w.evaluateReworkSelection(context.Background(), domain.Task{ReworkCount: 1}, domain.Agent{EscalationPolicy: `{"stages":[{"model":"not-in-provider-discovery","effort":"high"}],"budget_microusd":0}`}, domain.ProviderSetting{Provider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status == "selected" || policy.BudgetMicrousd != 0 {
		t.Fatalf("overlay probe must stop selection: policy=%#v decision=%#v", policy, decision)
	}
}
