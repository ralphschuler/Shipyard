package automation

import "testing"

func TestReworkPolicyFromJSONUsesConfiguredStages(t *testing.T) {
	policy, err := ReworkPolicyFromJSON(`{"version":"custom-v1","stages":[{"model":"nova","effort":"medium"},{"model":"nova","effort":"high"}],"human_escalation_after":4}`)
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	if got := policy.Select(1, 100); got.Model != "nova" || got.Effort != "high" || got.ReworkNumber != 1 {
		t.Fatalf("configured stage = %#v, want nova/high at stage 1", got)
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
