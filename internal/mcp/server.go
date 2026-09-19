package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"taskboard/internal/automation"
	"taskboard/internal/domain"
	"taskboard/internal/sandbox"
	"taskboard/internal/store"
	"taskboard/internal/validate"
)

type rpc struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
type Server struct {
	store  *store.Store
	worker *automation.Worker
}

func New(s *store.Store, worker *automation.Worker) *Server { return &Server{store: s, worker: worker} }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer tb_") {
		w.Header().Set("WWW-Authenticate", `Bearer realm="taskboard-mcp"`)
		http.Error(w, "MCP token required", http.StatusUnauthorized)
		return
	}
	hash := sha256.Sum256([]byte(strings.TrimPrefix(auth, "Bearer ")))
	user, token, err := s.store.UserAndAPITokenForHash(r.Context(), base64.RawURLEncoding.EncodeToString(hash[:]))
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="taskboard-mcp"`)
		http.Error(w, "invalid MCP token", http.StatusUnauthorized)
		return
	}
	if r.Method == "GET" {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": MCP stream ready\n\n"))
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var q rpc
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		http.Error(w, "invalid JSON-RPC", http.StatusBadRequest)
		return
	}
	var result any
	var errText string
	switch q.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "taskboard", "version": "0.1.0"}}
	case "tools/list":
		result = map[string]any{"tools": tools()}
	case "tools/call":
		result, errText = s.call(r, q.Params)
		var tool struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(q.Params, &tool)
		status := "ok"
		if errText != "" {
			status = "error"
		}
		_ = s.store.RecordAudit(r.Context(), user.ID, "mcp."+tool.Name, "mcp_tool", tool.Name, map[string]string{
			"channel": "mcp", "token_name": token.Name, "token_prefix": token.Prefix, "status": status,
		})
	default:
		errText = "method not found"
	}
	w.Header().Set("Content-Type", "application/json")
	if errText != "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "error": map[string]any{"code": func() int {
			if errText == "method not found" {
				return -32601
			}
			return -32602
		}(), "message": errText}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result})
}

func tools() []map[string]any {
	return []map[string]any{
		{"name": "list_boards", "description": "List all task boards", "inputSchema": schema()},
		{"name": "create_board", "description": "Create a board. Templates: software, product, bug-triage, support, content, marketing, research, planning, release, personal or empty.", "inputSchema": schemaWithOptional([]string{"name"}, "template")},
		{"name": "update_board", "description": "Rename a board", "inputSchema": schema("board_id", "name")},
		{"name": "delete_board", "description": "Delete a board and its tasks", "inputSchema": schema("board_id")},
		{"name": "get_workflow", "description": "Get columns and allowed transitions", "inputSchema": schema("board_id")},
		{"name": "create_column", "description": "Add a workflow column. column_type is standard, inbox, done, or needs_action; each special type is unique per board.", "inputSchema": schemaWithOptional([]string{"board_id", "name"}, "column_type")},
		{"name": "update_column", "description": "Edit a workflow column; use column_type (standard, inbox, done, needs_action) instead of naming conventions.", "inputSchema": schema("column_id", "name")},
		{"name": "delete_column", "description": "Delete an empty workflow column", "inputSchema": schema("column_id")},
		{"name": "create_transition", "description": "Allow a directed workflow transition", "inputSchema": schemaWithOptional([]string{"board_id", "from_column_id", "to_column_id"}, "action_name")},
		{"name": "delete_transition", "description": "Remove a workflow transition", "inputSchema": schema("transition_id")},
		{"name": "update_transition", "description": "Edit a workflow transition", "inputSchema": schema("transition_id", "from_column_id", "to_column_id")},
		{"name": "create_task", "description": "Create a task in the board initial column", "inputSchema": schemaWithOptional([]string{"board_id", "title"}, "description", "priority", "start_date", "due_date", "project_ids", "group_ids")},
		{"name": "list_tasks", "description": "List board tasks", "inputSchema": schema("board_id")},
		{"name": "get_task", "description": "Get task plus allowed moves", "inputSchema": schema("task_id")},
		{"name": "update_task", "description": "Edit task title, description, priority or dates", "inputSchema": schema("task_id", "title")},
		{"name": "delete_task", "description": "Delete a task", "inputSchema": schema("task_id")},
		{"name": "add_comment", "description": "Add a comment to a task", "inputSchema": schema("task_id", "body")},
		{"name": "list_labels", "description": "List labels for a board", "inputSchema": schema("board_id")},
		{"name": "create_label", "description": "Create or update a board label", "inputSchema": schema("board_id", "name")},
		{"name": "set_task_labels", "description": "Assign comma-separated label IDs to a task", "inputSchema": schema("task_id")},
		{"name": "move_task", "description": "Move a task through an allowed transition", "inputSchema": schema("task_id", "target_column_id")},
		{"name": "complete_task", "description": "Move a task to a specified reachable terminal column", "inputSchema": schema("task_id", "terminal_column_id")},
		{"name": "list_projects", "description": "List repositories and their board assignments", "inputSchema": schema()},
		{"name": "create_project", "description": "Register a repository project", "inputSchema": schemaWithOptional([]string{"name"}, "repository_url", "default_branch", "local_path", "board_ids")},
		{"name": "update_project", "description": "Update a repository project and its board assignments", "inputSchema": schemaWithOptional([]string{"project_id", "name"}, "repository_url", "default_branch", "local_path", "board_ids")},
		{"name": "list_project_groups", "description": "List reusable multi-repository target groups", "inputSchema": schema()},
		{"name": "create_project_group", "description": "Create a project group; project_ids is comma-separated", "inputSchema": schemaWithOptional([]string{"name"}, "description", "color", "project_ids")},
		{"name": "set_task_targets", "description": "Set task project_ids and group_ids as comma-separated target scopes", "inputSchema": schema("task_id")},
		{"name": "list_agents", "description": "List configured local agents", "inputSchema": schema()},
		{"name": "get_agent", "description": "Get an agent and its assigned skills", "inputSchema": schema("agent_id")},
		{"name": "create_agent", "description": "Create a local Codex agent profile", "inputSchema": schemaWithOptional([]string{"name", "prompt"}, "description", "prompt_prefix", "prompt_suffix", "max_parallel_runs", "sandbox_profile")},
		{"name": "update_agent", "description": "Update a local Codex agent profile", "inputSchema": schemaWithOptional([]string{"agent_id", "name", "prompt"}, "description", "prompt_prefix", "prompt_suffix", "max_parallel_runs", "enabled", "sandbox_profile")},
		{"name": "list_sandbox_profiles", "description": "List whitelisted sandbox profiles", "inputSchema": schema()},
		{"name": "set_agent_sandbox_profile", "description": "Assign a sandbox profile to an agent; applies to new runs", "inputSchema": schema("agent_id", "sandbox_profile")},
		{"name": "create_sandbox_profile", "description": "Create a worktree-only sandbox profile", "inputSchema": schemaWithOptional([]string{"name", "description", "network_mode", "write_mode"}, "active")},
		{"name": "update_sandbox_profile", "description": "Update description or active state of a validated sandbox profile", "inputSchema": schema("name")},
		{"name": "delete_agent", "description": "Delete an unused agent profile", "inputSchema": schema("agent_id")},
		{"name": "set_agent_skills", "description": "Set comma-separated installed skill IDs allowed for an agent", "inputSchema": schema("agent_id")},
		{"name": "list_installed_skills", "description": "List installed skills available to agents", "inputSchema": schema()},
		{"name": "list_automation_rules", "description": "List automation rules", "inputSchema": schema()},
		{"name": "create_automation_rule", "description": "Create a lifecycle automation rule. Optional label_id routes only tasks carrying that tag.", "inputSchema": schemaWithOptional([]string{"name", "trigger_type", "agent_id"}, "board_id", "target_column_id", "label_id", "success_column_id", "failure_column_id", "require_delivery_approval")},
		{"name": "set_automation_rule_enabled", "description": "Enable or pause an automation rule", "inputSchema": schema("rule_id", "enabled")},
		{"name": "delete_automation_rule", "description": "Delete an automation rule", "inputSchema": schema("rule_id")},
		{"name": "list_provider_settings", "description": "List configured provider profiles; secrets are never returned", "inputSchema": schema()},
		{"name": "update_provider_setting", "description": "Update a provider profile; set command only for a locally installed adapter", "inputSchema": schemaWithOptional([]string{"provider", "model", "enabled"}, "command", "secret_env", "base_url", "options")},
		{"name": "list_webhooks", "description": "List outbound webhook subscriptions", "inputSchema": schema()},
		{"name": "create_webhook", "description": "Create an outbound webhook subscription", "inputSchema": schema("name", "url", "events")},
		{"name": "set_webhook_enabled", "description": "Enable or pause an outbound webhook subscription", "inputSchema": schema("webhook_id", "enabled")},
		{"name": "delete_webhook", "description": "Delete a webhook subscription", "inputSchema": schema("webhook_id")},
		{"name": "list_task_runs", "description": "List agent runs for a task", "inputSchema": schema("task_id")},
		{"name": "get_run", "description": "Get run status, delivery gate and logs", "inputSchema": schema("run_id")},
		{"name": "create_workflow_run", "description": "Create an idempotent durable orchestration run", "inputSchema": schema("name", "idempotency_key")},
		{"name": "add_workflow_step", "description": "Add a dependency-aware orchestration step", "inputSchema": schema("workflow_run_id", "step_key")},
		{"name": "start_agent_run", "description": "Start an agent for a task", "inputSchema": schema("task_id", "agent_id")},
		{"name": "cancel_agent_run", "description": "Cancel a queued or running agent run", "inputSchema": schema("run_id")},
		{"name": "apply_agent_run", "description": "Apply a successful gated run diff", "inputSchema": schema("run_id")},
		{"name": "discard_agent_run", "description": "Discard a finished run worktree", "inputSchema": schema("run_id")},
	}
}
func schema(required ...string) map[string]any {
	return schemaWithOptional(required)
}

// schemaWithOptional keeps optional mutation fields discoverable to MCP
// clients. The dispatcher accepts these strings already; hiding them from the
// JSON schema made otherwise supported workflows look unavailable to agents.
func schemaWithOptional(required []string, optional ...string) map[string]any {
	p := map[string]any{}
	for _, k := range append(append([]string{}, required...), optional...) {
		p[k] = map[string]string{"type": "string"}
	}
	return map[string]any{"type": "object", "properties": p, "required": required}
}

func (s *Server) call(r *http.Request, raw json.RawMessage) (any, string) {
	var call struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &call); err != nil {
		return nil, "invalid tool parameters"
	}
	a := call.Arguments
	ctx := r.Context()
	var value any
	var err error
	switch call.Name {
	case "list_boards":
		value, err = s.store.ListBoards(ctx)
	case "create_board":
		value, err = s.store.CreateBoardWithTemplate(ctx, a["name"], a["template"])
	case "update_board":
		err = s.store.UpdateBoard(ctx, a["board_id"], a["name"])
		value = map[string]bool{"updated": err == nil}
	case "delete_board":
		err = s.store.DeleteBoard(ctx, a["board_id"])
		value = map[string]bool{"deleted": err == nil}
	case "get_workflow":
		var c, t any
		c, err = s.store.Columns(ctx, a["board_id"])
		if err == nil {
			t, err = s.store.Transitions(ctx, a["board_id"])
		}
		value = map[string]any{"columns": c, "transitions": t}
	case "create_column":
		value, err = s.store.AddColumn(ctx, a["board_id"], a["name"], a["column_type"])
	case "update_column":
		column, lookupErr := s.store.Column(ctx, a["column_id"])
		if lookupErr != nil {
			err = lookupErr
			break
		}
		typeName := a["column_type"]
		if typeName == "" {
			typeName = column.Type
		}
		err = s.store.UpdateColumn(ctx, a["column_id"], a["name"], typeName, atoi(a["canvas_x"]), atoi(a["canvas_y"]))
		value = map[string]bool{"updated": err == nil}
	case "delete_column":
		err = s.store.DeleteColumn(ctx, a["column_id"])
		value = map[string]bool{"deleted": err == nil}
	case "create_transition":
		value, err = s.store.AddTransition(ctx, a["board_id"], a["from_column_id"], a["to_column_id"], a["action_name"])
	case "delete_transition":
		err = s.store.DeleteTransition(ctx, a["transition_id"])
		value = map[string]bool{"deleted": err == nil}
	case "update_transition":
		err = s.store.UpdateTransition(ctx, a["transition_id"], a["from_column_id"], a["to_column_id"], a["action_name"])
		value = map[string]bool{"updated": err == nil}
	case "create_task":
		value, err = s.store.CreateTask(ctx, a["board_id"], a["title"], a["description"], a["priority"], a["start_date"], a["due_date"], "mcp")
		if err == nil {
			err = s.store.SetTaskTargets(ctx, value.(domain.Task).ID, csv(a["project_ids"]), csv(a["group_ids"]))
		}
	case "list_projects":
		value, err = s.store.Projects(ctx)
	case "create_project":
		value, err = s.store.CreateProject(ctx, a["name"], a["repository_url"], a["default_branch"], a["local_path"], csv(a["board_ids"]))
	case "update_project":
		err = s.store.UpdateProject(ctx, a["project_id"], a["name"], a["repository_url"], a["default_branch"], a["local_path"], csv(a["board_ids"]))
		value = map[string]bool{"updated": err == nil}
	case "list_project_groups":
		value, err = s.store.ProjectGroups(ctx)
	case "create_project_group":
		value, err = s.store.CreateProjectGroup(ctx, a["name"], a["description"], a["color"], csv(a["project_ids"]))
	case "set_task_targets":
		err = s.store.SetTaskTargets(ctx, a["task_id"], csv(a["project_ids"]), csv(a["group_ids"]))
		value = map[string]bool{"updated": err == nil}
	case "list_tasks":
		value, err = s.store.Tasks(ctx, a["board_id"])
	case "get_task":
		var t, m any
		t, err = s.store.GetTask(ctx, a["task_id"])
		if err == nil {
			m, err = s.store.Allowed(ctx, a["task_id"])
		}
		value = map[string]any{"task": t, "allowed_transitions": m}
	case "update_task":
		err = s.store.UpdateTask(ctx, a["task_id"], a["title"], a["description"], defaultString(a["priority"], "normal"), a["start_date"], a["due_date"])
		value = map[string]bool{"updated": err == nil}
	case "delete_task":
		err = s.store.DeleteTask(ctx, a["task_id"])
		value = map[string]bool{"deleted": err == nil}
	case "add_comment":
		err = s.store.AddComment(ctx, a["task_id"], a["author"], a["body"])
		value = map[string]bool{"created": err == nil}
	case "list_labels":
		value, err = s.store.Labels(ctx, a["board_id"])
	case "create_label":
		value, err = s.store.CreateLabel(ctx, a["board_id"], a["name"], defaultString(a["color"], "#3158d4"))
	case "set_task_labels":
		ids := []string{}
		for _, id := range strings.Split(a["label_ids"], ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		err = s.store.SetLabels(ctx, a["task_id"], ids)
		value = map[string]bool{"updated": err == nil}
	case "move_task":
		value, err = s.store.MoveTask(ctx, a["task_id"], a["target_column_id"], "mcp")
	case "complete_task":
		value, err = s.completeTask(ctx, a["task_id"], a["terminal_column_id"])
	case "list_agents":
		value, err = s.store.Agents(ctx)
	case "get_agent":
		var agent, skills any
		agent, err = s.store.GetAgent(ctx, a["agent_id"])
		if err == nil {
			skills, err = s.store.AgentSkills(ctx, a["agent_id"])
		}
		value = map[string]any{"agent": agent, "skills": skills}
	case "create_agent":
		value, err = s.store.CreateAgent(ctx, a["name"], a["description"], a["prompt_prefix"], a["prompt"], a["prompt_suffix"], atoi(a["max_parallel_runs"]))
		if err == nil && a["sandbox_profile"] != "" {
			err = s.store.UpdateAgentSandboxProfile(ctx, value.(domain.Agent).ID, a["sandbox_profile"])
		}
	case "update_agent":
		err = s.store.UpdateAgent(ctx, a["agent_id"], a["name"], a["description"], a["prompt_prefix"], a["prompt"], a["prompt_suffix"], atoi(a["max_parallel_runs"]), a["enabled"] != "false")
		if err == nil && a["sandbox_profile"] != "" {
			err = s.store.UpdateAgentSandboxProfile(ctx, a["agent_id"], a["sandbox_profile"])
		}
		value = map[string]bool{"updated": err == nil}
	case "list_sandbox_profiles":
		value, err = s.store.SandboxProfiles(ctx)
	case "set_agent_sandbox_profile":
		err = s.store.UpdateAgentSandboxProfile(ctx, a["agent_id"], a["sandbox_profile"])
		value = map[string]bool{"updated": err == nil}
	case "create_sandbox_profile":
		err = s.store.CreateSandboxProfile(ctx, sandbox.Profile{Name: a["name"], Description: a["description"], Mounts: []string{"worktree"}, NetworkMode: a["network_mode"], WriteMode: a["write_mode"], Active: a["active"] != "false"})
		value = map[string]bool{"created": err == nil}
	case "update_sandbox_profile":
		var p sandbox.Profile
		p, err = s.store.SandboxProfile(ctx, a["name"])
		if err == nil {
			if a["description"] != "" {
				p.Description = a["description"]
			}
			if a["active"] != "" {
				p.Active = a["active"] == "true"
			}
			err = s.store.UpdateSandboxProfile(ctx, p)
		}
		value = map[string]bool{"updated": err == nil}
	case "delete_agent":
		err = s.store.DeleteAgent(ctx, a["agent_id"])
		value = map[string]bool{"deleted": err == nil}
	case "set_agent_skills":
		err = s.store.SetAgentSkills(ctx, a["agent_id"], csv(a["skill_ids"]))
		value = map[string]bool{"updated": err == nil}
	case "list_installed_skills":
		value, err = s.store.InstalledSkills(ctx)
	case "list_automation_rules":
		value, err = s.store.Rules(ctx)
	case "create_automation_rule":
		requireDeliveryApproval := a["require_delivery_approval"] != "false"
		value, err = s.store.CreateRuleWithActionsAndLabelAndDelivery(ctx, a["name"], a["board_id"], a["trigger_type"], a["target_column_id"], a["label_id"], a["agent_id"], a["success_column_id"], a["failure_column_id"], requireDeliveryApproval)
	case "set_automation_rule_enabled":
		err = s.store.SetRuleEnabled(ctx, a["rule_id"], a["enabled"] == "true")
		value = map[string]bool{"updated": err == nil}
	case "delete_automation_rule":
		err = s.store.DeleteRule(ctx, a["rule_id"])
		value = map[string]bool{"deleted": err == nil}
	case "list_provider_settings":
		value, err = s.store.Providers(ctx)
	case "update_provider_setting":
		if err = automation.ValidateProviderConfiguration(a["provider"], a["command"], a["options"]); err == nil {
			err = s.store.SaveProvider(ctx, a["provider"], a["model"], a["command"], a["secret_env"], a["base_url"], a["options"], a["enabled"] == "true")
		}
		value = map[string]bool{"updated": err == nil}
	case "list_webhooks":
		value, err = s.store.Webhooks(ctx)
	case "create_webhook":
		if validationErr := validate.WebhookURL(a["url"]); validationErr != nil {
			err = validationErr
		} else {
			err = s.store.AddWebhook(ctx, a["name"], a["url"], a["events"])
		}
		value = map[string]bool{"created": err == nil}
	case "set_webhook_enabled":
		err = s.store.SetWebhookEnabled(ctx, a["webhook_id"], a["enabled"] == "true")
		value = map[string]bool{"updated": err == nil}
	case "delete_webhook":
		err = s.store.DeleteWebhook(ctx, a["webhook_id"])
		value = map[string]bool{"deleted": err == nil}
	case "list_task_runs":
		value, err = s.store.RunsForTask(ctx, a["task_id"])
	case "get_run":
		var run, delivery, logs any
		run, err = s.store.Run(ctx, a["run_id"])
		if err == nil {
			delivery, err = s.store.RunDelivery(ctx, a["run_id"])
		}
		if err == nil {
			logs, err = s.store.RunLogs(ctx, a["run_id"])
		}
		value = map[string]any{"run": run, "delivery": delivery, "logs": logs}
	case "create_workflow_run":
		value, err = s.store.CreateWorkflowRun(ctx, a["board_id"], a["task_id"], a["name"], a["idempotency_key"])
	case "add_workflow_step":
		value, err = s.store.AddWorkflowStep(ctx, a["workflow_run_id"], a["step_key"], a["task_id"], a["agent_id"], a["prompt"], csv(a["depends_on"]), atoi(a["max_attempts"]))
	case "start_agent_run":
		value, err = s.store.CreateManualRun(ctx, a["task_id"], a["agent_id"])
		if err == nil {
			go s.worker.Process(context.Background())
		}
	case "cancel_agent_run":
		err = s.worker.Cancel(ctx, a["run_id"])
		value = map[string]bool{"cancelled": err == nil}
	case "apply_agent_run":
		err = s.worker.Apply(ctx, a["run_id"])
		value = map[string]bool{"applied": err == nil}
	case "discard_agent_run":
		err = s.worker.Discard(ctx, a["run_id"])
		value = map[string]bool{"discarded": err == nil}
	default:
		return nil, "unknown tool"
	}
	if err != nil {
		return nil, err.Error()
	}
	body, _ := json.Marshal(value)
	return map[string]any{"content": []map[string]string{{"type": "text", "text": string(body)}}, "isError": false}, ""
}

func atoi(value string) int { n, _ := strconv.Atoi(value); return n }
func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
func csv(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
func (s *Server) completeTask(ctx context.Context, taskID, columnID string) (any, error) {
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	cols, err := s.store.Columns(ctx, task.BoardID)
	if err != nil {
		return nil, err
	}
	for _, col := range cols {
		if col.ID == columnID && col.Type == "done" {
			return s.store.MoveTask(ctx, taskID, columnID, "mcp")
		}
	}
	return nil, errors.New("target column is not a done column")
}
