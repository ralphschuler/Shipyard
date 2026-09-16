package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAgentWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := validateAgentWorkspace(dir); err == nil {
		t.Fatal("a non-git directory must be rejected")
	}
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	if err := validateAgentWorkspace(dir); err != nil {
		t.Fatalf("initialized repository rejected: %v", err)
	}
	if err := validateAgentWorkspace("relative/workspace"); err == nil {
		t.Fatal("relative path must be rejected")
	}
	missing := filepath.Join(dir, "missing")
	if err := validateAgentWorkspace(missing); err == nil {
		t.Fatal("missing workspace must be rejected")
	}
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := validateAgentWorkspace(dir); err == nil {
		t.Fatal("repository without git metadata must be rejected")
	}
}

func TestAutomationTimingRequiresCompleteSchedule(t *testing.T) {
	makeRequest := func(trigger, every, within, cooldown string) *http.Request {
		form := url.Values{"trigger_type": {trigger}, "schedule_every_minutes": {every}, "due_within_hours": {within}, "cooldown_minutes": {cooldown}}
		r := httptest.NewRequest("POST", "/automations", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	if _, _, _, err := automationTiming(makeRequest("task.due_soon", "", "24", "0")); err == nil {
		t.Fatal("incomplete schedule must fail")
	}
	if every, within, cooldown, err := automationTiming(makeRequest("task.due_soon", "15", "24", "5")); err != nil || every != 15 || within != 24 || cooldown != 5 {
		t.Fatalf("valid schedule: %d %d %d %v", every, within, cooldown, err)
	}
	if _, _, _, err := automationTiming(makeRequest("task.entered_column", "15", "", "0")); err == nil {
		t.Fatal("event rule must reject schedule fields")
	}
}
