package store

import "testing"

func TestValidateTaskTemplateInput(t *testing.T) {
	tests := []struct {
		name   string
		kind   string
		input  map[string]string
		fields []string
		want   string
	}{
		{name: "bug requires steps", kind: "bug", input: map[string]string{"expected": "works"}, fields: []string{"steps", "expected"}, want: "steps is required"},
		{name: "feature requires acceptance", kind: "feature", input: map[string]string{"benefit": "faster"}, fields: []string{"benefit", "acceptance"}, want: "acceptance is required"},
		{name: "complete input passes", kind: "bug", input: map[string]string{"steps": "one", "expected": "works"}, fields: []string{"steps", "expected"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTaskTemplateInput(tt.kind, tt.input, tt.fields)
			if (err == nil) != (tt.want == "") {
				t.Fatalf("validateTaskTemplateInput() error = %v, want %q", err, tt.want)
			}
			if err != nil && err.Error() != tt.want {
				t.Fatalf("validateTaskTemplateInput() error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestAppendTemplateInputKeepsDescriptionAndAddsStructuredFields(t *testing.T) {
	got := appendTemplateInput("Context", map[string]string{"steps": "Open app", "expected": "Dashboard appears"})
	want := "Context\n\n## Reproduktionsschritte\nOpen app\n\n## Erwartetes Ergebnis\nDashboard appears"
	if got != want {
		t.Fatalf("appendTemplateInput() = %q, want %q", got, want)
	}
}
