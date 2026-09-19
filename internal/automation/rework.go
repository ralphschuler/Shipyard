package automation

import (
	"encoding/json"
	"fmt"
)

// ModelEffort is the provider-neutral choice passed to an agent run.
type ModelEffort struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

type ReworkStage struct {
	Model, Effort string
}

// ReworkPolicy is intentionally data-only so boards can persist and edit it
// without changing the escalation algorithm.
type ReworkPolicy struct {
	Version               string
	Stages                []ReworkStage
	Capabilities          map[string][]string
	EstimatedCostMicrousd map[string]int64
	HumanEscalationAfter  int
}

type ReworkDecision struct {
	Status, Model, Effort, Fallback, Reason string
	ReworkNumber                            int
	PolicyVersion                           string
	EstimatedCostMicrousd                   int64
}

// ReworkPolicyFromJSON validates the operator-editable policy format. Missing
// capabilities are inferred from the stages, keeping the editor concise while
// still making unsupported model/effort combinations explicit at run time.
func ReworkPolicyFromJSON(raw string) (ReworkPolicy, error) {
	policy := DefaultReworkPolicy()
	if raw == "" || raw == "{}" {
		return policy, nil
	}
	var input struct {
		Version              string              `json:"version"`
		Stages               []ReworkStage       `json:"stages"`
		Capabilities         map[string][]string `json:"capabilities"`
		HumanEscalationAfter int                 `json:"human_escalation_after"`
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return ReworkPolicy{}, fmt.Errorf("Eskalationspolicy muss gültiges JSON sein: %w", err)
	}
	if len(input.Stages) == 0 {
		return ReworkPolicy{}, fmt.Errorf("Eskalationspolicy benötigt mindestens eine Modell-/Effort-Stufe")
	}
	policy.Version = input.Version
	if policy.Version == "" {
		policy.Version = "custom-v1"
	}
	policy.Stages = input.Stages
	policy.Capabilities = input.Capabilities
	if policy.Capabilities == nil {
		policy.Capabilities = map[string][]string{}
	}
	providedCapabilities := len(input.Capabilities) > 0
	for _, stage := range policy.Stages {
		if stage.Model == "" || !validEffort(stage.Effort) {
			return ReworkPolicy{}, fmt.Errorf("ungültige Modell-/Effort-Stufe %q/%q", stage.Model, stage.Effort)
		}
		if !providedCapabilities {
			if !contains(policy.Capabilities[stage.Model], stage.Effort) {
				policy.Capabilities[stage.Model] = append(policy.Capabilities[stage.Model], stage.Effort)
			}
		} else if len(policy.Capabilities[stage.Model]) == 0 {
			policy.Capabilities[stage.Model] = append(policy.Capabilities[stage.Model], stage.Effort)
		} else if !contains(policy.Capabilities[stage.Model], stage.Effort) {
			return ReworkPolicy{}, fmt.Errorf("Modell %q unterstützt Effort %q laut Policy nicht", stage.Model, stage.Effort)
		}
	}
	if input.HumanEscalationAfter > 0 {
		policy.HumanEscalationAfter = input.HumanEscalationAfter
	}
	return policy, nil
}

func validEffort(value string) bool {
	switch value {
	case "low", "medium", "high", "xhigh":
		return true
	default:
		return false
	}
}

func DefaultReworkPolicy() ReworkPolicy {
	return ReworkPolicy{
		Version: "rework-v1",
		Stages: []ReworkStage{
			{Model: "luna", Effort: "medium"}, {Model: "luna", Effort: "high"},
			{Model: "luna", Effort: "xhigh"}, {Model: "terra", Effort: "medium"},
			{Model: "terra", Effort: "high"}, {Model: "terra", Effort: "xhigh"},
			{Model: "soul", Effort: "high"},
		},
		Capabilities: map[string][]string{
			"luna": {"medium", "high", "xhigh"}, "terra": {"medium", "high", "xhigh"}, "soul": {"medium", "high", "xhigh"},
		},
		EstimatedCostMicrousd: map[string]int64{"luna/medium": 10, "luna/high": 20, "luna/xhigh": 30, "terra/medium": 30, "terra/high": 50, "terra/xhigh": 70, "soul/high": 100},
		HumanEscalationAfter:  7,
	}
}

// Select never moves to a more expensive model to repair an invalid stage.
// It first chooses the highest supported effort on the same model, and then
// pauses if that model has no supported capability.
func (p ReworkPolicy) Select(reworkNumber int, budgetMicrousd int64) ReworkDecision {
	d := ReworkDecision{Status: "selected", ReworkNumber: reworkNumber, PolicyVersion: p.Version}
	if reworkNumber < 0 {
		reworkNumber = 0
		d.ReworkNumber = 0
	}
	if p.HumanEscalationAfter > 0 && reworkNumber >= p.HumanEscalationAfter {
		d.Status, d.Reason = "human", "human escalation threshold reached"
		return d
	}
	if len(p.Stages) == 0 {
		d.Status, d.Reason = "paused", "policy has no stages"
		return d
	}
	stageIndex := reworkNumber
	if stageIndex >= len(p.Stages) {
		stageIndex = len(p.Stages) - 1
	}
	stage := p.Stages[stageIndex]
	d.Model, d.Effort = stage.Model, stage.Effort
	allowed := p.Capabilities[stage.Model]
	if !contains(allowed, stage.Effort) {
		fallback := highestAllowed(allowed)
		if fallback == "" {
			d.Status, d.Reason = "paused", fmt.Sprintf("unsupported capability %s/%s", stage.Model, stage.Effort)
			return d
		}
		d.Effort, d.Fallback = fallback, stage.Model+"/"+fallback
	}
	choice := d.Model + "/" + d.Effort
	d.EstimatedCostMicrousd = p.EstimatedCostMicrousd[choice]
	// A negative budget is the worker's explicit "no limit configured" value;
	// zero remains a real budget decision for callers that want a hard stop.
	if budgetMicrousd == 0 || (budgetMicrousd > 0 && d.EstimatedCostMicrousd > 0 && d.EstimatedCostMicrousd > budgetMicrousd) {
		d.Status, d.Reason = "budget", "budget limit would be exceeded"
	}
	return d
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func highestAllowed(values []string) string {
	for _, effort := range []string{"xhigh", "high", "medium", "low"} {
		if contains(values, effort) {
			return effort
		}
	}
	return ""
}
