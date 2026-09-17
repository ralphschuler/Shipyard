package store

import (
	"strings"
	"testing"
	"time"
)

func TestDashboardTaskFilterMatchesArguments(t *testing.T) {
	when := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		from, to   *time.Time
		provider   string
		model      string
		agent      string
		board      string
		placeholds string
		args       int
	}{
		{name: "unfiltered", placeholds: "TRUE", args: 0},
		{name: "board only", board: "board-id", placeholds: "$1", args: 1},
		{name: "usage filters", from: &when, provider: "openai", placeholds: "$1 $2 $3 $4 $5", args: 5},
		{name: "usage and board", from: &when, provider: "openai", board: "board-id", placeholds: "$1 $2 $3 $4 $5 $6", args: 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			condition, args := dashboardTaskFilter(tt.from, tt.to, tt.provider, tt.model, tt.agent, tt.board)
			if len(args) != tt.args {
				t.Fatalf("got %d SQL arguments, want %d (%s)", len(args), tt.args, condition)
			}
			for _, placeholder := range strings.Fields(tt.placeholds) {
				if !strings.Contains(condition, placeholder) {
					t.Errorf("condition %q does not contain expected placeholder %s", condition, placeholder)
				}
			}
		})
	}
}
