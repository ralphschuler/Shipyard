package web

import (
	"bytes"
	"context"
	"crypto/sha1"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"taskboard/internal/automation"
	"taskboard/internal/domain"
	"taskboard/internal/skillcatalog"
	"taskboard/internal/store"
	"taskboard/internal/updates"
	"taskboard/internal/validate"
	"time"
)

//go:embed templates/*.html static/*
var files embed.FS

type App struct {
	store        *store.Store
	worker       *automation.Worker
	update       *updates.Orchestrator
	templates    *template.Template
	live         *liveHub
	logins       *loginThrottle
	updateMu     sync.Mutex
	projectSyncs sync.Map // project UUID -> *sync.Mutex
}
type liveHub struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
}
type boardPage struct {
	Board       domain.Board
	Columns     []domain.Column
	Tasks       []domain.Task
	Transitions []domain.Transition
	Error       string
	Lang        string
	Labels      []domain.Label
	Projects    []domain.Project
	Groups      []domain.ProjectGroup
}
type taskPage struct {
	ID             string
	Task           domain.Task
	Allowed        []domain.Transition
	Columns        map[string]string
	History        []domain.History
	Comments       []domain.Comment
	BoardLabels    []domain.Label
	Error          string
	Runs           []taskRunView
	Agents         []domain.Agent
	Projects       []domain.Project
	Groups         []domain.ProjectGroup
	TargetProjects []domain.Project
	TargetGroups   []domain.ProjectGroup
	Targets        []domain.RepositoryTarget
	Interactions   []interactionView
	Changes        []taskChangeView
}

type usagePricesPage struct {
	Prices []domain.UsagePrice
	Error  string
}

// taskChangeView deliberately excludes prompt and workspace snapshots: those
// fields can contain provider details or environment values.
type taskChangeView struct {
	ID, Status, Summary, ErrorMessage, DiffSummary, GateStatus string
	CreatedAt                                                  time.Time
	FinishedAt, AppliedAt                                      *time.Time
}

type taskRunView struct {
	ID, TaskID, AgentID, Status, Summary, ErrorMessage string
	Queue                                              domain.RunQueueStatus
	StartedAt, FinishedAt                              *time.Time
	CreatedAt                                          time.Time
}

type runLogView struct {
	Entries        []domain.RunLog
	Truncated      bool
	RunID          string
	Limit          int
	OldestSequence int
}

const browserRunLogLimit = 30

// displayComments keeps the permanent audit trail intact while preventing a
// transient CLI retry from turning the task conversation into a wall of the
// same decision request. A reopened decision is still represented by its
// answer and a new interaction card; only duplicate legacy notices collapse.
func displayComments(comments []domain.Comment) []domain.Comment {
	lastAnswer := map[string]int{}
	for index, comment := range comments {
		if key := answeredDecisionKey(comment); key != "" {
			lastAnswer[key] = index
		}
	}
	seenDecisionNotice := map[string]bool{}
	result := make([]domain.Comment, 0, len(comments))
	for index, comment := range comments {
		if key := answeredDecisionKey(comment); key != "" && lastAnswer[key] != index {
			continue
		}
		key := strings.TrimSpace(comment.Author) + "\x00" + strings.TrimSpace(comment.Body)
		if comment.Author == "Agent" && strings.HasPrefix(strings.TrimSpace(comment.Body), "Agent benötigt eine Entscheidung:") {
			if seenDecisionNotice[key] {
				continue
			}
			seenDecisionNotice[key] = true
		}
		result = append(result, comment)
	}
	return result
}

func answeredDecisionKey(comment domain.Comment) string {
	if strings.TrimSpace(comment.Author) == "" {
		return ""
	}
	const prefix = "Antwort auf Agentenfrage „"
	body := strings.TrimSpace(comment.Body)
	if !strings.HasPrefix(body, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(body, prefix)
	if end := strings.Index(rest, "“:"); end >= 0 {
		return strings.TrimSpace(rest[:end])
	}
	return ""
}

// FieldID is repeated onto options before rendering. It keeps an immediate
// button response tied to its field while the template is inside an option range.
type interactionOption struct{ Value, Label, FieldID string }
type interactionField struct {
	ID, Label, Type string
	Required        bool
	Options         []interactionOption
}
type interactionSchema struct{ Fields []interactionField }
type interactionView struct {
	domain.AgentInteraction
	Fields []interactionField
}

// normalizeInteractionResponse accepts both current field-name submissions and
// the legacy direct-button markup. It is deliberately pure so each answer
// shape can be regression-tested without a database or browser.
func normalizeInteractionResponse(schemaRaw []byte, legacyKey string, response map[string][]string) map[string][]string {
	legacy, ok := response[legacyKey]
	if !ok {
		return response
	}
	var schema interactionSchema
	buttonFields := []string{}
	if json.Unmarshal(schemaRaw, &schema) == nil {
		for _, field := range schema.Fields {
			if field.Type == "buttons" && strings.TrimSpace(field.ID) != "" {
				buttonFields = append(buttonFields, field.ID)
			}
		}
	}
	if len(buttonFields) != 1 {
		return response
	}
	delete(response, legacyKey)
	response[buttonFields[0]] = legacy
	return response
}

type agentView struct {
	domain.Agent
	Skills []domain.InstalledSkill
}
type ruleView struct {
	domain.AutomationRule
	BoardName, ColumnName, LabelName, AgentName, SuccessColumnName, FailureColumnName string
}
type columnOption struct{ ID, Label, BoardID string }
type runPage struct {
	Run      domain.AgentRun
	Queue    domain.RunQueueStatus
	Logs     runLogView
	Task     domain.Task
	Delivery domain.RunDelivery
	Usage    domain.UsageReport
}
type integrationsPage struct {
	Connections []domain.IntegrationConnection
}
type runsPage struct {
	Runs                  []domain.RunOverview
	HasOlder, IsOlderPage bool
	OlderCursor           string
}
type auditPage struct {
	Events                []domain.AuditEvent
	HasOlder, IsOlderPage bool
	OlderCursor           string
}
type agentPolicyPage struct{ Prefix, Suffix string }
type appearancePage struct{ Preferences domain.UserPreferences }

func auditAction(kind, resourceType, resourceID string) string {
	if strings.HasPrefix(kind, "mcp.") {
		return "MCP · " + strings.TrimPrefix(kind, "mcp.")
	}
	if resourceType != "http" {
		return kind
	}
	switch {
	case resourceID == "/boards":
		return "Board angelegt"
	case strings.HasSuffix(resourceID, "/delete"):
		return "Eintrag gelöscht"
	case strings.HasSuffix(resourceID, "/move"):
		return "Task verschoben"
	case strings.HasSuffix(resourceID, "/comments"):
		return "Kommentar hinzugefügt"
	case strings.HasSuffix(resourceID, "/runs"):
		return "Agent-Run gestartet"
	case strings.HasSuffix(resourceID, "/apply"):
		return "Änderungen übernommen"
	case strings.HasSuffix(resourceID, "/discard"):
		return "Worktree verworfen"
	case strings.HasSuffix(resourceID, "/restart"):
		return "Run neu gestartet"
	case strings.HasPrefix(resourceID, "/automations"):
		return "Automation geändert"
	case strings.HasPrefix(resourceID, "/agents"):
		return "Agent geändert"
	case strings.HasPrefix(resourceID, "/skills"):
		return "Skill-Katalog geändert"
	case strings.HasPrefix(resourceID, "/settings"):
		return "Einstellungen geändert"
	default:
		return "Control-Panel-Aktion"
	}
}

func auditStatus(metadata string) string {
	var value map[string]string
	if json.Unmarshal([]byte(metadata), &value) != nil || value["status"] == "" {
		return "—"
	}
	return value["status"]
}

func auditStatusClass(metadata string) string {
	status := auditStatus(metadata)
	if status == "ok" || strings.HasPrefix(status, "2") || strings.HasPrefix(status, "3") {
		return "status-succeeded"
	}
	if status == "—" {
		return ""
	}
	return "status-failed"
}

func runStatusLabel(status string) string {
	switch status {
	case "queued":
		return "Wartet"
	case "running":
		return "Läuft"
	case "succeeded":
		return "Erfolgreich"
	case "failed":
		return "Fehlgeschlagen"
	case "cancelled":
		return "Abgebrochen"
	case "partial":
		return "Teilweise beendet"
	default:
		return status
	}
}

// gateStatusLabel keeps the delivery summary readable without exposing the
// storage-level state machine directly in the UI.
func gateStatusLabel(status string) string {
	switch status {
	case "passed":
		return "Bestanden"
	case "failed":
		return "Fehlgeschlagen"
	case "pending":
		return "Ausstehend"
	case "skipped":
		return "Übersprungen"
	default:
		return status
	}
}

func excerpt(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit < 1 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit])) + " …"
}

func webTemplateFunctions() template.FuncMap {
	return template.FuncMap{"tasksFor": func(tasks []domain.Task, id string) []domain.Task {
		out := []domain.Task{}
		for _, t := range tasks {
			if t.ColumnID == id {
				out = append(out, t)
			}
		}
		return out
	}, "hasAgent": func(id string, assigned []string) bool {
		for _, assignedID := range assigned {
			if id == assignedID {
				return true
			}
		}
		return false
	}, "tr": translate, "auditAction": auditAction, "auditStatus": auditStatus, "auditStatusClass": auditStatusClass, "runStatus": runStatusLabel, "gateStatus": gateStatusLabel, "excerpt": excerpt, "taskCount": func(count int) string {
		if count == 1 {
			return "1 Aufgabe"
		}
		return fmt.Sprintf("%d Aufgaben", count)
	}, "action": func(t domain.Transition, names map[string]string) string {
		if t.ActionName != "" {
			return t.ActionName
		}
		return "Nach " + names[t.ToColumnID]
	}, "due": func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.Format("02.01.2006")
	}, "datetime": func(value any) string {
		switch t := value.(type) {
		case time.Time:
			return t.Format("02.01.2006 · 15:04")
		case *time.Time:
			if t != nil {
				return t.Format("02.01.2006 · 15:04")
			}
		}
		return ""
	}, "source": func(source string) string {
		switch source {
		case "mcp":
			return "MCP"
		case "automation":
			return "Automation"
		case "agent_review":
			return "Agent-Review"
		case "agent_interaction":
			return "Agentenentscheidung"
		case "agent_failure":
			return "Agent-Fehler"
		default:
			return "Web-App"
		}
	}, "pct": func(value, max int) int {
		if max < 1 {
			return 0
		}
		return value * 100 / max
	}, "logIsLarge": func(message string) bool {
		return len([]rune(message)) > 900
	}, "logPreview": func(message string) string {
		const limit = 180
		runes := []rune(message)
		if len(runes) <= limit {
			return message
		}
		return string(runes[:limit]) + " …"
	}, "usd": func(micros int64) string {
		if micros == 0 {
			return "nicht bestimmbar"
		}
		return fmt.Sprintf("$%.2f", float64(micros)/1_000_000)
	}, "int64ptr": func(value *int64) any {
		if value == nil {
			return "unbekannt"
		}
		return *value
	}, "deref": func(value *int64) int64 {
		if value == nil {
			return 0
		}
		return *value
	}, "costPct": func(value, max int64) int {
		if max < 1 {
			return 0
		}
		return int(value * 100 / max)
	}, "priorityLabel": func(priority string) string {
		switch priority {
		case "urgent":
			return "Dringend"
		case "high":
			return "Hoch"
		case "low":
			return "Niedrig"
		default:
			return "Normal"
		}
	}, "hasLabel": func(labels []domain.Label, id string) bool {
		for _, label := range labels {
			if label.ID == id {
				return true
			}
		}
		return false
	}, "hasSkill": func(skills []domain.InstalledSkill, id string) bool {
		for _, skill := range skills {
			if skill.ID == id {
				return true
			}
		}
		return false
	}, "hasBoard": func(boards []domain.Board, id string) bool {
		for _, board := range boards {
			if board.ID == id {
				return true
			}
		}
		return false
	}, "hasProject": func(projects []domain.Project, id string) bool {
		for _, project := range projects {
			if project.ID == id {
				return true
			}
		}
		return false
	}, "hasGroup": func(groups []domain.ProjectGroup, id string) bool {
		for _, group := range groups {
			if group.ID == id {
				return true
			}
		}
		return false
	}, "linePoints": func(metrics []domain.Metric) string {
		if len(metrics) == 0 {
			return ""
		}
		max := 1
		for _, metric := range metrics {
			if metric.Count > max {
				max = metric.Count
			}
		}
		points := make([]string, 0, len(metrics))
		for i, metric := range metrics {
			x := 8
			if len(metrics) > 1 {
				x = 8 + i*184/(len(metrics)-1)
			}
			y := 82 - metric.Count*70/max
			points = append(points, fmt.Sprintf("%d,%d", x, y))
		}
		return strings.Join(points, " ")
	}}
}

func parseTemplates() (*template.Template, error) {
	return template.New("").Funcs(webTemplateFunctions()).ParseFS(files, "templates/*.html")
}

func New(s *store.Store, worker *automation.Worker) *App {
	return NewWithUpdateOrchestrator(s, worker, nil)
}

// NewWithUpdateOrchestrator is the explicit production integration point for
// a deployment's backup, signature verification, binary switch, restart,
// healthcheck and rollback adapter. The default constructor intentionally
// leaves it nil, so an incomplete deployment can never mutate itself.
func NewWithUpdateOrchestrator(s *store.Store, worker *automation.Worker, orchestrator *updates.Orchestrator) *App {
	templates, err := parseTemplates()
	if err != nil {
		panic("parse web templates: " + err.Error())
	}
	app := &App{store: s, worker: worker, update: orchestrator, live: &liveHub{clients: map[chan string]struct{}{}}, logins: newLoginThrottle(), templates: templates}
	go s.ListenChanges(context.Background(), app.live.publish)
	go app.syncProjectsLoop()
	return app
}
func (a *App) Register(m *http.ServeMux) {
	// The React/shadcn client is served as a protected preview during the
	// migration. It shares the existing browser session and talks to /api/v1;
	// legacy views stay available until their replacement is feature-complete.
	m.Handle("GET /app/", http.StripPrefix("/app/", http.FileServer(http.Dir("frontend/dist"))))
	m.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		// Keep the browser console clean without introducing a separately
		// deployed asset; the actual icon remains embedded under /static.
		http.Redirect(w, r, "/static/favicon.svg", http.StatusTemporaryRedirect)
	})
	m.Handle("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.FileServerFS(files).ServeHTTP(w, r)
	}))
	m.HandleFunc("GET /api/i18n", a.i18nAPI)
	m.HandleFunc("GET /events", a.events)
	m.HandleFunc("GET /api/v1/dashboard", a.dashboardAPI)
	m.HandleFunc("GET /api/v1/boards", a.boardsAPI)
	m.HandleFunc("GET /api/v1/board-templates", a.boardTemplatesAPI)
	m.HandleFunc("POST /api/v1/boards", a.createBoardAPI)
	m.HandleFunc("GET /api/v1/boards/{id}", a.boardAPI)
	m.HandleFunc("GET /api/v1/tasks/{id}", a.taskAPI)
	m.HandleFunc("GET /api/v1/runs/{id}", a.runAPI)
	m.HandleFunc("GET /api/v1/runs/{id}/logs", a.runLogsAPI)
	m.HandleFunc("GET /api/v1/projects", a.projectsAPI)
	m.HandleFunc("GET /api/v1/project-groups", a.projectGroupsAPI)
	m.HandleFunc("GET /api/v1/agents", a.agentsAPI)
	m.HandleFunc("GET /api/v1/agents/{id}", a.agentAPI)
	m.HandleFunc("GET /api/v1/automations", a.automationsAPI)
	m.HandleFunc("GET /api/v1/schedules", a.schedulesAPI)
	m.HandleFunc("GET /api/v1/webhooks", a.webhooksAPI)
	m.HandleFunc("GET /api/v1/skills", a.skillsAPI)
	m.HandleFunc("GET /api/v1/skills/search", a.skillsSearchAPI)
	m.HandleFunc("POST /api/v1/skills/install", a.installSkillAPI)
	m.HandleFunc("GET /api/v1/runs", a.runsAPI)
	m.HandleFunc("GET /api/v1/audit", a.auditAPI)
	m.HandleFunc("GET /api/v1/settings/providers", a.providersAPI)
	m.HandleFunc("GET /api/v1/settings/secrets", a.secretsAPI)
	m.HandleFunc("POST /api/v1/settings/secrets", a.createSecretAPI)
	m.HandleFunc("GET /api/v1/settings/agent-policy", a.agentPolicyAPI)
	m.HandleFunc("GET /api/v1/settings/appearance", a.appearanceAPI)
	m.HandleFunc("GET /api/v1/settings/integrations", a.integrationsAPI)
	m.HandleFunc("GET /api/v1/settings/updates", a.updatesAPI)
	m.HandleFunc("POST /api/v1/settings/updates/install", a.installUpdateAPI)
	m.HandleFunc("GET /api/v1/account", a.accountAPI)
	m.HandleFunc("POST /api/v1/account/tokens", a.createAccountTokenAPI)
	m.HandleFunc("GET /dashboard/attention", a.dashboardAttention)
	m.HandleFunc("GET /healthz", a.health)
	m.HandleFunc("GET /setup", a.setup)
	m.HandleFunc("POST /setup", a.setup)
	m.HandleFunc("GET /login", a.login)
	m.HandleFunc("POST /login", a.login)
	m.HandleFunc("POST /logout", a.logout)
	m.HandleFunc("GET /account", a.account)
	m.HandleFunc("GET /runs", a.runs)
	m.HandleFunc("GET /audit", a.audit)
	m.HandleFunc("POST /account/tokens", a.createAccountToken)
	m.HandleFunc("POST /account/tokens/{id}/revoke", a.revokeAccountToken)
	m.HandleFunc("GET /{$}", a.dashboard)
	m.HandleFunc("GET /boards", a.boards)
	m.HandleFunc("GET /projects", a.projects)
	m.HandleFunc("POST /projects", a.createProject)
	m.HandleFunc("POST /projects/{id}", a.updateProject)
	m.HandleFunc("POST /projects/{id}/sync", a.syncProject)
	m.HandleFunc("POST /projects/{id}/delete", a.deleteProject)
	m.HandleFunc("POST /project-groups", a.createProjectGroup)
	m.HandleFunc("POST /project-groups/{id}", a.updateProjectGroup)
	m.HandleFunc("POST /project-groups/{id}/delete", a.deleteProjectGroup)
	m.HandleFunc("GET /skills", a.skills)
	m.HandleFunc("POST /skills/skills-sh/install", a.installSkillsSH)
	m.HandleFunc("POST /skills/{id}/uninstall", a.uninstallSkill)
	m.HandleFunc("GET /agents", a.agents)
	m.HandleFunc("POST /agents", a.createAgent)
	m.HandleFunc("POST /agents/{id}/skills", a.updateAgentSkills)
	m.HandleFunc("POST /agents/{id}", a.updateAgent)
	m.HandleFunc("POST /agents/{id}/delete", a.deleteAgent)
	m.HandleFunc("GET /automations", a.automations)
	m.HandleFunc("GET /automations/preview", a.automationPreview)
	m.HandleFunc("GET /schedules", a.schedules)
	m.HandleFunc("POST /schedules", a.createSchedule)
	m.HandleFunc("POST /schedules/{id}/delete", a.deleteSchedule)
	m.HandleFunc("GET /webhooks", a.webhooks)
	m.HandleFunc("POST /webhooks", a.addWebhook)
	m.HandleFunc("POST /webhooks/{id}/enabled", a.setWebhookEnabled)
	m.HandleFunc("POST /webhooks/{id}/delete", a.deleteWebhook)
	m.HandleFunc("GET /agents/templates", a.agentTemplates)
	// Settings is represented by tabs.  Keep the parent URL useful for
	// bookmarks and integrations rather than returning a surprising 404.
	m.HandleFunc("GET /settings", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/settings/providers", http.StatusSeeOther)
	})
	m.HandleFunc("GET /settings/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/settings/providers", http.StatusSeeOther)
	})
	m.HandleFunc("GET /settings/providers", a.providerSettings)
	m.HandleFunc("GET /settings/secrets", a.secrets)
	m.HandleFunc("POST /settings/secrets", a.createSecret)
	m.HandleFunc("POST /settings/secrets/{id}/replace", a.replaceSecret)
	m.HandleFunc("POST /settings/secrets/{id}/revoke", a.revokeSecret)
	m.HandleFunc("POST /settings/secrets/{id}/delete", a.deleteSecret)
	m.HandleFunc("POST /settings/secrets/{id}/agents", a.assignSecretAgents)
	m.HandleFunc("GET /settings/prices", a.usagePrices)
	m.HandleFunc("POST /settings/prices", a.saveUsagePrice)
	m.HandleFunc("POST /settings/prices/{id}", a.updateUsagePrice)
	m.HandleFunc("POST /settings/prices/{id}/delete", a.deleteUsagePrice)
	m.HandleFunc("GET /settings/agent-policy", a.agentPolicy)
	m.HandleFunc("POST /settings/agent-policy", a.saveAgentPolicy)
	m.HandleFunc("GET /settings/appearance", a.appearance)
	m.HandleFunc("POST /settings/appearance", a.saveAppearance)
	m.HandleFunc("POST /settings/providers/{provider}", a.saveProvider)
	m.HandleFunc("POST /settings/providers/{provider}/test", a.testProvider)
	m.HandleFunc("GET /settings/integrations", a.integrations)
	m.HandleFunc("GET /settings/updates", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/settings/providers", http.StatusSeeOther)
	})
	m.HandleFunc("POST /settings/integrations", a.createIntegration)
	m.HandleFunc("POST /settings/integrations/{id}/delete", a.deleteIntegration)
	m.HandleFunc("POST /agents/templates", a.createTemplateAgent)
	m.HandleFunc("POST /automations", a.createAutomation)
	m.HandleFunc("POST /automations/{id}", a.updateAutomation)
	m.HandleFunc("POST /automations/{id}/enabled", a.setAutomationEnabled)
	m.HandleFunc("POST /automations/{id}/delete", a.deleteAutomation)
	m.HandleFunc("POST /boards", a.createBoard)
	m.HandleFunc("GET /boards/{id}", a.board)
	m.HandleFunc("POST /boards/{id}", a.updateBoard)
	m.HandleFunc("POST /boards/{id}/delete", a.deleteBoard)
	m.HandleFunc("POST /boards/{id}/tasks", a.createTask)
	m.HandleFunc("POST /boards/{id}/labels", a.createLabel)
	m.HandleFunc("POST /boards/{id}/columns", a.addColumn)
	m.HandleFunc("GET /boards/{id}/workflow", a.workflow)
	m.HandleFunc("POST /boards/{id}/transitions", a.addTransition)
	m.HandleFunc("POST /columns/{id}", a.updateColumn)
	m.HandleFunc("POST /columns/{id}/position", a.updateColumnPosition)
	m.HandleFunc("POST /columns/{id}/delete", a.deleteColumn)
	m.HandleFunc("POST /transitions/{id}/delete", a.deleteTransition)
	m.HandleFunc("POST /transitions/{id}", a.updateTransition)
	m.HandleFunc("GET /tasks/{id}", a.task)
	m.HandleFunc("GET /tasks/{id}/panel", a.taskPanel)
	m.HandleFunc("POST /tasks/{id}/comments", a.addComment)
	m.HandleFunc("POST /interactions/{id}/answer", a.answerInteraction)
	m.HandleFunc("POST /tasks/{id}", a.updateTask)
	m.HandleFunc("POST /tasks/{id}/targets", a.setTaskTargets)
	m.HandleFunc("POST /tasks/{id}/delete", a.deleteTask)
	m.HandleFunc("POST /tasks/{id}/move", a.moveTask)
	m.HandleFunc("POST /tasks/{id}/runs", a.startRun)
	m.HandleFunc("POST /tasks/{id}/handoff", a.handoffTask)
	m.HandleFunc("POST /tasks/{id}/needs-decision", a.requestDecision)
	m.HandleFunc("GET /runs/{id}", a.run)
	m.HandleFunc("GET /runs/{id}/trace", a.runTrace)
	m.HandleFunc("GET /runs/{id}/logs", a.runLogs)
	m.HandleFunc("GET /runs/{id}/logs/{sequence}", a.runLogEntry)
	m.HandleFunc("POST /runs/{id}/cancel", a.cancelRun)
	m.HandleFunc("POST /runs/{id}/restart", a.restartRun)
	m.HandleFunc("POST /runs/{id}/apply", a.applyRun)
	m.HandleFunc("POST /runs/{id}/discard", a.discardRun)
	m.HandleFunc("GET /runs/{id}/diff", a.runDiff)
	m.HandleFunc("POST /runs/{id}/reject", a.rejectRun)
	m.HandleFunc("POST /notifications/{id}/read", a.readNotification)
}
func (a *App) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.store.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok","database":"ok"}`))
}
func (a *App) events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch := make(chan string, 8)
	a.live.mu.Lock()
	a.live.clients[ch] = struct{}{}
	a.live.mu.Unlock()
	defer func() { a.live.mu.Lock(); delete(a.live.clients, ch); a.live.mu.Unlock() }()
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case payload := <-ch:
			_, _ = fmt.Fprintf(w, "event: change\ndata: %s\n\n", payload)
			flusher.Flush()
		case <-keepalive.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
func (h *liveHub) publish(payload string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		select {
		case client <- payload:
		default:
		}
	}
}
func (a *App) skills(w http.ResponseWriter, r *http.Request) {
	installed, e := a.store.InstalledSkills(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	var catalog []domain.CatalogSkill
	var catalogError string
	if query != "" {
		catalog, e = skillcatalog.SearchSkillsSH(r.Context(), query)
		if e != nil {
			catalogError = "Der skills.sh-Katalog ist gerade nicht erreichbar. Bitte erneut versuchen."
		}
	}
	a.render(r, w, "skills.html", map[string]any{"Installed": installed, "Catalog": catalog, "Query": query, "CatalogError": catalogError})
}
func (a *App) installSkillsSH(w http.ResponseWriter, r *http.Request) {
	if e := skillcatalog.InstallSkillsSH(r.Context(), a.store, r.FormValue("source"), r.FormValue("slug")); e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/skills", http.StatusSeeOther)
}
func (a *App) scanSkillSource(w http.ResponseWriter, r *http.Request) {
	if e := skillcatalog.Scan(r.Context(), a.store, r.PathValue("id")); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/skills", 303)
}
func (a *App) refreshSkills(w http.ResponseWriter, r *http.Request) {
	sources, err := a.store.SkillSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, source := range sources {
		if source.Enabled {
			if err = skillcatalog.Scan(r.Context(), a.store, source.ID); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		}
	}
	http.Redirect(w, r, "/skills", 303)
}
func (a *App) deleteSkillSource(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteSkillSource(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/skills", 303)
}
func (a *App) installSkill(w http.ResponseWriter, r *http.Request) {
	if e := skillcatalog.Install(r.Context(), a.store, r.PathValue("id")); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/skills", 303)
}
func (a *App) uninstallSkill(w http.ResponseWriter, r *http.Request) {
	if e := a.store.UninstallSkill(r.Context(), r.PathValue("id")); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/skills", 303)
}
func (a *App) addSkillSource(w http.ResponseWriter, r *http.Request) {
	if e := a.store.AddSkillSource(r.Context(), r.FormValue("name"), r.FormValue("repository_url"), r.FormValue("branch")); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/skills", 303)
}
func (a *App) agents(w http.ResponseWriter, r *http.Request) {
	agents, e := a.store.Agents(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	skills, e := a.store.InstalledSkills(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	views := make([]agentView, 0, len(agents))
	for _, agent := range agents {
		assigned, err := a.store.AgentSkills(r.Context(), agent.ID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		views = append(views, agentView{Agent: agent, Skills: assigned})
	}
	a.render(r, w, "agents.html", map[string]any{"Agents": views, "Skills": skills})
}
func (a *App) createAgent(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	if e := validateAgentWorkspace(r.FormValue("workspace_path")); e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	max, _ := strconv.Atoi(r.FormValue("max_parallel_runs"))
	agent, e := a.store.CreateAgent(r.Context(), r.FormValue("name"), r.FormValue("description"), r.FormValue("prompt_prefix"), r.FormValue("prompt"), r.FormValue("prompt_suffix"), r.FormValue("workspace_path"), max)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	if e := a.store.SetAgentSkills(r.Context(), agent.ID, r.Form["skill_ids"]); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/agents", 303)
}
func (a *App) updateAgentSkills(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	if e := a.store.SetAgentSkills(r.Context(), r.PathValue("id"), r.Form["skill_ids"]); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/agents", 303)
}
func (a *App) updateAgent(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	if e := validateAgentWorkspace(r.FormValue("workspace_path")); e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	max, _ := strconv.Atoi(r.FormValue("max_parallel_runs"))
	if e := a.store.UpdateAgent(r.Context(), r.PathValue("id"), r.FormValue("name"), r.FormValue("description"), r.FormValue("prompt_prefix"), r.FormValue("prompt"), r.FormValue("prompt_suffix"), r.FormValue("workspace_path"), max, r.FormValue("enabled") == "true"); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/agents", 303)
}

// validateAgentWorkspace runs in the same process context as the worker. It
// avoids accepting host paths such as /tmp that may be invisible after the
// service sandbox is applied, and verifies the Git prerequisite needed for
// isolated worktrees before a user can save an agent profile.
func validateAgentWorkspace(raw string) error {
	workspace := strings.TrimSpace(raw)
	if workspace == "" || !filepath.IsAbs(workspace) {
		return errors.New("Workspace muss ein absoluter Pfad sein")
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return errors.New("Workspace ist für den Server nicht als Verzeichnis verfügbar")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", workspace, "rev-parse", "--is-inside-work-tree").Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return errors.New("Workspace muss ein zugängliches Git-Repository sein")
	}
	return nil
}
func (a *App) deleteAgent(w http.ResponseWriter, r *http.Request) {
	if e := a.store.DeleteAgent(r.Context(), r.PathValue("id")); e != nil {
		http.Error(w, "Agent konnte nicht ausgemustert werden: "+e.Error(), 409)
		return
	}
	http.Redirect(w, r, "/agents", 303)
}
func (a *App) automations(w http.ResponseWriter, r *http.Request) {
	rules, e := a.store.Rules(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	boards, e := a.store.ListBoards(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	agents, e := a.store.Agents(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	boardNames, agentNames := map[string]string{}, map[string]string{}
	for _, b := range boards {
		boardNames[b.ID] = b.Name
	}
	for _, ag := range agents {
		agentNames[ag.ID] = ag.Name
	}
	options := []columnOption{}
	columnNames := map[string]string{}
	labels := []domain.Label{}
	labelNames := map[string]string{}
	for _, b := range boards {
		columns, err := a.store.Columns(r.Context(), b.ID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		for _, c := range columns {
			options = append(options, columnOption{ID: c.ID, Label: b.Name + " → " + c.Name, BoardID: b.ID})
			columnNames[c.ID] = c.Name
		}
		boardLabels, err := a.store.Labels(r.Context(), b.ID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		labels = append(labels, boardLabels...)
		for _, label := range boardLabels {
			labelNames[label.ID] = label.Name
		}
	}
	views := make([]ruleView, 0, len(rules))
	for _, rule := range rules {
		views = append(views, ruleView{AutomationRule: rule, BoardName: boardNames[rule.BoardID], ColumnName: columnNames[rule.TargetColumnID], LabelName: labelNames[rule.LabelID], AgentName: agentNames[rule.AgentID], SuccessColumnName: columnNames[rule.SuccessColumnID], FailureColumnName: columnNames[rule.FailureColumnID]})
	}
	a.render(r, w, "automations.html", map[string]any{"Rules": views, "Boards": boards, "Agents": agents, "Columns": options, "Labels": labels})
}
func (a *App) automationPreview(w http.ResponseWriter, r *http.Request) {
	boardID, columnID := strings.TrimSpace(r.URL.Query().Get("board_id")), strings.TrimSpace(r.URL.Query().Get("target_column_id"))
	if boardID == "" {
		http.Error(w, "Wähle zuerst ein Board aus.", http.StatusBadRequest)
		return
	}
	tasks, err := a.store.AutomationPreviewTasks(r.Context(), boardID, columnID, 25)
	if err != nil {
		http.Error(w, "Vorschau konnte nicht geladen werden.", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"tasks": tasks, "limited": len(tasks) == 25})
}
func (a *App) createAutomation(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.FormValue("agent_id")) == "" {
		http.Error(w, "Wähle einen aktiven Agent aus.", http.StatusBadRequest)
		return
	}
	every, within, cooldown, err := automationTiming(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	requireDeliveryApproval := r.FormValue("require_delivery_approval") != "false"
	_, e := a.store.CreateConfiguredRule(r.Context(), r.FormValue("name"), r.FormValue("board_id"), r.FormValue("trigger_type"), r.FormValue("target_column_id"), r.FormValue("label_id"), r.FormValue("agent_id"), r.FormValue("success_column_id"), r.FormValue("failure_column_id"), requireDeliveryApproval, every, within, cooldown)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/automations", 303)
}
func (a *App) updateAutomation(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.FormValue("agent_id")) == "" {
		http.Error(w, "Wähle einen aktiven Agent aus.", http.StatusBadRequest)
		return
	}
	every, within, cooldown, timingErr := automationTiming(r)
	if timingErr != nil {
		http.Error(w, timingErr.Error(), http.StatusBadRequest)
		return
	}
	requireDeliveryApproval := r.FormValue("require_delivery_approval") != "false"
	if err := a.store.UpdateConfiguredRule(r.Context(), r.PathValue("id"), r.FormValue("name"), r.FormValue("board_id"), r.FormValue("trigger_type"), r.FormValue("target_column_id"), r.FormValue("label_id"), r.FormValue("agent_id"), r.FormValue("success_column_id"), r.FormValue("failure_column_id"), requireDeliveryApproval, every, within, cooldown); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/automations", http.StatusSeeOther)
}

func automationTiming(r *http.Request) (every, within, cooldown int, err error) {
	parse := func(name string) (int, error) {
		raw := strings.TrimSpace(r.FormValue(name))
		if raw == "" {
			return 0, nil
		}
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			return 0, errors.New("Zeitwerte müssen ganze Zahlen sein")
		}
		return value, nil
	}
	if every, err = parse("schedule_every_minutes"); err != nil {
		return
	}
	if within, err = parse("due_within_hours"); err != nil {
		return
	}
	if cooldown, err = parse("cooldown_minutes"); err != nil {
		return
	}
	if cooldown < 0 || cooldown > 7*24*60 {
		err = errors.New("Cooldown muss zwischen 0 und 10080 Minuten liegen")
		return
	}
	if r.FormValue("trigger_type") == "task.due_soon" {
		if every < 1 || within < 1 {
			err = errors.New("Zeitregeln benötigen Prüfintervall und Fälligkeitsfenster")
		}
		return
	}
	if every != 0 || within != 0 {
		err = errors.New("Prüfintervall und Fälligkeitsfenster sind nur für Zeitregeln erlaubt")
	}
	return
}
func (a *App) schedules(w http.ResponseWriter, r *http.Request) {
	boards, e := a.store.ListBoards(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	agents, e := a.store.Agents(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	schedules, e := a.store.Schedules(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	a.render(r, w, "schedules.html", map[string]any{"Boards": boards, "Agents": agents, "Schedules": schedules})
}
func (a *App) createSchedule(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.FormValue("agent_id")) == "" {
		http.Error(w, "Wähle einen aktiven Agent aus.", http.StatusBadRequest)
		return
	}
	every, everyErr := strconv.Atoi(strings.TrimSpace(r.FormValue("every")))
	within, withinErr := strconv.Atoi(strings.TrimSpace(r.FormValue("within")))
	if everyErr != nil || withinErr != nil || every < 1 || within < 1 {
		http.Error(w, "Zeitregeln benötigen ein positives Prüfintervall und Fälligkeitsfenster.", http.StatusBadRequest)
		return
	}
	_, e := a.store.CreateConfiguredRule(r.Context(), r.FormValue("name"), r.FormValue("board_id"), "task.due_soon", "", "", r.FormValue("agent_id"), "", "", true, every, within, 0)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/schedules", 303)
}
func (a *App) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteRule(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/schedules", http.StatusSeeOther)
}
func (a *App) webhooks(w http.ResponseWriter, r *http.Request) {
	hooks, e := a.store.Webhooks(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	a.render(r, w, "webhooks.html", map[string]any{"Webhooks": hooks})
}
func (a *App) addWebhook(w http.ResponseWriter, r *http.Request) {
	if err := validate.WebhookURL(r.FormValue("url")); err != nil {
		http.Error(w, "Webhook-URL muss eine vollständige HTTP(S)-Adresse sein.", http.StatusBadRequest)
		return
	}
	if e := a.store.AddWebhook(r.Context(), r.FormValue("name"), r.FormValue("url"), r.FormValue("events")); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/webhooks", 303)
}
func (a *App) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	if e := a.store.DeleteWebhook(r.Context(), r.PathValue("id")); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/webhooks", 303)
}
func (a *App) setWebhookEnabled(w http.ResponseWriter, r *http.Request) {
	if err := a.store.SetWebhookEnabled(r.Context(), r.PathValue("id"), r.FormValue("enabled") == "true"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/webhooks", http.StatusSeeOther)
}
func (a *App) agentTemplates(w http.ResponseWriter, r *http.Request) {
	a.render(r, w, "agent-templates.html", nil)
}
func (a *App) createTemplateAgent(w http.ResponseWriter, r *http.Request) {
	kind := r.FormValue("kind")
	prompt := "Implementiere die zugewiesene Aufgabe fokussiert. Erstelle keinen Push, Merge oder Release."
	desc := "Implementierungs-Agent"
	if kind == "review" {
		prompt = "Prüfe die Änderung kritisch und dokumentiere konkrete Probleme. Erstelle keinen Push, Merge oder Release."
		desc = "Code-Review-Agent"
	}
	if kind == "docs" {
		prompt = "Aktualisiere die Dokumentation zur Aufgabe. Erstelle keinen Push, Merge oder Release."
		desc = "Dokumentations-Agent"
	}
	_, e := a.store.CreateAgent(r.Context(), r.FormValue("name"), desc, "", prompt, "", r.FormValue("workspace"), 1)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/agents", 303)
}
func (a *App) providerSettings(w http.ResponseWriter, r *http.Request) {
	p, e := a.store.Providers(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	a.render(r, w, "providers.html", map[string]any{"Providers": p})
}

func canManageSecrets(u domain.User) bool { return u.Role == "owner" || u.Role == "admin" }
func (a *App) secrets(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canManageSecrets(u) {
		http.Error(w, "forbidden", 403)
		return
	}
	items, err := a.store.Secrets(r.Context())
	if err != nil {
		http.Error(w, "secrets unavailable", 500)
		return
	}
	agents, err := a.store.Agents(r.Context())
	if err != nil {
		http.Error(w, "agents unavailable", 500)
		return
	}
	a.render(r, w, "secrets.html", map[string]any{"Secrets": items, "Agents": agents})
}
func (a *App) secretsAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok {
		http.Error(w, "not authenticated", 401)
		return
	}
	if !canManageSecrets(u) {
		http.Error(w, "forbidden", 403)
		return
	}
	items, err := a.store.Secrets(r.Context())
	if err != nil {
		http.Error(w, "secrets unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}
func (a *App) createSecretAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok {
		http.Error(w, "not authenticated", 401)
		return
	}
	if !canManageSecrets(u) {
		http.Error(w, "forbidden", 403)
		return
	}
	var input struct{ Name, Description, EnvName, Value string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	secret, err := a.store.CreateSecret(r.Context(), u.ID, input.Name, input.Description, input.EnvName, input.Value)
	if err != nil {
		http.Error(w, "secret could not be saved", 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(secret)
}
func (a *App) createSecret(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok || !canManageSecrets(u) {
		http.Error(w, "forbidden", 403)
		return
	}
	_, err := a.store.CreateSecret(r.Context(), u.ID, r.FormValue("name"), r.FormValue("description"), r.FormValue("env_name"), r.FormValue("value"))
	if err != nil {
		http.Error(w, "secret could not be saved", 400)
		return
	}
	http.Redirect(w, r, "/settings/secrets", 303)
}
func (a *App) replaceSecret(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok || !canManageSecrets(u) {
		http.Error(w, "forbidden", 403)
		return
	}
	if err := a.store.ReplaceSecret(r.Context(), u.ID, r.PathValue("id"), r.FormValue("value")); err != nil {
		http.Error(w, "secret could not be replaced", 400)
		return
	}
	http.Redirect(w, r, "/settings/secrets", 303)
}
func (a *App) revokeSecret(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok || !canManageSecrets(u) {
		http.Error(w, "forbidden", 403)
		return
	}
	if err := a.store.RevokeSecret(r.Context(), u.ID, r.PathValue("id")); err != nil {
		http.Error(w, "secret could not be revoked", 400)
		return
	}
	http.Redirect(w, r, "/settings/secrets", 303)
}
func (a *App) deleteSecret(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok || !canManageSecrets(u) {
		http.Error(w, "forbidden", 403)
		return
	}
	if err := a.store.DeleteSecret(r.Context(), u.ID, r.PathValue("id")); err != nil {
		http.Error(w, "secret could not be deleted", 400)
		return
	}
	http.Redirect(w, r, "/settings/secrets", 303)
}
func (a *App) assignSecretAgents(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok || !canManageSecrets(u) {
		http.Error(w, "forbidden", 403)
		return
	}
	if err := a.store.SetSecretAgents(r.Context(), u.ID, r.PathValue("id"), r.Form["agent_id"]); err != nil {
		http.Error(w, "secret assignment could not be saved", 400)
		return
	}
	http.Redirect(w, r, "/settings/secrets", 303)
}
func (a *App) agentPolicy(w http.ResponseWriter, r *http.Request) {
	prefix, suffix, err := a.store.AgentPromptPolicy(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(r, w, "agent-policy.html", agentPolicyPage{Prefix: prefix, Suffix: suffix})
}
func (a *App) saveAgentPolicy(w http.ResponseWriter, r *http.Request) {
	if err := a.store.UpdateAgentPromptPolicy(r.Context(), r.FormValue("prompt_prefix"), r.FormValue("prompt_suffix")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/settings/agent-policy", http.StatusSeeOther)
}
func (a *App) appearance(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	prefs, err := a.store.UserPreferences(r.Context(), user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(r, w, "appearance.html", appearancePage{Preferences: prefs})
}
func (a *App) saveAppearance(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	theme := r.FormValue("theme")
	hints := r.FormValue("shortcut_hints") == "true"
	language := r.FormValue("language")
	if language == "" {
		if prefs, err := a.store.UserPreferences(r.Context(), user.ID); err == nil && prefs.Language != "" {
			language = prefs.Language
		} else {
			language = "de"
		}
	}
	if err := a.store.SaveUserPreferences(r.Context(), user.ID, theme, hints, language); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "shipyard_theme", Value: theme, Path: "/", MaxAge: 31536000, Secure: secureCookie(r), SameSite: http.SameSiteLaxMode})
	hintValue := "false"
	if hints {
		hintValue = "true"
	}
	http.SetCookie(w, &http.Cookie{Name: "shipyard_shortcut_hints", Value: hintValue, Path: "/", MaxAge: 31536000, Secure: secureCookie(r), SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: "shipyard_language", Value: language, Path: "/", MaxAge: 31536000, Secure: secureCookie(r), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/settings/appearance", http.StatusSeeOther)
}
func (a *App) saveProvider(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	options := r.FormValue("options")
	if err := automation.ValidateProviderConfiguration(provider, r.FormValue("command"), options); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if e := a.store.SaveProvider(r.Context(), provider, r.FormValue("model"), r.FormValue("command"), r.FormValue("secret_env"), r.FormValue("base_url"), options, r.FormValue("enabled") == "true"); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/settings/providers", 303)
}

func parseMicrousd(value string) (*int64, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || n < 0 {
		return nil, errors.New("Preis muss eine positive Ganzzahl in Micro-USD pro Million sein")
	}
	return &n, nil
}

func (a *App) usagePrices(w http.ResponseWriter, r *http.Request) {
	prices, err := a.store.UsagePrices(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	a.render(r, w, "prices.html", usagePricesPage{Prices: prices})
}

func (a *App) saveUsagePrice(w http.ResponseWriter, r *http.Request) {
	a.persistUsagePrice(w, r, "")
}

func (a *App) updateUsagePrice(w http.ResponseWriter, r *http.Request) {
	a.persistUsagePrice(w, r, r.PathValue("id"))
}

func (a *App) persistUsagePrice(w http.ResponseWriter, r *http.Request, id string) {
	from, err := time.Parse("2006-01-02", strings.TrimSpace(r.FormValue("valid_from")))
	if err != nil {
		http.Error(w, "Gültig ab muss ein Datum sein.", 400)
		return
	}
	var until *time.Time
	if value := strings.TrimSpace(r.FormValue("valid_until")); value != "" {
		parsed, parseErr := time.Parse("2006-01-02", value)
		if parseErr != nil || !parsed.After(from) {
			http.Error(w, "Gültig bis muss nach Gültig ab liegen.", 400)
			return
		}
		until = &parsed
	}
	p := domain.UsagePrice{ID: id, Provider: strings.TrimSpace(r.FormValue("provider")), Model: strings.TrimSpace(r.FormValue("model")), ServiceTier: strings.TrimSpace(r.FormValue("service_tier")), Version: strings.TrimSpace(r.FormValue("version")), ValidFrom: from}
	p.ValidUntil = until
	if p.Provider == "" || p.Model == "" || p.Version == "" {
		http.Error(w, "Provider, Modell und Version sind Pflichtfelder.", 400)
		return
	}
	fields := []**int64{&p.Input, &p.Output, &p.CachedInput, &p.CacheWrite, &p.Reasoning}
	values := []string{"input", "output", "cached_input", "cache_write", "reasoning"}
	known := 0
	for i, field := range fields {
		value, parseErr := parseMicrousd(r.FormValue(values[i]))
		if parseErr != nil {
			http.Error(w, parseErr.Error(), 400)
			return
		}
		*field = value
		if value != nil {
			known++
		}
	}
	if known == 0 {
		http.Error(w, "Mindestens eine Tokenrate ist erforderlich.", 400)
		return
	}
	var saveErr error
	if user, ok := currentUser(r.Context()); ok {
		if id == "" {
			saveErr = a.store.SaveUsagePriceWithAudit(r.Context(), p, user.ID)
		} else {
			saveErr = a.store.UpdateUsagePriceWithAudit(r.Context(), p, user.ID)
		}
	} else if id == "" {
		saveErr = a.store.SaveUsagePrice(r.Context(), p)
	} else {
		saveErr = a.store.UpdateUsagePrice(r.Context(), p)
	}
	if saveErr != nil {
		http.Error(w, "Preis konnte nicht gespeichert werden: "+saveErr.Error(), 400)
		return
	}
	http.Redirect(w, r, "/settings/prices", 303)
}

func (a *App) deleteUsagePrice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var deleteErr error
	if user, ok := currentUser(r.Context()); ok {
		deleteErr = a.store.DeleteUsagePriceWithAudit(r.Context(), id, user.ID)
	} else {
		deleteErr = a.store.DeleteUsagePrice(r.Context(), id)
	}
	if deleteErr != nil {
		http.Error(w, "Preis konnte nicht gelöscht werden: "+deleteErr.Error(), 400)
		return
	}
	http.Redirect(w, r, "/settings/prices", 303)
}
func (a *App) testProvider(w http.ResponseWriter, r *http.Request) {
	result, err := a.worker.CheckProviderForAgent(r.Context(), r.PathValue("provider"), r.URL.Query().Get("agent_id"))
	if err != nil {
		http.Error(w, "Provider-Test fehlgeschlagen: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"result": result})
}
func (a *App) integrations(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	connections, err := a.store.IntegrationConnections(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "Integrationen konnten nicht geladen werden.", http.StatusInternalServerError)
		return
	}
	a.render(r, w, "integrations.html", integrationsPage{Connections: connections})
}
func (a *App) createIntegration(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok {
		http.Error(w, "Anmeldung erforderlich.", http.StatusUnauthorized)
		return
	}
	provider := r.FormValue("provider")
	if provider != "github" && provider != "gitlab" && provider != "codeberg" {
		http.Error(w, "Unbekannter Provider.", http.StatusBadRequest)
		return
	}
	baseURL := strings.TrimSpace(r.FormValue("base_url"))
	if baseURL != "" {
		parsed, err := url.Parse(baseURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			http.Error(w, "Die Basis-URL muss eine vollständige HTTPS-Adresse sein.", http.StatusBadRequest)
			return
		}
	}
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		label = strings.ToUpper(provider[:1]) + provider[1:]
	}
	if _, err := a.store.CreateIntegrationConnection(r.Context(), u.ID, provider, label, baseURL); err != nil {
		http.Error(w, "Integration konnte nicht angelegt werden.", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/settings/integrations", http.StatusSeeOther)
}
func (a *App) deleteIntegration(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok {
		http.Error(w, "Anmeldung erforderlich.", http.StatusUnauthorized)
		return
	}
	if err := a.store.DeleteIntegrationConnection(r.Context(), r.PathValue("id"), u.ID); err != nil {
		http.Error(w, "Integration konnte nicht entfernt werden.", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/settings/integrations", http.StatusSeeOther)
}
func (a *App) setAutomationEnabled(w http.ResponseWriter, r *http.Request) {
	if e := a.store.SetRuleEnabled(r.Context(), r.PathValue("id"), r.FormValue("enabled") == "true"); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/automations", 303)
}
func (a *App) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	if e := a.store.DeleteRule(r.Context(), r.PathValue("id")); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/automations", 303)
}
func language(r *http.Request) string {
	if cookie, err := r.Cookie("shipyard_language"); err == nil {
		if cookie.Value == "en" {
			return "en"
		}
		if cookie.Value == "de" {
			return "de"
		}
	}
	if strings.HasPrefix(r.Header.Get("Accept-Language"), "de") {
		return "de"
	}
	if strings.HasPrefix(r.Header.Get("Accept-Language"), "en") {
		return "en"
	}
	return "de"
}

func (a *App) accountLanguage(r *http.Request) string {
	if user, ok := currentUser(r.Context()); ok {
		if prefs, err := a.store.UserPreferences(r.Context(), user.ID); err == nil {
			return resolveLanguage(prefs.Language, r)
		}
	}
	return language(r)
}

func (a *App) render(r *http.Request, w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var page bytes.Buffer
	if e := a.templates.ExecuteTemplate(&page, name, data); e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	// Account preferences are authoritative for every server-rendered view.
	// This also covers templates that do not carry a page-specific Lang field.
	lang := a.accountLanguage(r)
	html := page.String()
	// Templates from before the i18n layer used a fixed language attribute.
	// Normalize every document here so a newly localized page cannot announce
	// German while the account is using English.
	if start := strings.Index(html, `<html lang="`); start >= 0 {
		valueStart := start + len(`<html lang="`)
		if end := strings.Index(html[valueStart:], `"`); end >= 0 {
			html = html[:valueStart] + lang + html[valueStart+end:]
		}
	}
	html = localizeHTML(html, lang)
	// Every server-rendered view exposes one stable swap boundary. HTMX uses
	// it for mutations today and for fragment navigation in the next layer.
	html = strings.Replace(html, "<main ", `<main id="app-main" `, 1)
	html = strings.Replace(html, "<main>", `<main id="app-main">`, 1)
	_, _ = io.WriteString(w, html)
}

func (a *App) i18nAPI(w http.ResponseWriter, r *http.Request) {
	// Keep the original flat English field for API consumers while exposing
	// the bidirectional dictionaries used by the browser.
	writeAPI(w, map[string]any{"language": a.accountLanguage(r), "translations": legacyDictionary(languageEnglish), "languages": legacyDictionaries()}, nil)
}
func (a *App) account(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	tokens, err := a.store.APITokens(r.Context(), u.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	a.render(r, w, "account.html", map[string]any{"User": u, "Tokens": tokens})
}
func (a *App) createAccountToken(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok {
		http.Error(w, "not authenticated", 401)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "Tokenname ist erforderlich", 400)
		return
	}
	raw := "tb_" + randomToken()
	token, err := a.store.CreateAPIToken(r.Context(), u.ID, name, tokenHash(raw), raw[:11], nil)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	tokens, _ := a.store.APITokens(r.Context(), u.ID)
	a.render(r, w, "account.html", map[string]any{"User": u, "Tokens": tokens, "NewToken": raw, "Created": token})
}
func (a *App) revokeAccountToken(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok {
		http.Error(w, "not authenticated", 401)
		return
	}
	if err := a.store.RevokeAPIToken(r.Context(), r.PathValue("id"), u.ID); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}
func (a *App) boards(w http.ResponseWriter, r *http.Request) {
	bs, e := a.store.ListBoards(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	a.render(r, w, "boards.html", map[string]any{"Boards": bs, "Templates": store.BoardTemplates(), "Lang": a.accountLanguage(r)})
}
func (a *App) projects(w http.ResponseWriter, r *http.Request) {
	projects, err := a.store.Projects(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	boards, err := a.store.ListBoards(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	groups, err := a.store.ProjectGroups(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	a.render(r, w, "projects.html", map[string]any{"Projects": projects, "Boards": boards, "Groups": groups})
}
func (a *App) createProjectGroup(w http.ResponseWriter, r *http.Request) {
	_, err := a.store.CreateProjectGroup(r.Context(), r.FormValue("name"), r.FormValue("description"), r.FormValue("color"), r.Form["project_ids"])
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/projects", 303)
}
func (a *App) updateProjectGroup(w http.ResponseWriter, r *http.Request) {
	err := a.store.UpdateProjectGroup(r.Context(), r.PathValue("id"), r.FormValue("name"), r.FormValue("description"), r.FormValue("color"), r.Form["project_ids"])
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/projects", 303)
}
func (a *App) deleteProjectGroup(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteProjectGroup(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/projects", 303)
}
func (a *App) createProject(w http.ResponseWriter, r *http.Request) {
	p, err := a.store.CreateProject(r.Context(), r.FormValue("name"), r.FormValue("repository_url"), r.FormValue("default_branch"), r.FormValue("local_path"), r.Form["board_ids"])
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if p.RepositoryURL != "" {
		_ = a.syncProjectRepo(r.Context(), p)
	}
	if err := a.setProjectGroups(r.Context(), p.ID, r.Form["group_ids"], r.FormValue("new_group")); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/projects", 303)
}
func (a *App) updateProject(w http.ResponseWriter, r *http.Request) {
	err := a.store.UpdateProject(r.Context(), r.PathValue("id"), r.FormValue("name"), r.FormValue("repository_url"), r.FormValue("default_branch"), r.FormValue("local_path"), r.Form["board_ids"])
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := a.setProjectGroups(r.Context(), r.PathValue("id"), r.Form["group_ids"], r.FormValue("new_group")); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/projects", 303)
}
func (a *App) setProjectGroups(ctx context.Context, projectID string, ids []string, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName != "" {
		hash := sha1.Sum([]byte(strings.ToLower(newName)))
		palette := []string{"#3158d4", "#0f766e", "#a16207", "#9333ea", "#be123c", "#0369a1"}
		group, err := a.store.EnsureProjectGroup(ctx, newName, palette[int(hash[0])%len(palette)])
		if err != nil {
			return err
		}
		ids = append(ids, group.ID)
	}
	return a.store.SetProjectGroups(ctx, projectID, ids)
}
func (a *App) syncProject(w http.ResponseWriter, r *http.Request) {
	p, err := a.store.Project(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	_ = a.syncProjectRepo(r.Context(), p)
	http.Redirect(w, r, "/projects", 303)
}
func (a *App) deleteProject(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteProject(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/projects", 303)
}

// remoteDefaultBranch reads the remote HEAD instead of assuming that every
// repository uses "main". It is intentionally used only as a recovery path
// after a configured clone branch was rejected by the remote.
func remoteDefaultBranch(ctx context.Context, repositoryURL string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "ls-remote", "--symref", repositoryURL, "HEAD").Output()
	if err != nil {
		return "", err
	}
	return parseRemoteDefaultBranch(string(out))
}

func parseRemoteDefaultBranch(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD" {
			return strings.TrimPrefix(fields[1], "refs/heads/"), nil
		}
	}
	return "", errors.New("Remote-Standardbranch konnte nicht bestimmt werden")
}

func (a *App) syncProjectRepo(ctx context.Context, p domain.Project) error {
	// A manual refresh and the background loop may reach the same project at
	// once. Git worktrees cannot be cloned or fast-forwarded concurrently.
	lockValue, _ := a.projectSyncs.LoadOrStore(p.ID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	// Git can wait indefinitely for a remote or credential helper. Keep the
	// web request and the periodic synchronizer bounded.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if p.RepositoryURL == "" {
		return a.store.RecordProjectSync(ctx, p.ID, "Kein Repository hinterlegt")
	}
	path := p.LocalPath
	if path == "" {
		path = filepath.Join("/home/agent/.taskboard-projects", p.ID)
	}
	if !filepath.IsAbs(path) {
		return a.store.RecordProjectSync(ctx, p.ID, "Lokaler Pfad muss absolut sein")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return a.store.RecordProjectSync(ctx, p.ID, err.Error())
	}
	var command *exec.Cmd
	cloning := false
	if _, err := os.Stat(filepath.Join(path, ".git")); os.IsNotExist(err) {
		cloning = true
		command = exec.CommandContext(ctx, "git", "clone", "--branch", p.DefaultBranch, "--single-branch", p.RepositoryURL, path)
	} else {
		command = exec.CommandContext(ctx, "git", "-C", path, "pull", "--ff-only", "origin", p.DefaultBranch)
	}
	out, err := command.CombinedOutput()
	if err != nil && cloning && strings.Contains(string(out), "Remote branch ") {
		// A missing configured branch is common for imported repositories. The
		// target directory is absent after a failed git clone; if it is not, do
		// not overwrite any local material and surface the original error.
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			if branch, branchErr := remoteDefaultBranch(ctx, p.RepositoryURL); branchErr == nil && branch != "" && branch != p.DefaultBranch {
				out, err = exec.CommandContext(ctx, "git", "clone", "--branch", branch, "--single-branch", p.RepositoryURL, path).CombinedOutput()
				if err == nil {
					p.DefaultBranch = branch
					_ = a.store.SetProjectDefaultBranch(ctx, p.ID, branch)
				}
			}
		}
	}
	problem := ""
	if err != nil {
		problem = string(out)
		if len(problem) > 1000 {
			problem = problem[:1000]
		}
	}
	_ = a.store.RecordProjectSync(ctx, p.ID, problem)
	return err
}
func (a *App) syncProjectsLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		projects, err := a.store.Projects(context.Background())
		if err != nil {
			continue
		}
		for _, project := range projects {
			if project.RepositoryURL == "" {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_ = a.syncProjectRepo(ctx, project)
			cancel()
		}
	}
}
func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	from, to, e := dashboardRange(r)
	if e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	d, e := a.store.DashboardFiltered(r.Context(), from, to, r.URL.Query().Get("provider"), r.URL.Query().Get("model"), r.URL.Query().Get("agent"), r.URL.Query().Get("board"))
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	max := 1
	for _, m := range append(append([]domain.Metric{}, d.ByColumn...), d.ByPriority...) {
		if m.Count > max {
			max = m.Count
		}
	}
	a.render(r, w, "dashboard.html", map[string]any{"Dashboard": d, "Max": max})
}

// dashboardAPI is the first stable UI API used by the React/shadcn client.
// It deliberately returns the same domain projection as the legacy view so
// the migration does not duplicate business or metric logic in JavaScript.
func (a *App) dashboardAPI(w http.ResponseWriter, r *http.Request) {
	from, to, err := dashboardRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dashboard, err := a.store.DashboardFiltered(r.Context(), from, to, r.URL.Query().Get("provider"), r.URL.Query().Get("model"), r.URL.Query().Get("agent"), r.URL.Query().Get("board"))
	if err != nil {
		http.Error(w, "Dashboard-Daten sind momentan nicht verfügbar.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dashboard)
}

func dashboardRange(r *http.Request) (*time.Time, *time.Time, error) {
	value := r.URL.Query().Get("range")
	if value == "" || value == "all" {
		return nil, nil, nil
	}
	now := time.Now()
	to := now
	if value == "today" {
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return &start, &to, nil
	}
	if value == "7d" || value == "30d" {
		days := 7
		if value == "30d" {
			days = 30
		}
		start := now.AddDate(0, 0, -days)
		return &start, &to, nil
	}
	if value == "custom" {
		start, startErr := time.Parse("2006-01-02", r.URL.Query().Get("from"))
		end, endErr := time.Parse("2006-01-02", r.URL.Query().Get("to"))
		if startErr != nil || endErr != nil || !end.After(start) {
			return nil, nil, errors.New("Benutzerdefinierter Zeitraum ist ungültig")
		}
		end = end.AddDate(0, 0, 1)
		return &start, &end, nil
	}
	return nil, nil, errors.New("Unbekannter Dashboard-Zeitraum")
}

func writeAPI(w http.ResponseWriter, value any, err error) {
	if err != nil {
		http.Error(w, "Daten sind momentan nicht verfügbar.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func (a *App) boardsAPI(w http.ResponseWriter, r *http.Request) {
	value, err := a.store.ListBoards(r.Context())
	writeAPI(w, value, err)
}
func (a *App) boardTemplatesAPI(w http.ResponseWriter, r *http.Request) {
	writeAPI(w, store.BoardTemplates(), nil)
}
func (a *App) projectsAPI(w http.ResponseWriter, r *http.Request) {
	value, err := a.store.Projects(r.Context())
	writeAPI(w, value, err)
}
func (a *App) projectGroupsAPI(w http.ResponseWriter, r *http.Request) {
	value, err := a.store.ProjectGroups(r.Context())
	writeAPI(w, value, err)
}
func (a *App) agentsAPI(w http.ResponseWriter, r *http.Request) {
	value, err := a.store.Agents(r.Context())
	writeAPI(w, value, err)
}
func (a *App) agentAPI(w http.ResponseWriter, r *http.Request) {
	agent, err := a.store.GetAgent(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAPI(w, nil, err)
		return
	}
	skills, err := a.store.AgentSkills(r.Context(), agent.ID)
	writeAPI(w, map[string]any{"agent": agent, "skills": skills}, err)
}
func (a *App) automationsAPI(w http.ResponseWriter, r *http.Request) {
	value, err := a.store.Rules(r.Context())
	writeAPI(w, value, err)
}
func (a *App) schedulesAPI(w http.ResponseWriter, r *http.Request) {
	rules, err := a.store.Schedules(r.Context())
	writeAPI(w, rules, err)
}
func (a *App) webhooksAPI(w http.ResponseWriter, r *http.Request) {
	value, err := a.store.Webhooks(r.Context())
	writeAPI(w, value, err)
}
func (a *App) skillsAPI(w http.ResponseWriter, r *http.Request) {
	value, err := a.store.InstalledSkills(r.Context())
	writeAPI(w, value, err)
}
func (a *App) skillsSearchAPI(w http.ResponseWriter, r *http.Request) {
	value, err := skillcatalog.SearchSkillsSH(r.Context(), r.URL.Query().Get("q"))
	writeAPI(w, value, err)
}
func (a *App) installSkillAPI(w http.ResponseWriter, r *http.Request) {
	var input struct{ Source, Slug string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&input); err != nil {
		http.Error(w, "Ungültige Skill-Daten.", http.StatusBadRequest)
		return
	}
	if err := skillcatalog.InstallSkillsSH(r.Context(), a.store, input.Source, input.Slug); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *App) runsAPI(w http.ResponseWriter, r *http.Request) {
	before, beforeID, _, err := listCursor(r.URL.Query().Get("before"))
	if err != nil {
		http.Error(w, "Ungültiger Seitenzeiger.", http.StatusBadRequest)
		return
	}
	value, err := a.store.AllRunsBefore(r.Context(), 51, before, beforeID)
	if err != nil {
		writeAPI(w, nil, err)
		return
	}
	next := ""
	if len(value) > 50 {
		value = value[:50]
		last := value[len(value)-1]
		next = encodeListCursor(last.CreatedAt, last.ID)
	}
	writeAPI(w, map[string]any{"items": value, "next": next}, nil)
}
func (a *App) auditAPI(w http.ResponseWriter, r *http.Request) {
	before, beforeID, _, err := listCursor(r.URL.Query().Get("before"))
	if err != nil {
		http.Error(w, "Ungültiger Seitenzeiger.", http.StatusBadRequest)
		return
	}
	value, err := a.store.AuditEventsBefore(r.Context(), 51, before, beforeID)
	if err != nil {
		writeAPI(w, nil, err)
		return
	}
	next := ""
	if len(value) > 50 {
		value = value[:50]
		last := value[len(value)-1]
		next = encodeListCursor(last.CreatedAt, last.ID)
	}
	writeAPI(w, map[string]any{"items": value, "next": next}, nil)
}
func (a *App) providersAPI(w http.ResponseWriter, r *http.Request) {
	value, err := a.store.Providers(r.Context())
	writeAPI(w, value, err)
}
func (a *App) agentPolicyAPI(w http.ResponseWriter, r *http.Request) {
	prefix, suffix, err := a.store.AgentPromptPolicy(r.Context())
	writeAPI(w, map[string]string{"prefix": prefix, "suffix": suffix}, err)
}
func (a *App) appearanceAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	value, err := a.store.UserPreferences(r.Context(), u.ID)
	writeAPI(w, value, err)
}
func (a *App) integrationsAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	value, err := a.store.IntegrationConnections(r.Context(), u.ID)
	writeAPI(w, value, err)
}

func (a *App) updatesAPI(w http.ResponseWriter, r *http.Request) {
	snapshot := a.resolveUpdates(r)
	writeAPI(w, map[string]any{"current": snapshot.Current, "source": map[string]string{"provider": snapshot.Provider, "repository": snapshot.Repository, "branch": snapshot.Branch}, "status": snapshot.Status, "release": snapshot.Release, "installable": snapshot.Installable, "reason": snapshot.Reason, "checked_at": time.Now().UTC()}, nil)
}

func (a *App) resolveUpdates(r *http.Request) updates.Snapshot {
	current := updates.Current{Version: os.Getenv("TASKBOARD_VERSION"), Commit: os.Getenv("TASKBOARD_COMMIT_SHA"), BuiltAt: os.Getenv("TASKBOARD_BUILD_TIME")}
	if current.Version == "" {
		current.Version = "development"
	}
	if current.Commit == "" {
		current.Commit = "unknown"
	}
	repository := os.Getenv("TASKBOARD_GITHUB_REPOSITORY")
	if repository == "" {
		repository = "ralphschuler/Shipyard"
	}
	branch := os.Getenv("TASKBOARD_GITHUB_BRANCH")
	if branch == "" {
		branch = "master"
	}
	// Release metadata must always come from the configured GitHub API. Never
	// accept operator- or UI-supplied version, commit, checksum, or trust flags.
	return updates.Resolve(r.Context(), current, repository, branch, updates.Client{HTTP: http.DefaultClient, BaseURL: os.Getenv("TASKBOARD_GITHUB_API_URL"), Token: os.Getenv("TASKBOARD_GITHUB_TOKEN"), ApprovedTags: strings.Split(os.Getenv("TASKBOARD_GITHUB_RELEASE_ALLOWLIST"), ","), LocalChangelogPath: os.Getenv("TASKBOARD_CHANGELOG_PATH")})
}

func (a *App) installUpdateAPI(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&input); err != nil || !input.Confirm {
		http.Error(w, "Explizite Update-Bestätigung erforderlich.", http.StatusBadRequest)
		return
	}
	// Serialize the check/backup/mutation sequence inside this process. A
	// second browser tab must not pass the busy check while the first update is
	// already replacing the binary.
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	if a.update == nil {
		// No generic shell/binary replacement is permitted. The deployment-specific
		// adapter must be supplied before an installation can mutate this process.
		http.Error(w, "Update geprüft, aber kein sicherer Installationsadapter ist konfiguriert.", http.StatusServiceUnavailable)
		return
	}
	if a.store != nil {
		conn, err := a.store.DB.Acquire(r.Context())
		if err != nil {
			http.Error(w, "Update-Sperre konnte nicht sicher geprüft werden.", http.StatusServiceUnavailable)
			return
		}
		defer conn.Release()
		var locked bool
		err = conn.QueryRow(r.Context(), `SELECT pg_try_advisory_lock(hashtextextended('shipyard:update-install', 0))`).Scan(&locked)
		if err != nil {
			http.Error(w, "Update-Sperre konnte nicht sicher geprüft werden.", http.StatusServiceUnavailable)
			return
		}
		if !locked {
			http.Error(w, "Eine andere Update-Installation läuft bereits.", http.StatusConflict)
			return
		}
		defer func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended('shipyard:update-install', 0))`)
		}()
		currentSessionHash := ""
		if cookie, cookieErr := r.Cookie("taskboard_session"); cookieErr == nil {
			currentSessionHash = tokenHash(cookie.Value)
		}
		busy, err := a.activeUpdateRuns(r.Context(), currentSessionHash)
		if err != nil {
			http.Error(w, "Aktive Runs konnten nicht sicher geprüft werden.", http.StatusServiceUnavailable)
			return
		}
		if busy {
			http.Error(w, "Aktive Runs oder Workspaces blockieren die Update-Installation.", http.StatusConflict)
			return
		}
	}
	snapshot := a.resolveUpdates(r)
	if !snapshot.Installable || snapshot.Status != "update_available" {
		if err := a.auditUpdate(r, "update.install_blocked", snapshot, "snapshot_not_installable"); err != nil {
			http.Error(w, "Update-Audit konnte nicht sicher geschrieben werden.", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "Update ist nicht verifiziert und installierbar.", http.StatusConflict)
		return
	}
	if err := a.auditUpdate(r, "update.install_confirmed", snapshot, "confirmed"); err != nil {
		http.Error(w, "Update-Audit konnte nicht sicher geschrieben werden.", http.StatusServiceUnavailable)
		return
	}
	progress := make([]updates.Progress, 0, 12)
	var auditErr error
	installCtx, cancelInstall := context.WithCancel(r.Context())
	defer cancelInstall()
	if err := a.update.Install(installCtx, snapshot, func(p updates.Progress) {
		progress = append(progress, p)
		if p.Status == "running" {
			if err := a.auditUpdate(r, "update."+p.Phase, snapshot, "started"); err != nil && auditErr == nil {
				auditErr = err
				cancelInstall()
			}
		} else if p.Status == "succeeded" {
			if err := a.auditUpdate(r, "update."+p.Phase, snapshot, "succeeded"); err != nil && auditErr == nil {
				auditErr = err
			}
		} else if p.Status == "failed" {
			if err := a.auditUpdate(r, "update."+p.Phase, snapshot, "failed"); err != nil && auditErr == nil {
				auditErr = err
			}
		}
	}); err != nil {
		if auditErr != nil {
			log.Printf("update audit failed during installation: %v", auditErr)
		}
		if auditFailure := a.auditUpdate(r, "update.install_failed", snapshot, "failed"); auditFailure != nil {
			log.Printf("update failure audit failed: %v", auditFailure)
		}
		http.Error(w, updateInstallErrorMessage(err), http.StatusConflict)
		return
	}
	if auditErr != nil {
		log.Printf("update audit failed after installation: %v", auditErr)
		http.Error(w, "Update wurde ausgeführt, aber nicht vollständig auditiert.", http.StatusServiceUnavailable)
		return
	}
	if err := a.auditUpdate(r, "update.install_succeeded", snapshot, "succeeded"); err != nil {
		log.Printf("update audit failed after successful installation: %v", err)
		http.Error(w, "Update wurde ausgeführt, aber nicht vollständig auditiert.", http.StatusServiceUnavailable)
		return
	}
	writeAPI(w, map[string]any{"status": "succeeded", "progress": progress}, nil)
	if a.update.AfterSuccess != nil {
		a.update.AfterSuccess()
	}
}

// Adapter errors can contain filesystem paths, command lines, or deployment
// details. Keep those details in server-side diagnostics, never in the API
// response rendered by the browser.
func updateInstallErrorMessage(error) string {
	return "Update konnte nicht sicher installiert werden. Die Wiederherstellung wurde geprüft."
}

func activeUpdateRunsQuery(currentSessionHash string) (string, []any) {
	// Other browser sessions are not an update hazard: they survive a normal
	// service restart and treating them as active work made stale SSO sessions
	// block every installation. Only durable agent work is protected here.
	_ = currentSessionHash
	return `SELECT EXISTS(
		SELECT 1 FROM agent_runs WHERE status IN ('queued','running') AND workspace_snapshot <> ''
	) OR EXISTS(
		SELECT 1 FROM agent_run_batches WHERE status IN ('queued','running')
	)`, nil
}

func (a *App) activeUpdateRuns(ctx context.Context, currentSessionHash string) (bool, error) {
	var busy bool
	query, args := activeUpdateRunsQuery(currentSessionHash)
	err := a.store.DB.QueryRow(ctx, query, args...).Scan(&busy)
	return busy, err
}

func (a *App) auditUpdate(r *http.Request, kind string, snapshot updates.Snapshot, result string) error {
	if a.store == nil {
		return nil
	}
	actor := ""
	if user, ok := currentUser(r.Context()); ok {
		actor = user.ID
	}
	return a.store.RecordAudit(r.Context(), actor, kind, "update", snapshot.Release.Commit, map[string]string{
		"result": result, "version": snapshot.Release.Version, "repository": snapshot.Repository,
	})
}
func (a *App) accountAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	tokens, err := a.store.APITokens(r.Context(), u.ID)
	writeAPI(w, map[string]any{"user": u, "tokens": tokens}, err)
}
func (a *App) createAccountTokenAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := currentUser(r.Context())
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	var input struct{ Name string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&input); err != nil || strings.TrimSpace(input.Name) == "" {
		http.Error(w, "Tokenname ist erforderlich", http.StatusBadRequest)
		return
	}
	raw := "tb_" + randomToken()
	token, err := a.store.CreateAPIToken(r.Context(), u.ID, strings.TrimSpace(input.Name), tokenHash(raw), raw[:11], nil)
	writeAPI(w, map[string]any{"token": raw, "record": token}, err)
}
func (a *App) boardAPI(w http.ResponseWriter, r *http.Request) {
	value, err := a.page(r.Context(), r.PathValue("id"), "", a.accountLanguage(r))
	if err == nil {
		value.Tasks = filterBoardTasks(value.Tasks, value.Board.ID, r.URL.Query())
	}
	writeAPI(w, value, err)
}

// filterBoardTasks keeps API filtering consistent with the client-side board
// view. The board is loaded first so project matching uses effective targets,
// including the implicit single-project board fallback.
func filterBoardTasks(tasks []domain.Task, boardID string, query url.Values) []domain.Task {
	search := strings.ToLower(strings.TrimSpace(query.Get("search")))
	column, priority, label, project := query.Get("column"), query.Get("priority"), query.Get("label"), query.Get("project")
	if boardID == "" && search == "" && column == "" && priority == "" && label == "" && project == "" {
		return tasks
	}
	filtered := make([]domain.Task, 0, len(tasks))
	for _, task := range tasks {
		if boardID != "" && task.BoardID != boardID {
			continue
		}
		textMatches := search == "" || strings.Contains(strings.ToLower(task.Title+" "+task.Description), search)
		labelMatches := label == ""
		if !labelMatches {
			for _, taskLabel := range task.Labels {
				if taskLabel.ID == label {
					labelMatches = true
					break
				}
			}
		}
		projectMatches := project == ""
		if !projectMatches {
			for _, target := range task.TargetProjects {
				if target.ID == project {
					projectMatches = true
					break
				}
			}
		}
		if textMatches && (column == "" || task.ColumnID == column) && (priority == "" || task.Priority == priority) && labelMatches && projectMatches {
			filtered = append(filtered, task)
		}
	}
	return filtered
}
func (a *App) taskAPI(w http.ResponseWriter, r *http.Request) {
	value, err := a.taskData(r.Context(), r.PathValue("id"))
	writeAPI(w, value, err)
}
func (a *App) runAPI(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.Run(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAPI(w, nil, err)
		return
	}
	logs, truncated, err := a.store.RecentRunLogs(r.Context(), run.ID, browserRunLogLimit)
	if err != nil {
		writeAPI(w, nil, err)
		return
	}
	task, err := a.store.GetTask(r.Context(), run.TaskID)
	if err != nil {
		writeAPI(w, nil, err)
		return
	}
	delivery, err := a.store.RunDelivery(r.Context(), run.ID)
	usage, usageErr := a.store.RunUsage(r.Context(), run.ID)
	if usageErr != nil {
		writeAPI(w, nil, usageErr)
		return
	}
	for index := range logs {
		logs[index].Message = automation.RedactSensitiveText(logs[index].Message)
	}
	queue, queueErr := a.store.RunQueueStatus(r.Context(), run.ID)
	if queueErr != nil {
		writeAPI(w, nil, queueErr)
		return
	}
	writeAPI(w, map[string]any{"run": safeRunView(run), "queue": queue, "task": task, "delivery": delivery, "usage": usage, "logs": logs, "logsTruncated": truncated}, err)
}
func safeRunView(run domain.AgentRun) map[string]any {
	return map[string]any{"ID": run.ID, "TaskID": run.TaskID, "AgentID": run.AgentID, "RuleID": run.RuleID, "BatchID": run.BatchID, "Status": run.Status, "TargetProject": run.TargetProject, "Summary": automation.RedactSensitiveText(run.Summary), "ErrorMessage": automation.RedactSensitiveText(run.ErrorMessage), "StartedAt": run.StartedAt, "FinishedAt": run.FinishedAt, "CreatedAt": run.CreatedAt}
}

func (a *App) runLogsAPI(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.Run(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAPI(w, nil, err)
		return
	}
	before, err := strconv.Atoi(r.URL.Query().Get("before"))
	if r.URL.Query().Get("before") != "" && (err != nil || before < 2) {
		http.Error(w, "Ungültiger Log-Seitenzeiger.", http.StatusBadRequest)
		return
	}
	after, err := strconv.Atoi(r.URL.Query().Get("after"))
	if r.URL.Query().Get("after") != "" && (err != nil || after < 0) {
		http.Error(w, "Ungültiger Log-Fortsetzungszeiger.", http.StatusBadRequest)
		return
	}
	if before > 0 && after > 0 {
		http.Error(w, "Es kann nur eine Log-Richtung geladen werden.", http.StatusBadRequest)
		return
	}
	var entries []domain.RunLog
	var truncated, hasMore bool
	if before > 0 {
		entries, truncated, err = a.store.RunLogsBefore(r.Context(), run.ID, before, browserRunLogLimit)
	} else if after > 0 {
		entries, hasMore, err = a.store.RunLogsAfter(r.Context(), run.ID, after, browserRunLogLimit)
	} else {
		entries, truncated, err = a.store.RecentRunLogs(r.Context(), run.ID, browserRunLogLimit)
	}
	for index := range entries {
		entries[index].Message = automation.RedactSensitiveText(entries[index].Message)
	}
	writeAPI(w, map[string]any{"entries": entries, "truncated": truncated, "hasMore": hasMore, "status": run.Status}, err)
}
func (a *App) createBoardAPI(w http.ResponseWriter, r *http.Request) {
	var input struct{ Name, Template string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&input); err != nil {
		http.Error(w, "Ungültige Board-Daten.", http.StatusBadRequest)
		return
	}
	value, err := a.store.CreateBoardWithTemplate(r.Context(), input.Name, defaultString(input.Template, "software"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(value)
}
func (a *App) dashboardAttention(w http.ResponseWriter, r *http.Request) {
	attention, err := a.store.DashboardAttention(r.Context())
	if err != nil {
		http.Error(w, "Dashboard-Daten sind momentan nicht verfügbar.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(attention)
}
func (a *App) createBoard(w http.ResponseWriter, r *http.Request) {
	b, e := a.store.CreateBoardWithTemplate(r.Context(), r.FormValue("name"), defaultString(r.FormValue("template"), "software"))
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/boards/"+b.ID, http.StatusSeeOther)
}
func (a *App) page(c context.Context, id, errText string, lang string) (boardPage, error) {
	b, e := a.store.GetBoard(c, id)
	if e != nil {
		return boardPage{}, e
	}
	cols, e := a.store.Columns(c, id)
	if e != nil {
		return boardPage{}, e
	}
	tasks, e := a.store.Tasks(c, id)
	if e != nil {
		return boardPage{}, e
	}
	targetProjects, e := a.store.EffectiveTaskTargetProjectsForTasks(c, tasks)
	if e != nil {
		return boardPage{}, e
	}
	for i := range tasks {
		tasks[i].TargetProjects = targetProjects[tasks[i].ID]
	}
	tr, e := a.store.Transitions(c, id)
	labels, labelErr := a.store.Labels(c, id)
	if labelErr != nil {
		return boardPage{}, labelErr
	}
	projects, projectErr := a.store.BoardProjects(c, id)
	if projectErr != nil {
		return boardPage{}, projectErr
	}
	groups, groupErr := a.store.ProjectGroups(c)
	if groupErr != nil {
		return boardPage{}, groupErr
	}
	return boardPage{Board: b, Columns: cols, Tasks: tasks, Transitions: tr, Error: errText, Lang: lang, Labels: labels, Projects: projects, Groups: groups}, e
}
func (a *App) board(w http.ResponseWriter, r *http.Request) {
	p, e := a.page(r.Context(), r.PathValue("id"), "", a.accountLanguage(r))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	a.render(r, w, "board.html", p)
}
func (a *App) updateBoard(w http.ResponseWriter, r *http.Request) {
	if err := a.store.UpdateBoard(r.Context(), r.PathValue("id"), r.FormValue("name")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/boards/"+r.PathValue("id"), http.StatusSeeOther)
}
func (a *App) deleteBoard(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteBoard(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *App) createTask(w http.ResponseWriter, r *http.Request) {
	task, e := a.store.CreateTask(r.Context(), r.PathValue("id"), r.FormValue("title"), r.FormValue("description"), defaultString(r.FormValue("priority"), "normal"), r.FormValue("start_date"), r.FormValue("due_date"), "web")
	if e != nil {
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			http.Error(w, e.Error(), http.StatusBadRequest)
			return
		}
		p, _ := a.page(r.Context(), r.PathValue("id"), e.Error(), a.accountLanguage(r))
		a.render(r, w, "board.html", p)
		return
	}
	if e = a.store.SetLabels(r.Context(), task.ID, r.Form["label_ids"]); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	if e = a.store.SetTaskTargets(r.Context(), task.ID, r.Form["target_project_ids"], r.Form["target_group_ids"]); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/boards/"+r.PathValue("id"), http.StatusSeeOther)
}
func (a *App) createLabel(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "Tag-Name fehlt.", http.StatusBadRequest)
		return
	}
	if _, err := a.store.CreateLabel(r.Context(), r.PathValue("id"), name, defaultString(r.FormValue("color"), "#176f8a")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/boards/"+r.PathValue("id"), http.StatusSeeOther)
}
func (a *App) addColumn(w http.ResponseWriter, r *http.Request) {
	_, e := a.store.AddColumn(r.Context(), r.PathValue("id"), r.FormValue("name"), r.FormValue("column_type"))
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/boards/"+r.PathValue("id")+"/workflow", 303)
}
func (a *App) workflow(w http.ResponseWriter, r *http.Request) {
	p, e := a.page(r.Context(), r.PathValue("id"), "", a.accountLanguage(r))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	a.render(r, w, "workflow.html", p)
}
func (a *App) addTransition(w http.ResponseWriter, r *http.Request) {
	_, e := a.store.AddTransition(r.Context(), r.PathValue("id"), r.FormValue("from"), r.FormValue("to"), r.FormValue("action_name"))
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/boards/"+r.PathValue("id")+"/workflow", 303)
}
func (a *App) updateColumn(w http.ResponseWriter, r *http.Request) {
	x, errX := strconv.Atoi(r.FormValue("x"))
	y, errY := strconv.Atoi(r.FormValue("y"))
	if errX != nil || errY != nil {
		column, err := a.store.Column(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, "column not found", http.StatusNotFound)
			return
		}
		x, y = column.CanvasX, column.CanvasY
	}
	column, err := a.store.Column(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "column not found", http.StatusNotFound)
		return
	}
	typeName := r.FormValue("column_type")
	if typeName == "" {
		typeName = column.Type
	}
	if e := a.store.UpdateColumn(r.Context(), r.PathValue("id"), r.FormValue("name"), typeName, x, y); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, r.Referer(), 303)
}
func (a *App) updateColumnPosition(w http.ResponseWriter, r *http.Request) {
	x, errX := strconv.Atoi(r.FormValue("x"))
	y, errY := strconv.Atoi(r.FormValue("y"))
	if errX != nil || errY != nil || x < 0 || y < 0 {
		http.Error(w, "invalid canvas position", http.StatusBadRequest)
		return
	}
	if err := a.store.UpdateColumnPosition(r.Context(), r.PathValue("id"), x, y); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *App) deleteColumn(w http.ResponseWriter, r *http.Request) {
	if e := a.store.DeleteColumn(r.Context(), r.PathValue("id")); e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	http.Redirect(w, r, r.Referer(), 303)
}
func (a *App) deleteTransition(w http.ResponseWriter, r *http.Request) {
	if e := a.store.DeleteTransition(r.Context(), r.PathValue("id")); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, r.Referer(), 303)
}
func (a *App) updateTransition(w http.ResponseWriter, r *http.Request) {
	if err := a.store.UpdateTransition(r.Context(), r.PathValue("id"), r.FormValue("from"), r.FormValue("to"), r.FormValue("action_name")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, r.Referer(), http.StatusSeeOther)
}
func (a *App) task(w http.ResponseWriter, r *http.Request) {
	p, err := a.taskData(r.Context(), r.PathValue("id"))
	if err != nil {
		log.Printf("task page %s: %v", r.PathValue("id"), err)
		http.NotFound(w, r)
		return
	}
	a.render(r, w, "task.html", p)
}
func (a *App) taskPanel(w http.ResponseWriter, r *http.Request) {
	p, err := a.taskData(r.Context(), r.PathValue("id"))
	if err != nil {
		log.Printf("task panel %s: %v", r.PathValue("id"), err)
		http.NotFound(w, r)
		return
	}
	a.render(r, w, "task-panel.html", p)
}
func (a *App) taskData(ctx context.Context, id string) (taskPage, error) {
	t, e := a.store.GetTask(ctx, id)
	if e != nil {
		return taskPage{}, e
	}
	allowed, e := a.store.Allowed(ctx, t.ID)
	if e != nil {
		return taskPage{}, e
	}
	cols, e := a.store.Columns(ctx, t.BoardID)
	if e != nil {
		return taskPage{}, e
	}
	names := map[string]string{}
	for _, c := range cols {
		names[c.ID] = c.Name
	}
	h, e := a.store.History(ctx, t.ID)
	if e != nil {
		return taskPage{}, e
	}
	comments, e := a.store.Comments(ctx, t.ID)
	if e != nil {
		return taskPage{}, e
	}
	interactions, e := a.store.OpenInteractions(ctx, t.ID)
	if e != nil {
		return taskPage{}, e
	}
	views := make([]interactionView, 0, len(interactions))
	for _, interaction := range interactions {
		var schema interactionSchema
		if json.Unmarshal(interaction.Schema, &schema) == nil && len(schema.Fields) > 0 {
			for fieldIndex := range schema.Fields {
				for optionIndex := range schema.Fields[fieldIndex].Options {
					schema.Fields[fieldIndex].Options[optionIndex].FieldID = schema.Fields[fieldIndex].ID
				}
			}
			views = append(views, interactionView{AgentInteraction: interaction, Fields: schema.Fields})
		}
	}
	boardLabels, e := a.store.Labels(ctx, t.BoardID)
	if e != nil {
		return taskPage{}, e
	}
	runs, e := a.store.RunsForTask(ctx, t.ID)
	if e != nil {
		return taskPage{}, e
	}
	publicRuns := make([]taskRunView, 0, len(runs))
	changes := make([]taskChangeView, 0, len(runs))
	for _, run := range runs {
		queue, queueErr := a.store.RunQueueStatus(ctx, run.ID)
		if queueErr != nil {
			return taskPage{}, queueErr
		}
		publicRuns = append(publicRuns, taskRunView{ID: run.ID, TaskID: run.TaskID, AgentID: run.AgentID, Status: run.Status, Queue: queue, Summary: automation.RedactSensitiveText(run.Summary), ErrorMessage: automation.RedactSensitiveText(run.ErrorMessage), StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, CreatedAt: run.CreatedAt})
		delivery, deliveryErr := a.store.RunDelivery(ctx, run.ID)
		if deliveryErr != nil {
			return taskPage{}, deliveryErr
		}
		changes = append(changes, taskChangeView{
			ID: run.ID, Status: run.Status, Summary: automation.RedactSensitiveText(run.Summary),
			ErrorMessage: automation.RedactSensitiveText(run.ErrorMessage), DiffSummary: delivery.DiffSummary,
			GateStatus: delivery.GateStatus,
			CreatedAt:  run.CreatedAt, FinishedAt: run.FinishedAt,
			AppliedAt: delivery.AppliedAt,
		})
	}
	agents, e := a.store.Agents(ctx)
	if e != nil {
		return taskPage{}, e
	}
	projects, e := a.store.Projects(ctx)
	if e != nil {
		return taskPage{}, e
	}
	groups, e := a.store.ProjectGroups(ctx)
	if e != nil {
		return taskPage{}, e
	}
	targetProjects, e := a.store.EffectiveTaskTargetProjects(ctx, t.ID)
	if e != nil {
		return taskPage{}, e
	}
	targetGroups, e := a.store.TaskTargetGroups(ctx, t.ID)
	if e != nil {
		return taskPage{}, e
	}
	targets, e := a.store.EffectiveTaskRepositoryTargets(ctx, t.ID)
	if e != nil {
		return taskPage{}, e
	}
	return taskPage{ID: t.ID, Task: t, Allowed: allowed, Columns: names, History: h, Comments: displayComments(comments), Interactions: views, BoardLabels: boardLabels, Runs: publicRuns, Changes: changes, Agents: agents, Projects: projects, Groups: groups, TargetProjects: targetProjects, TargetGroups: targetGroups, Targets: targets}, nil
}
func (a *App) startRun(w http.ResponseWriter, r *http.Request) {
	runs, e := a.store.CreateManualRuns(r.Context(), r.PathValue("id"), r.FormValue("agent_id"))
	if e != nil {
		if errors.Is(e, store.ErrTaskAgentActive) {
			http.Error(w, "Dieser Agent arbeitet bereits an dieser Aufgabe. Öffne den laufenden Run oder warte auf dessen Abschluss.", http.StatusConflict)
			return
		}
		http.Error(w, e.Error(), 409)
		return
	}
	go a.worker.Process(context.Background())
	http.Redirect(w, r, "/runs/"+runs[0].ID, 303)
}

// handoffTask makes specialist collaboration explicit. The handoff note is a
// normal task comment, therefore the next agent receives it in the immutable
// task context instead of relying on hidden routing state.
func (a *App) handoffTask(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Übergabe konnte nicht gelesen werden.", http.StatusBadRequest)
		return
	}
	taskID, agentID := r.PathValue("id"), strings.TrimSpace(r.FormValue("agent_id"))
	note := strings.TrimSpace(r.FormValue("note"))
	if agentID == "" || note == "" {
		http.Error(w, "Wähle einen Agenten und beschreibe die Übergabe.", http.StatusBadRequest)
		return
	}
	agent, err := a.store.GetAgent(r.Context(), agentID)
	if err != nil || !agent.Enabled {
		http.Error(w, "Der gewählte Agent ist nicht verfügbar.", http.StatusBadRequest)
		return
	}
	user, _ := currentUser(r.Context())
	author := user.DisplayName
	if author == "" {
		author = "Du"
	}
	if err = a.store.AddComment(r.Context(), taskID, author, "Übergabe an "+agent.Name+":\n"+note); err != nil {
		http.Error(w, "Übergabe konnte nicht gespeichert werden.", http.StatusBadRequest)
		return
	}
	if r.FormValue("start") != "true" {
		http.Redirect(w, r, "/tasks/"+taskID, http.StatusSeeOther)
		return
	}
	runs, err := a.store.CreateManualRuns(r.Context(), taskID, agentID)
	if err != nil {
		http.Error(w, "Übergabe gespeichert; Folge-Run konnte nicht gestartet werden: "+err.Error(), http.StatusConflict)
		return
	}
	go a.worker.Process(context.Background())
	http.Redirect(w, r, "/runs/"+runs[0].ID, http.StatusSeeOther)
}
func (a *App) requestDecision(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Entscheidungsbedarf konnte nicht gelesen werden.", http.StatusBadRequest)
		return
	}
	taskID := r.PathValue("id")
	question := strings.TrimSpace(r.FormValue("question"))
	if question == "" {
		http.Error(w, "Formuliere die benötigte Entscheidung.", http.StatusBadRequest)
		return
	}
	user, _ := currentUser(r.Context())
	author := user.DisplayName
	if author == "" {
		author = "Du"
	}
	if err := a.store.AddComment(r.Context(), taskID, author, "Menschliche Entscheidung benötigt:\n"+question); err != nil {
		http.Error(w, "Entscheidung konnte nicht gespeichert werden: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.store.CreateNotification(r.Context(), taskID, "", "needs_decision", "Eine menschliche Entscheidung wird benötigt: "+question); err != nil {
		http.Error(w, "Entscheidung gespeichert, Benachrichtigung fehlgeschlagen: "+err.Error(), http.StatusConflict)
		return
	}
	if _, err := a.store.MoveTaskToNeedsActionForHumanDecision(r.Context(), taskID); err != nil {
		http.Error(w, "Entscheidung gespeichert, Task konnte nicht verschoben werden: "+err.Error(), http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/tasks/"+taskID, http.StatusSeeOther)
}
func (a *App) setTaskTargets(w http.ResponseWriter, r *http.Request) {
	if err := a.store.SetTaskTargets(r.Context(), r.PathValue("id"), r.Form["target_project_ids"], r.Form["target_group_ids"]); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/tasks/"+r.PathValue("id"), 303)
}
func (a *App) runs(w http.ResponseWriter, r *http.Request) {
	before, beforeID, isOlderPage, err := listCursor(r.URL.Query().Get("before"))
	if err != nil {
		http.Error(w, "Ungültiger Seitenzeiger.", http.StatusBadRequest)
		return
	}
	runs, err := a.store.AllRunsBefore(r.Context(), 51, before, beforeID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	hasOlder := len(runs) > 50
	if hasOlder {
		runs = runs[:50]
	}
	page := runsPage{Runs: runs, HasOlder: hasOlder, IsOlderPage: isOlderPage}
	if hasOlder && len(runs) > 0 {
		page.OlderCursor = encodeListCursor(runs[len(runs)-1].CreatedAt, runs[len(runs)-1].ID)
	}
	a.render(r, w, "runs.html", page)
}
func (a *App) audit(w http.ResponseWriter, r *http.Request) {
	before, beforeID, isOlderPage, err := listCursor(r.URL.Query().Get("before"))
	if err != nil {
		http.Error(w, "Ungültiger Seitenzeiger.", http.StatusBadRequest)
		return
	}
	events, err := a.store.AuditEventsBefore(r.Context(), 51, before, beforeID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	hasOlder := len(events) > 50
	if hasOlder {
		events = events[:50]
	}
	page := auditPage{Events: events, HasOlder: hasOlder, IsOlderPage: isOlderPage}
	if hasOlder && len(events) > 0 {
		page.OlderCursor = encodeListCursor(events[len(events)-1].CreatedAt, events[len(events)-1].ID)
	}
	a.render(r, w, "audit.html", page)
}

func encodeListCursor(createdAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func listCursor(raw string) (*time.Time, string, bool, error) {
	if raw == "" {
		return nil, "", false, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, "", false, err
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return nil, "", false, errors.New("invalid cursor")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, "", false, err
	}
	return &createdAt, parts[1], true, nil
}
func (a *App) run(w http.ResponseWriter, r *http.Request) {
	run, e := a.store.Run(r.Context(), r.PathValue("id"))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	logs, truncated, e := a.store.RecentRunLogs(r.Context(), run.ID, browserRunLogLimit)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	task, e := a.store.GetTask(r.Context(), run.TaskID)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	delivery, e := a.store.RunDelivery(r.Context(), run.ID)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	usage, e := a.store.RunUsage(r.Context(), run.ID)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	queue, e := a.store.RunQueueStatus(r.Context(), run.ID)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	a.render(r, w, "run.html", runPage{Run: run, Queue: queue, Logs: newRunLogView(logs, truncated, run.ID), Task: task, Delivery: delivery, Usage: usage})
}
func (a *App) runLogs(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.Run(r.Context(), r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	before, err := strconv.Atoi(r.URL.Query().Get("before"))
	if r.URL.Query().Get("before") != "" && (err != nil || before < 2) {
		http.Error(w, "ungültige Log-Seite", http.StatusBadRequest)
		return
	}
	var logs []domain.RunLog
	var truncated bool
	if before > 0 {
		logs, truncated, err = a.store.RunLogsBefore(r.Context(), run.ID, before, browserRunLogLimit)
	} else {
		logs, truncated, err = a.store.RecentRunLogs(r.Context(), run.ID, browserRunLogLimit)
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("X-Run-Status", run.Status)
	a.render(r, w, "run-log.html", newRunLogView(logs, truncated, run.ID))
}

func newRunLogView(entries []domain.RunLog, truncated bool, runID string) runLogView {
	view := runLogView{Entries: entries, Truncated: truncated, RunID: runID, Limit: browserRunLogLimit}
	if len(entries) > 0 {
		view.OldestSequence = entries[0].Sequence
	}
	return view
}
func (a *App) runLogEntry(w http.ResponseWriter, r *http.Request) {
	if _, err := a.store.Run(r.Context(), r.PathValue("id")); err != nil {
		http.NotFound(w, r)
		return
	}
	sequence, err := strconv.Atoi(r.PathValue("sequence"))
	if err != nil || sequence < 1 {
		http.NotFound(w, r)
		return
	}
	entry, err := a.store.RunLog(r.Context(), r.PathValue("id"), sequence)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, automation.RedactSensitiveText(entry.Message))
}
func (a *App) runTrace(w http.ResponseWriter, r *http.Request) {
	trace, err := a.store.RunTrace(r.Context(), r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(trace)
}
func (a *App) restartRun(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.Run(r.Context(), r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if run.Status == "queued" || run.Status == "running" {
		http.Error(w, "Laufenden Run zuerst abbrechen oder abwarten.", 409)
		return
	}
	runs, err := a.store.CreateManualRuns(r.Context(), run.TaskID, run.AgentID)
	if err != nil {
		if errors.Is(err, store.ErrTaskAgentActive) {
			http.Error(w, "Für diesen Agenten läuft bereits ein Run zu dieser Aufgabe.", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), 409)
		return
	}
	go a.worker.Process(context.Background())
	http.Redirect(w, r, "/runs/"+runs[0].ID, http.StatusSeeOther)
}
func (a *App) cancelRun(w http.ResponseWriter, r *http.Request) {
	if e := a.worker.Cancel(r.Context(), r.PathValue("id")); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/runs/"+r.PathValue("id"), 303)
}
func (a *App) applyRun(w http.ResponseWriter, r *http.Request) {
	if e := a.worker.Apply(r.Context(), r.PathValue("id")); e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	http.Redirect(w, r, "/runs/"+r.PathValue("id"), 303)
}
func (a *App) discardRun(w http.ResponseWriter, r *http.Request) {
	if e := a.worker.Discard(r.Context(), r.PathValue("id")); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/runs/"+r.PathValue("id"), 303)
}
func (a *App) runDiff(w http.ResponseWriter, r *http.Request) {
	if _, err := a.store.Run(r.Context(), r.PathValue("id")); err != nil {
		http.NotFound(w, r)
		return
	}
	diff, err := a.worker.Diff(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "Diff ist für diesen Run nicht verfügbar.", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(diff))
}
func (a *App) rejectRun(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Feedback konnte nicht gelesen werden.", http.StatusBadRequest)
		return
	}
	feedback := strings.TrimSpace(r.FormValue("feedback"))
	if feedback == "" {
		http.Error(w, "Beschreibe kurz, was der Agent beim nächsten Versuch ändern soll.", http.StatusBadRequest)
		return
	}
	run, err := a.store.Run(r.Context(), r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user, _ := currentUser(r.Context())
	author := user.DisplayName
	if author == "" {
		author = "Du"
	}
	if err = a.store.AddComment(r.Context(), run.TaskID, author, "Run abgelehnt. Feedback für den nächsten Agentenversuch:\n"+feedback); err != nil {
		http.Error(w, "Feedback konnte nicht gespeichert werden: "+err.Error(), http.StatusConflict)
		return
	}
	if err = a.worker.Discard(r.Context(), run.ID); err != nil {
		http.Error(w, "Feedback wurde gespeichert, Run konnte nicht verworfen werden: "+err.Error(), http.StatusConflict)
		return
	}
	if r.FormValue("restart") != "true" {
		http.Redirect(w, r, "/tasks/"+run.TaskID, http.StatusSeeOther)
		return
	}
	runs, err := a.store.CreateManualRuns(r.Context(), run.TaskID, run.AgentID)
	if err != nil {
		http.Error(w, "Feedback gespeichert; der neue Run konnte nicht angelegt werden: "+err.Error(), http.StatusConflict)
		return
	}
	go a.worker.Process(context.Background())
	http.Redirect(w, r, "/runs/"+runs[0].ID, http.StatusSeeOther)
}
func (a *App) readNotification(w http.ResponseWriter, r *http.Request) {
	if e := a.store.MarkNotificationRead(r.Context(), r.PathValue("id")); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/", 303)
}
func (a *App) addComment(w http.ResponseWriter, r *http.Request) {
	author := "Web-App"
	if user, ok := currentUser(r.Context()); ok {
		author = user.DisplayName
	}
	if err := a.store.AddComment(r.Context(), r.PathValue("id"), author, r.FormValue("body")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Header.Get("X-Task-Panel") == "true" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/tasks/"+r.PathValue("id"), http.StatusSeeOther)
}
func (a *App) answerInteraction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Antwort konnte nicht gelesen werden.", http.StatusBadRequest)
		return
	}
	before, err := a.store.Interaction(r.Context(), r.PathValue("id"))
	if err != nil || before.Status != "open" {
		http.Error(w, "Diese Agentenfrage wurde bereits beantwortet oder existiert nicht.", http.StatusConflict)
		return
	}
	user, _ := currentUser(r.Context())
	response := map[string][]string{}
	freeform := strings.TrimSpace(r.FormValue("freeform_answer"))
	for key, values := range r.PostForm {
		if key != "csrf_token" && key != "freeform_answer" && key != "next_column_id" {
			response[key] = values
		}
	}
	// Older pages use the task id as the name of a direct-answer button. Map
	// that value back to the one declared button field so persisted responses
	// always have the documented schema. Newer markup uses the field id.
	response = normalizeInteractionResponse(before.Schema, before.TaskID, response)
	if len(response) == 0 && freeform == "" {
		http.Error(w, "Wähle eine Antwort oder gib eine eigene Antwort ein.", http.StatusBadRequest)
		return
	}
	raw, _ := json.Marshal(response)
	_, runs, err := a.store.ResolveInteractionAndMove(r.Context(), r.PathValue("id"), user.DisplayName, freeform, raw, r.FormValue("next_column_id"))
	if err != nil {
		http.Error(w, "Antwort konnte nicht vollständig verarbeitet werden: "+err.Error(), http.StatusConflict)
		return
	}
	go a.worker.Process(context.Background())
	if len(runs) == 0 {
		http.Redirect(w, r, "/tasks/"+before.TaskID, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/runs/"+runs[0].ID, http.StatusSeeOther)
}
func (a *App) moveTask(w http.ResponseWriter, r *http.Request) {
	t, e := a.store.MoveTask(r.Context(), r.PathValue("id"), r.FormValue("target_column_id"), "web")
	if e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		a.render(r, w, "task-card.html", t)
		return
	}
	http.Redirect(w, r, "/boards/"+t.BoardID, 303)
}
func (a *App) updateTask(w http.ResponseWriter, r *http.Request) {
	if err := a.store.UpdateTask(r.Context(), r.PathValue("id"), r.FormValue("title"), r.FormValue("description"), defaultString(r.FormValue("priority"), "normal"), r.FormValue("start_date"), r.FormValue("due_date")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.store.SetLabels(r.Context(), r.PathValue("id"), r.Form["label_ids"]); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/tasks/"+r.PathValue("id"), http.StatusSeeOther)
}
func (a *App) deleteTask(w http.ResponseWriter, r *http.Request) {
	task, err := a.store.GetTask(r.Context(), r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err = a.store.DeleteTask(r.Context(), task.ID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/boards/"+task.BoardID, http.StatusSeeOther)
}
func defaultString(x, d string) string {
	if x == "" {
		return d
	}
	return x
}
func _unused() { fmt.Print("") }
