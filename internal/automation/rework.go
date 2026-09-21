package automation

import (
	"encoding/json"
	"fmt"
	"taskboard/internal/domain"
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
	BudgetMicrousd        int64
	BudgetConfigured      bool
	TariffsConfigured     bool
}

type ReworkDecision struct {
	Status, Model, Effort, Fallback, Reason string
	ReworkNumber                            int
	PolicyVersion                           string
	DiscoverySource                         string
	BudgetDecision                          string
	EstimatedCostMicrousd                   int64
	BudgetLimitMicrousd                     int64
}

// ReworkPolicyFromJSON validates the operator-editable policy format. Budget
// and cost tariffs are persisted as configured, including an explicit zero
// ceiling. Capabilities are never inferred from stages: only an explicit map
// or live provider discovery may confirm a model/effort combination.
func ReworkPolicyFromJSON(raw string) (ReworkPolicy, error) {
	policy := DefaultReworkPolicy()
	if raw == "" || raw == "{}" {
		return policy, nil
	}
	var input struct {
		Version               string              `json:"version"`
		Stages                []ReworkStage       `json:"stages"`
		Capabilities          map[string][]string `json:"capabilities"`
		EstimatedCostMicrousd map[string]int64    `json:"estimated_cost_microusd"`
		HumanEscalationAfter  int                 `json:"human_escalation_after"`
		BudgetMicrousd        *int64              `json:"budget_microusd"`
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
		if providedCapabilities && !contains(policy.Capabilities[stage.Model], stage.Effort) {
			return ReworkPolicy{}, fmt.Errorf("Modell %q unterstützt Effort %q laut Policy nicht", stage.Model, stage.Effort)
		}
	}
	if input.EstimatedCostMicrousd != nil {
		tariffs := make(map[string]int64, len(input.EstimatedCostMicrousd))
		for choice, cost := range input.EstimatedCostMicrousd {
			if choice == "" || cost < 0 {
				return ReworkPolicy{}, fmt.Errorf("ungültiger Kostentarif %q=%d", choice, cost)
			}
			tariffs[choice] = cost
		}
		policy.EstimatedCostMicrousd = tariffs
		policy.TariffsConfigured = true
	}
	if input.BudgetMicrousd != nil {
		if *input.BudgetMicrousd < 0 {
			return ReworkPolicy{}, fmt.Errorf("budget_microusd darf nicht negativ sein")
		}
		policy.BudgetMicrousd = *input.BudgetMicrousd
		policy.BudgetConfigured = true
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
// pauses if that model has no supported capability. A negative budget remains
// an explicit unlimited override for callers that opt into it; the worker
// never supplies that value.
func (p ReworkPolicy) Select(reworkNumber int, budgetMicrousd int64) ReworkDecision {
	d := ReworkDecision{Status: "selected", ReworkNumber: reworkNumber, PolicyVersion: p.Version, BudgetLimitMicrousd: budgetMicrousd}
	if reworkNumber < 0 {
		reworkNumber = 0
		d.ReworkNumber = 0
	}
	if p.HumanEscalationAfter > 0 && reworkNumber >= p.HumanEscalationAfter {
		d.Status, d.Reason = "human", "human escalation threshold reached"
		d.BudgetDecision = formatBudgetDecision(d.Status, budgetMicrousd, 0)
		return d
	}
	if len(p.Stages) == 0 {
		d.Status, d.Reason = "paused", "policy has no stages"
		d.BudgetDecision = formatBudgetDecision(d.Status, budgetMicrousd, 0)
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
			d.BudgetDecision = formatBudgetDecision(d.Status, budgetMicrousd, 0)
			return d
		}
		d.Effort, d.Fallback = fallback, stage.Model+"/"+fallback
	}
	choice := d.Model + "/" + d.Effort
	d.EstimatedCostMicrousd = p.EstimatedCostMicrousd[choice]
	if budgetMicrousd == 0 {
		d.Status, d.Reason = "budget", "budget limit would be exceeded"
	} else if budgetMicrousd > 0 && d.EstimatedCostMicrousd <= 0 {
		d.Status, d.Reason = "paused", "no confirmed cost tariff for "+choice
	} else if budgetMicrousd > 0 && d.EstimatedCostMicrousd > budgetMicrousd {
		d.Status, d.Reason = "budget", "budget limit would be exceeded"
	}
	d.BudgetDecision = formatBudgetDecision(d.Status, budgetMicrousd, d.EstimatedCostMicrousd)
	return d
}

// ResolveReworkSelection is the worker decision path: stored budget/tariffs
// fill gaps, live discovery confirms model/effort tiers, and a zero ceiling
// stops execution. Human escalation is evaluated first and is unchanged.
func ResolveReworkSelection(policy ReworkPolicy, stored domain.ReworkPolicyState, reworkNumber int, discovery CapabilityDiscovery) ReworkDecision {
	bindStoredBudgetAndTariffs(&policy, stored)
	d := ReworkDecision{ReworkNumber: reworkNumber, PolicyVersion: policy.Version, BudgetLimitMicrousd: policy.BudgetMicrousd, DiscoverySource: discovery.Source}
	if d.DiscoverySource == "" {
		d.DiscoverySource = "provider-discovery"
	}
	if policy.HumanEscalationAfter > 0 && reworkNumber >= policy.HumanEscalationAfter {
		d.Status, d.Reason = "human", "human escalation threshold reached"
		d.BudgetDecision = formatBudgetDecision(d.Status, policy.BudgetMicrousd, 0)
		return d
	}
	if !discovery.Confirmed() {
		reason := "provider discovery did not confirm capabilities"
		if discovery.Error != "" {
			reason = discovery.Error
		}
		d.Status, d.Reason = "paused", reason
		d.BudgetDecision = formatBudgetDecision(d.Status, policy.BudgetMicrousd, 0)
		return d
	}
	policy.bindDiscoveredCapabilities(discovery)
	d = policy.Select(reworkNumber, policy.BudgetMicrousd)
	d.DiscoverySource = discovery.Source
	if d.DiscoverySource == "" {
		d.DiscoverySource = "provider-discovery"
	}
	if d.Status == "selected" && !discovery.Supports(d.Model, d.Effort) {
		d.Status, d.Reason = "paused", fmt.Sprintf("unconfirmed capability %s/%s", d.Model, d.Effort)
		d.BudgetDecision = formatBudgetDecision(d.Status, d.BudgetLimitMicrousd, d.EstimatedCostMicrousd)
	}
	return d
}

func bindStoredBudgetAndTariffs(policy *ReworkPolicy, stored domain.ReworkPolicyState) {
	if policy == nil {
		return
	}
	if !policy.BudgetConfigured {
		policy.BudgetMicrousd = stored.BudgetLimitMicrousd
	}
	if policy.TariffsConfigured {
		return
	}
	for choice, cost := range stored.EstimatedCostMicrousd {
		if choice == "" || cost < 0 {
			continue
		}
		if policy.EstimatedCostMicrousd == nil {
			policy.EstimatedCostMicrousd = map[string]int64{}
		}
		policy.EstimatedCostMicrousd[choice] = cost
	}
}

func (p *ReworkPolicy) bindDiscoveredCapabilities(discovery CapabilityDiscovery) {
	discovered := capabilitiesFromDiscovery(discovery)
	if len(p.Capabilities) == 0 {
		p.Capabilities = discovered
		return
	}
	p.Capabilities = intersectCapabilities(p.Capabilities, discovered)
}

func capabilitiesFromDiscovery(discovery CapabilityDiscovery) map[string][]string {
	result := map[string][]string{}
	for _, model := range discovery.Models {
		if efforts, ok := discovery.ModelEfforts[model]; ok && len(efforts) > 0 {
			result[model] = append([]string(nil), efforts...)
			continue
		}
		result[model] = append([]string(nil), discovery.Efforts...)
	}
	return result
}

func intersectCapabilities(policyCaps, discovered map[string][]string) map[string][]string {
	result := map[string][]string{}
	for model, efforts := range policyCaps {
		allowed := discovered[model]
		for _, effort := range efforts {
			if contains(allowed, effort) && !contains(result[model], effort) {
				result[model] = append(result[model], effort)
			}
		}
	}
	return result
}

func formatBudgetDecision(status string, limit, cost int64) string {
	if status == "selected" {
		status = "allowed"
	}
	return fmt.Sprintf("%s limit_microusd=%d estimated_cost_microusd=%d", status, limit, cost)
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
