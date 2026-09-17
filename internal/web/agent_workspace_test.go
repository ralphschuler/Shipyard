package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

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
