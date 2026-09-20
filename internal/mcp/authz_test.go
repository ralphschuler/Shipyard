package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"taskboard/internal/domain"
	"testing"
)

func TestToolCatalogueMatchesAuthorizationTable(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range tools() {
		name := tool["name"].(string)
		if _, ok := toolActions[name]; !ok {
			t.Errorf("tool %s is advertised but has no web action mapping", name)
		}
		seen[name] = true
	}
	for name := range toolActions {
		if !seen[name] {
			t.Errorf("web action mapping for %s has no advertised tool", name)
		}
	}
}

func TestRoleMatrixForMCPReadWriteAdminTools(t *testing.T) {
	samples := map[string][]string{
		"read": {
			"list_boards", "get_workflow", "list_tasks", "get_task", "list_labels",
			"list_projects", "list_project_groups", "list_task_runs", "get_run",
		},
		"write": {
			"create_board", "update_board", "delete_board", "create_task", "update_task",
			"delete_task", "add_comment", "move_task", "complete_task", "create_project",
			"start_agent_run", "apply_agent_run", "discard_agent_run", "cancel_agent_run",
			"create_workflow_run", "add_workflow_step",
		},
		"admin": {
			"list_agents", "get_agent", "create_agent", "update_agent", "delete_agent",
			"set_agent_skills", "set_agent_sandbox_profile", "list_sandbox_profiles",
			"create_sandbox_profile", "update_sandbox_profile", "list_installed_skills",
			"list_automation_rules", "create_automation_rule", "set_automation_rule_enabled",
			"delete_automation_rule", "list_provider_settings", "update_provider_setting",
			"list_webhooks", "create_webhook", "set_webhook_enabled", "delete_webhook",
		},
	}
	for class, names := range samples {
		for _, name := range names {
			action, ok := toolActions[name]
			if !ok {
				t.Fatalf("missing action for %s", name)
			}
			if got := toolClass(action); got != class {
				t.Errorf("%s class = %s, want %s (%s %s)", name, got, class, action.method, action.path)
			}
		}
	}

	roles := []string{"viewer", "member", "admin", "owner"}
	for _, role := range roles {
		for class, names := range samples {
			for _, name := range names {
				err := authorizeTool(role, name, nil)
				wantAllow := false
				switch role {
				case "owner", "admin":
					wantAllow = true
				case "member":
					wantAllow = class != "admin"
				case "viewer":
					wantAllow = class == "read"
				}
				if (err == nil) != wantAllow {
					t.Errorf("%s as %s: err=%v, want allow=%v", name, role, err, wantAllow)
				}
			}
		}
	}
}

func TestUnknownToolAndUnknownRoleAreDenied(t *testing.T) {
	if err := authorizeTool("owner", "drop_database", nil); err != errUnknownTool {
		t.Fatalf("unknown tool err = %v, want %v", err, errUnknownTool)
	}
	if err := authorizeTool("service", "list_boards", nil); err != errForbidden {
		t.Fatalf("unknown role err = %v, want %v", err, errForbidden)
	}
	if err := authorizeTool("", "list_boards", nil); err != errForbidden {
		t.Fatalf("empty role err = %v, want %v", err, errForbidden)
	}
}

func TestMemberCannotAdministerProvidersSkillsSandboxOrAgents(t *testing.T) {
	for _, name := range []string{
		"update_provider_setting", "list_provider_settings",
		"create_agent", "update_agent", "delete_agent", "set_agent_skills",
		"set_agent_sandbox_profile", "create_sandbox_profile", "update_sandbox_profile",
		"list_installed_skills", "create_automation_rule",
	} {
		if err := authorizeTool("member", name, nil); err != errForbidden {
			t.Errorf("member %s: err=%v, want forbidden", name, err)
		}
	}
}

func TestDemotedViewerLosesWriteAccessOnTheNextCall(t *testing.T) {
	if err := authorizeTool("member", "create_board", nil); err != nil {
		t.Fatalf("member must still create boards: %v", err)
	}
	if err := authorizeTool("member", "start_agent_run", nil); err != nil {
		t.Fatalf("member must still start runs: %v", err)
	}
	// Tokens do not cache rights. A later call that observes role=viewer, as
	// UserAndAPITokenForHash does after a demotion, must refuse mutations.
	if err := authorizeTool("viewer", "create_board", nil); err != errForbidden {
		t.Fatalf("demoted viewer create_board: err=%v, want forbidden", err)
	}
	if err := authorizeTool("viewer", "update_provider_setting", nil); err != errForbidden {
		t.Fatalf("demoted viewer update_provider_setting: err=%v, want forbidden", err)
	}
	if err := authorizeTool("viewer", "list_boards", nil); err != nil {
		t.Fatalf("demoted viewer must still read boards: %v", err)
	}
}

func TestTokenScopesOnlyRestrictTheLiveRole(t *testing.T) {
	if err := authorizeTool("admin", "create_agent", []string{"write"}); err != errForbidden {
		t.Fatalf("admin write-scoped token must not administer agents: %v", err)
	}
	if err := authorizeTool("member", "create_board", []string{"read"}); err != errForbidden {
		t.Fatalf("member read-scoped token must not write: %v", err)
	}
	if err := authorizeTool("member", "list_boards", []string{"read"}); err != nil {
		t.Fatalf("member read-scoped token must list boards: %v", err)
	}
	if err := authorizeTool("member", "create_agent", []string{"admin"}); err != errForbidden {
		t.Fatalf("scopes must not expand a member into admin: %v", err)
	}
	if err := authorizeTool("admin", "update_provider_setting", []string{"admin"}); err != nil {
		t.Fatalf("admin-scoped admin must update providers: %v", err)
	}
	if err := authorizeTool("admin", "create_agent", []string{"create_agent"}); err != nil {
		t.Fatalf("per-tool scope must allow that tool: %v", err)
	}
}

func TestToolsListIsFilteredToTheCurrentRole(t *testing.T) {
	viewer := map[string]bool{}
	for _, tool := range toolsFor(actor{User: domain.User{Role: "viewer"}}) {
		viewer[tool["name"].(string)] = true
	}
	member := map[string]bool{}
	for _, tool := range toolsFor(actor{User: domain.User{Role: "member"}}) {
		member[tool["name"].(string)] = true
	}
	admin := map[string]bool{}
	for _, tool := range toolsFor(actor{User: domain.User{Role: "admin"}}) {
		admin[tool["name"].(string)] = true
	}
	owner := map[string]bool{}
	for _, tool := range toolsFor(actor{User: domain.User{Role: "owner"}}) {
		owner[tool["name"].(string)] = true
	}
	if !viewer["list_boards"] || viewer["create_board"] || viewer["create_agent"] {
		t.Fatalf("viewer catalogue = %#v", viewer)
	}
	if !member["create_board"] || !member["start_agent_run"] || member["update_provider_setting"] || member["create_agent"] {
		t.Fatalf("member catalogue = %#v", member)
	}
	if !admin["update_provider_setting"] || !admin["create_agent"] || !admin["create_board"] {
		t.Fatalf("admin catalogue = %#v", admin)
	}
	if len(owner) != len(tools()) || len(admin) != len(tools()) {
		t.Fatalf("admin/owner must see the full catalogue: owner=%d admin=%d all=%d", len(owner), len(admin), len(tools()))
	}
	if len(toolsFor(actor{User: domain.User{Role: "unknown"}})) != 0 {
		t.Fatal("unknown roles must see no tools")
	}
}

func TestCallAuditMetadataRecordsForbiddenRole(t *testing.T) {
	meta := callAuditMetadata(domain.User{Role: "member"}, domain.APIToken{Name: "ci", Prefix: "tb_abc"}, errForbidden.Error())
	if meta["status"] != "forbidden" || meta["role"] != "member" || meta["token_name"] != "ci" || meta["channel"] != "mcp" {
		t.Fatalf("audit metadata = %#v", meta)
	}
	if jsonRPCErrorCode(errForbidden.Error()) != -32001 {
		t.Fatal("forbidden calls must use a distinct JSON-RPC error code")
	}
}

func TestUnauthorizedCallDoesNotReachDispatcher(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil).WithContext(context.Background())
	for _, role := range []string{"viewer", "member"} {
		for _, name := range []string{"update_provider_setting", "create_agent", "create_sandbox_profile", "set_agent_skills", "create_automation_rule"} {
			params, err := jsonToolCall(name, map[string]string{"provider": "openai", "model": "stolen", "enabled": "true", "name": "evil", "prompt": "x"})
			if err != nil {
				t.Fatal(err)
			}
			_, message := (&Server{}).call(request, actor{User: domain.User{Role: role}}, params)
			if message != errForbidden.Error() {
				t.Fatalf("%s as %s: %q, want forbidden (a store panic would mean the dispatcher ran)", name, role, message)
			}
		}
	}
	params, err := jsonToolCall("create_board", map[string]string{"name": "should-not-exist"})
	if err != nil {
		t.Fatal(err)
	}
	_, message := (&Server{}).call(request, actor{User: domain.User{Role: "viewer"}}, params)
	if message != errForbidden.Error() {
		t.Fatalf("viewer create_board: %q, want forbidden", message)
	}
}

func jsonToolCall(name string, arguments map[string]string) ([]byte, error) {
	return json.Marshal(map[string]any{"name": name, "arguments": arguments})
}
