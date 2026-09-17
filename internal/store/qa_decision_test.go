package store

import (
	"encoding/json"
	"testing"

	"taskboard/internal/domain"
)

func TestQADecisionTargetMapsReleaseButtonsToAllowedWorkflowTargets(t *testing.T) {
	columns := []domain.Column{
		{ID: "qa", Name: "QA", Type: "standard"},
		{ID: "development", Name: "In Progress", Type: "standard"},
		{ID: "done", Name: "Done", Type: "done"},
	}
	transitions := []domain.Transition{
		{FromColumnID: "qa", ToColumnID: "development"},
		{FromColumnID: "qa", ToColumnID: "done"},
	}

	for _, test := range []struct {
		name, value, want string
	}{
		{name: "approve", value: "approve", want: "done"},
		{name: "rework", value: "rework", want: "development"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := json.Marshal(map[string][]string{"release_decision": {test.value}})
			if err != nil {
				t.Fatal(err)
			}
			if got := qaDecisionTarget("qa_release", response, "qa", columns, transitions); got != test.want {
				t.Fatalf("target = %q, want %q", got, test.want)
			}
		})
	}
}

func TestQADecisionTargetFailsClosedForInvalidOrAmbiguousAnswers(t *testing.T) {
	columns := []domain.Column{
		{ID: "qa", Name: "QA", Type: "standard"},
		{ID: "development", Name: "In Progress", Type: "standard"},
		{ID: "done", Name: "Done", Type: "done"},
	}
	transitions := []domain.Transition{
		{FromColumnID: "qa", ToColumnID: "development"},
		{FromColumnID: "qa", ToColumnID: "done"},
	}
	for _, response := range [][]byte{
		[]byte(`{"release_decision":["later"]}`),
		[]byte(`{"release_decision":["approve","rework"]}`),
		[]byte(`{"other":["rework"]}`),
		[]byte(`not-json`),
	} {
		if got := qaDecisionTarget("qa_release", response, "qa", columns, transitions); got != "" {
			t.Fatalf("invalid QA response produced target %q", got)
		}
	}
	if got := qaDecisionTarget("other", []byte(`{"release_decision":["rework"]}`), "qa", columns, transitions); got != "" {
		t.Fatalf("non-QA interaction produced target %q", got)
	}
}

func TestQADecisionTargetRejectsFallbackForInvalidAnswer(t *testing.T) {
	columns := []domain.Column{
		{ID: "qa", Name: "QA", Type: "standard"},
		{ID: "development", Name: "In Progress", Type: "standard"},
		{ID: "done", Name: "Done", Type: "done"},
	}
	transitions := []domain.Transition{
		{FromColumnID: "qa", ToColumnID: "development"},
		{FromColumnID: "qa", ToColumnID: "done"},
	}

	if _, err := resolveQADecisionTarget("qa_release", []byte(`{"release_decision":["later"]}`), "qa", columns, transitions); err == nil {
		t.Fatal("invalid QA answer must reject the fallback workflow target")
	}
}
