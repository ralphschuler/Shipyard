package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"taskboard/internal/domain"
	"testing"
)

func TestToolCatalogueIncludesMutations(t *testing.T) {
	want := map[string]bool{"update_board": false, "delete_task": false, "add_comment": false, "complete_task": false, "create_agent": false, "create_automation_rule": false, "get_run": false, "update_provider_setting": false}
	for _, tool := range tools() {
		if _, ok := want[tool["name"].(string)]; ok {
			want[tool["name"].(string)] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing tool %s", name)
		}
	}
}

func TestToolSchemasExposeSupportedOptionalArguments(t *testing.T) {
	want := map[string][]string{
		"create_board":            {"template"},
		"create_task":             {"description", "priority", "project_ids", "group_ids"},
		"create_automation_rule":  {"target_column_id", "label_id", "require_delivery_approval"},
		"update_provider_setting": {"command", "options", "base_url"},
	}
	for _, tool := range tools() {
		name := tool["name"].(string)
		fields, tracked := want[name]
		if !tracked {
			continue
		}
		schema := tool["inputSchema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		for _, field := range fields {
			if _, ok := properties[field]; !ok {
				t.Fatalf("%s schema hides supported field %s", name, field)
			}
		}
	}
}

func TestUpdateSandboxProfileRequiresName(t *testing.T) {
	for _, tool := range tools() {
		if tool["name"] != "update_sandbox_profile" {
			continue
		}
		schema := tool["inputSchema"].(map[string]any)
		required := schema["required"].([]string)
		for _, field := range required {
			if field == "name" {
				return
			}
		}
		t.Fatal("update_sandbox_profile must require name")
	}
	t.Fatal("update_sandbox_profile tool is missing")
}

func TestCreateWebhookRejectsInvalidURLBeforeStoreAccess(t *testing.T) {
	request := httptest.NewRequest("POST", "/mcp", nil)
	params, err := json.Marshal(map[string]any{
		"name": "create_webhook",
		"arguments": map[string]string{
			"name":   "invalid",
			"url":    "ftp://example.test/hook",
			"events": "run.succeeded",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, message := (&Server{}).call(request.WithContext(context.Background()), actor{User: domain.User{Role: "admin"}}, params)
	if message == "" {
		t.Fatal("expected invalid webhook URL to be rejected")
	}
}

func TestServeHTTPRejectsMissingTokenWithoutDispatch(t *testing.T) {
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	(&Server{}).ServeHTTP(res, req)
	if res.Code != 401 {
		t.Fatalf("status = %d, want 401", res.Code)
	}
}
