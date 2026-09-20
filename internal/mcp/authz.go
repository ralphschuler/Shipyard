package mcp

import (
	"errors"
	"net/http"
	"strings"
	"taskboard/internal/authz"
	"taskboard/internal/domain"
)

var (
	errUnknownTool = errors.New("unknown tool")
	errForbidden   = errors.New("forbidden")
)

// actor is the live identity for one MCP request. Role is taken from the
// current user row (re-read on every call). Scopes are an optional further
// restriction; empty means every operation the role already allows.
type actor struct {
	User   domain.User
	Scopes []string
}

type webAction struct {
	method, path string
}

// toolActions maps each MCP tool onto the web/JSON route that performs the
// same work, so RoleRestriction stays the single authorization policy.
var toolActions = map[string]webAction{
	"list_boards":                 {http.MethodGet, "/api/v1/boards"},
	"create_board":                {http.MethodPost, "/api/v1/boards"},
	"update_board":                {http.MethodPost, "/boards/board"},
	"delete_board":                {http.MethodPost, "/boards/board/delete"},
	"get_workflow":                {http.MethodGet, "/boards/board/workflow"},
	"create_column":               {http.MethodPost, "/boards/board/columns"},
	"update_column":               {http.MethodPost, "/columns/column"},
	"delete_column":               {http.MethodPost, "/columns/column/delete"},
	"create_transition":           {http.MethodPost, "/boards/board/transitions"},
	"delete_transition":           {http.MethodPost, "/transitions/transition/delete"},
	"update_transition":           {http.MethodPost, "/transitions/transition"},
	"create_task":                 {http.MethodPost, "/boards/board/tasks"},
	"list_tasks":                  {http.MethodGet, "/api/v1/boards/board"},
	"get_task":                    {http.MethodGet, "/api/v1/tasks/task"},
	"update_task":                 {http.MethodPost, "/tasks/task"},
	"delete_task":                 {http.MethodPost, "/tasks/task/delete"},
	"add_comment":                 {http.MethodPost, "/tasks/task/comments"},
	"list_labels":                 {http.MethodGet, "/boards/board"},
	"create_label":                {http.MethodPost, "/boards/board/labels"},
	"set_task_labels":             {http.MethodPost, "/tasks/task"},
	"move_task":                   {http.MethodPost, "/tasks/task/move"},
	"complete_task":               {http.MethodPost, "/tasks/task/move"},
	"list_projects":               {http.MethodGet, "/api/v1/projects"},
	"create_project":              {http.MethodPost, "/projects"},
	"update_project":              {http.MethodPost, "/projects/project"},
	"list_project_groups":         {http.MethodGet, "/api/v1/project-groups"},
	"create_project_group":        {http.MethodPost, "/project-groups"},
	"set_task_targets":            {http.MethodPost, "/tasks/task/targets"},
	"list_agents":                 {http.MethodGet, "/api/v1/agents"},
	"get_agent":                   {http.MethodGet, "/api/v1/agents/agent"},
	"create_agent":                {http.MethodPost, "/agents"},
	"update_agent":                {http.MethodPost, "/agents/agent"},
	"list_sandbox_profiles":       {http.MethodGet, "/api/v1/settings/sandbox-profiles"},
	"set_agent_sandbox_profile":   {http.MethodPost, "/agents/agent"},
	"create_sandbox_profile":      {http.MethodPost, "/sandbox-profiles"},
	"update_sandbox_profile":      {http.MethodPost, "/sandbox-profiles/profile"},
	"delete_agent":                {http.MethodPost, "/agents/agent/delete"},
	"set_agent_skills":            {http.MethodPost, "/agents/agent/skills"},
	"list_installed_skills":       {http.MethodGet, "/api/v1/skills"},
	"list_automation_rules":       {http.MethodGet, "/api/v1/automations"},
	"create_automation_rule":      {http.MethodPost, "/automations"},
	"set_automation_rule_enabled": {http.MethodPost, "/automations/rule/enabled"},
	"delete_automation_rule":      {http.MethodPost, "/automations/rule/delete"},
	"list_provider_settings":      {http.MethodGet, "/api/v1/settings/providers"},
	"update_provider_setting":     {http.MethodPost, "/settings/providers/provider"},
	"list_webhooks":               {http.MethodGet, "/api/v1/webhooks"},
	"create_webhook":              {http.MethodPost, "/webhooks"},
	"set_webhook_enabled":         {http.MethodPost, "/webhooks/webhook/enabled"},
	"delete_webhook":              {http.MethodPost, "/webhooks/webhook/delete"},
	"list_task_runs":              {http.MethodGet, "/api/v1/runs"},
	"get_run":                     {http.MethodGet, "/api/v1/runs/run"},
	"create_workflow_run":         {http.MethodPost, "/boards"},
	"add_workflow_step":           {http.MethodPost, "/tasks/task"},
	"start_agent_run":             {http.MethodPost, "/tasks/task/runs"},
	"cancel_agent_run":            {http.MethodPost, "/runs/run/cancel"},
	"apply_agent_run":             {http.MethodPost, "/runs/run/apply"},
	"discard_agent_run":           {http.MethodPost, "/runs/run/discard"},
}

func knownRole(role string) bool {
	switch role {
	case "owner", "admin", "member", "viewer":
		return true
	default:
		return false
	}
}

func authorizeTool(role, name string, scopes []string) error {
	if !knownRole(role) {
		return errForbidden
	}
	action, ok := toolActions[name]
	if !ok {
		return errUnknownTool
	}
	if status, _ := authz.RoleRestriction(action.method, action.path, role); status != 0 {
		return errForbidden
	}
	if !scopeAllows(scopes, name, action) {
		return errForbidden
	}
	return nil
}

func (a actor) authorize(name string) error {
	return authorizeTool(a.User.Role, name, a.Scopes)
}

func toolsFor(a actor) []map[string]any {
	allowed := make([]map[string]any, 0, len(tools()))
	for _, tool := range tools() {
		name, _ := tool["name"].(string)
		if a.authorize(name) == nil {
			allowed = append(allowed, tool)
		}
	}
	return allowed
}

func toolClass(action webAction) string {
	if authz.IsAdminArea(action.path) {
		return "admin"
	}
	if authz.UnsafeMethod(action.method) {
		return "write"
	}
	return "read"
}

func scopeAllows(scopes []string, name string, action webAction) bool {
	if len(scopes) == 0 {
		return true
	}
	class := toolClass(action)
	for _, scope := range scopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if scope == "" {
			continue
		}
		if scope == name || scope == class || scope == "admin" {
			return true
		}
		if scope == "write" && (class == "read" || class == "write") {
			return true
		}
		if scope == "read" && class == "read" {
			return true
		}
	}
	return false
}

func callAuditMetadata(user domain.User, token domain.APIToken, errText string) map[string]string {
	status := "ok"
	switch errText {
	case "":
	case errForbidden.Error():
		status = "forbidden"
	default:
		status = "error"
	}
	return map[string]string{
		"channel":      "mcp",
		"token_name":   token.Name,
		"token_prefix": token.Prefix,
		"status":       status,
		"role":         user.Role,
	}
}

func jsonRPCErrorCode(errText string) int {
	switch errText {
	case "method not found":
		return -32601
	case errForbidden.Error():
		return -32001
	default:
		return -32602
	}
}
