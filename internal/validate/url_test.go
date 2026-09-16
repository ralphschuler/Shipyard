package validate

import "testing"

func TestWebhookURL(t *testing.T) {
	valid := []string{
		"https://hooks.example.test/taskboard",
		"http://sentry.internal:9000/hooks?event=run",
	}
	for _, value := range valid {
		if err := WebhookURL(value); err != nil {
			t.Fatalf("expected %q to be valid: %v", value, err)
		}
	}
	invalid := []string{"", "hooks.example.test", "ftp://example.test/hook", "https://user:pass@example.test/hook", "https://example.test/hook#secret"}
	for _, value := range invalid {
		if err := WebhookURL(value); err == nil {
			t.Fatalf("expected %q to be invalid", value)
		}
	}
}
