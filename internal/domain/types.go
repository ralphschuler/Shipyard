package domain

import (
	"encoding/json"
	"time"
)

type User struct {
	ID, Email, DisplayName, PasswordHash, Role string
	Active                                     bool
	CreatedAt                                  time.Time
	LastLoginAt                                *time.Time
}
type UserPreferences struct {
	Theme         string
	ShortcutHints bool
	Language      string
}
type Session struct {
	ID, UserID, CSRFToken string
	ExpiresAt             time.Time
}
type APIToken struct {
	ID, UserID, Name, Prefix string
	ExpiresAt, LastUsedAt    *time.Time
	CreatedAt                time.Time
}
type IntegrationConnection struct {
	ID, UserID, Provider, Label, BaseURL, AccountLogin, Status, LastError string
	LastSyncedAt                                                          *time.Time
	CreatedAt, UpdatedAt                                                  time.Time
}

type Board struct {
	ID, Name  string
	CreatedAt time.Time
}

// BoardTemplate is a selectable workflow recipe. It is deliberately data-only
// so the catalogue can be rendered as cards and later enriched with imagery.
type BoardTemplate struct {
	ID, Name, Summary, Detail string
	Columns                   []string
}
type Project struct {
	ID, Name, RepositoryURL, DefaultBranch, LocalPath, LastSyncError string
	LastSyncedAt                                                     *time.Time
	CreatedAt, UpdatedAt                                             time.Time
	Boards                                                           []Board
}
type ProjectGroup struct {
	ID, Name, Description, Color string
	CreatedAt, UpdatedAt         time.Time
	Projects                     []Project
}
type Column struct {
	ID, BoardID, Name, Type    string
	Position, CanvasX, CanvasY int
	IsInitial, IsTerminal      bool
}
type Transition struct{ ID, BoardID, FromColumnID, ToColumnID, ActionName string }
type Task struct {
	ID, BoardID, ColumnID, ColumnName, Title, Description, Priority string
	StartDate, DueDate                                              *time.Time
	CompletedAt                                                     *time.Time
	CreatedAt                                                       time.Time
	IsTerminal                                                      bool
	Labels                                                          []Label
	TargetProjects                                                  []Project
}
type Label struct{ ID, Name, Color string }
type History struct {
	FromName, ToName, Source string
	OccurredAt               time.Time
}
type Comment struct {
	ID, TaskID, Author, Body string
	CreatedAt                time.Time
}
type AgentInteraction struct {
	ID, TaskID, AgentID, AgentRunID, Title, Body, Status, AnsweredBy string
	Schema, Response                                                 []byte
	CreatedAt, AnsweredAt                                            *time.Time
	DecisionKey, Fingerprint, ContinuationRunID                      string
}
type TaskDecision struct {
	ID, TaskID, AgentID, InteractionID, Key, Title, FreeformAnswer, ResolvedBy, ReopenReason string
	Response                                                                                 []byte
	ResolvedAt                                                                               time.Time
}
type Metric struct {
	Name  string
	Count int
}
type Dashboard struct {
	Total, Active, Completed       int
	ByColumn, ByPriority           []Metric
	CreatedSeries, CompletedSeries []Metric
	Runs                           RunMetrics
	EstimatedCostMicrousd          int64
	KnownActualCostMicrousd        int64
	EstimatedCostMicrousdV2        int64
	IncludedOrUnknownRuns          int
	IncludedOrUnknownTokens        int64
	UsageByDimension               []UsageMetric
	CostByAgent                    []CostMetric
	TelemetrySeries                []TelemetryPoint
	UsageTokenBreakdown            TokenBreakdown
	Notifications                  []Notification
}

// TokenBreakdown keeps aggregate classes nullable: NULL means no selected run
// reported that class and must not be rendered as an artificial zero.
type TokenBreakdown struct {
	InputTokens, OutputTokens, CachedInputTokens   *int64
	CacheWriteTokens, ReasoningTokens, TotalTokens *int64
}

// DashboardAttention keeps actionable work separate from historic metrics so
// a previous failed run never looks like an immediate incident.
type DashboardAttention struct {
	BlockedTasks, FailedRuns7d, OpenInteractions, DueNext24h int
}
type RunMetrics struct{ Queued, ResourceWaiting, Running, Succeeded, Failed int }
type CostMetric struct {
	Name           string
	AmountMicrousd int64
}
type UsageMetric struct {
	Dimension, Name   string
	Tokens            int64
	ActualMicrousd    int64
	EstimatedMicrousd int64
}
type TelemetryPoint struct {
	Day                                       string
	ActualMicrousd, EstimatedMicrousd, Tokens int64
}
type Notification struct {
	ID, TaskID, AgentRunID, Kind, Message string
	CreatedAt                             time.Time
}
type SkillSource struct {
	ID, Name, RepositoryURL, Branch string
	Enabled                         bool
	CreatedAt                       time.Time
}
type InstalledSkill struct {
	ID, SkillID, Name, Description, RepositoryPath, InstallPath, CommitSHA, Status string
	InstalledAt                                                                    time.Time
}
type Skill struct{ ID, SourceID, Name, Description, RepositoryPath string }

// CatalogSkill is a transient skills.sh discovery result. It deliberately is
// not stored as a local catalog row: the public catalog is too large and its
// ranking changes frequently. Only a skill the operator installs becomes a
// durable Shipyard skill.
type CatalogSkill struct {
	Source, Slug, Name, URL string
	Installs                int
}
type Agent struct {
	ID, Name, Description, Adapter, Prompt, PromptPrefix, PromptSuffix, WorkspacePath string
	Enabled                                                                           bool
	MaxParallelRuns                                                                   int
	CreatedAt                                                                         time.Time
}
type AutomationRule struct {
	ID, Name, BoardID, TriggerType, TargetColumnID, LabelID, AgentID, SuccessColumnID, FailureColumnID string
	Enabled                                                                                            bool
	RequireDeliveryApproval                                                                            bool
	ScheduleEveryMinutes, DueWithinHours, CooldownMinutes                                              int
	CreatedAt                                                                                          time.Time
}
type AgentRun struct {
	ID, TaskID, AgentID, RuleID, BatchID, Status, PromptSnapshot, WorkspaceSnapshot, TargetProject, Summary, ErrorMessage string
	StartedAt, FinishedAt                                                                                                 *time.Time
	CreatedAt                                                                                                             time.Time
}
type RunQueueStatus struct {
	Position      int
	WaitingReason string
	BlockingRunID string
	BlockingAgent string
	Workspace     string
	WaitingSince  time.Time
	NextAttemptAt time.Time
}

// WorktreeCleanupCandidate identifies an isolated checkout that no longer
// carries a deliverable patch. The source repository itself is never a
// cleanup target.
type WorktreeCleanupCandidate struct {
	RunID, SourceWorkspace, WorktreePath string
}
type RunOverview struct {
	ID, TaskID, TaskTitle, AgentID, AgentName, Status, Summary, ErrorMessage string
	QueuePosition                                                            int
	QueueReason, BlockingRunID, QueueBlockingAgent, QueueWorkspace           string
	QueueWaitingSince, QueueNextAttemptAt                                    *time.Time
	StartedAt, FinishedAt                                                    *time.Time
	CreatedAt                                                                time.Time
	DurationSeconds                                                          int
}
type AuditEvent struct {
	ID, Kind, ResourceType, ResourceID, Actor, TokenName string
	Metadata                                             string
	CreatedAt                                            time.Time
}
type RepositoryTarget struct {
	ID, TaskID, ProjectID, ProjectName, RepositoryURL, DefaultBranch, LocalPath string
	TargetSource                                                                string
	SourceGroups                                                                []byte
	CreatedAt                                                                   time.Time
}
type AgentRunBatch struct {
	ID, TaskID, AgentID, RuleID, EventID, Status                    string
	TotalTargets, SucceededTargets, FailedTargets, CancelledTargets int
	CreatedAt, UpdatedAt                                            time.Time
}
type RunDelivery struct {
	DiffSummary, GateStatus, GateOutput                    string
	AcceptedCommitSHA                                      string
	InputTokens, OutputTokens, TokenUsage, DurationSeconds int
	EstimatedCostMicrousd                                  int64
	AppliedAt                                              *time.Time
}
type UsageReport struct {
	Provider, Model, ServiceTier, Status, CostSource, PriceVersion string
	APICalls, InputTokens, OutputTokens, CachedInputTokens         *int64
	CacheWriteTokens, ReasoningTokens, TotalTokens                 *int64
	NativeCostMicrousd, CalculatedCostMicrousd                     *int64
	RawUsage                                                       []byte
	CostCalculatedAt                                               *time.Time
}
type UsagePrice struct {
	ID, Provider, Model, ServiceTier, Version         string
	ValidFrom                                         time.Time
	ValidUntil                                        *time.Time
	Input, Output, CachedInput, CacheWrite, Reasoning *int64
	CreatedAt                                         time.Time
}
type Webhook struct {
	ID, Name, URL, Events             string
	Enabled                           bool
	CreatedAt                         time.Time
	PendingDeliveries, DeadDeliveries int
}
type WebhookDelivery struct {
	ID, WebhookID, URL, EventName, Payload, Status, LastError string
	AttemptCount                                              int
}
type Schedule struct {
	ID, Name, BoardName, AgentName string
	EveryMinutes, DueWithinHours   int
	Enabled                        bool
}
type ProviderSetting struct {
	ID, Provider                                string
	Enabled                                     bool
	Model, Command, SecretEnv, BaseURL, Options string
	UpdatedAt                                   time.Time
}
type Secret struct {
	ID, Name, Description, EnvName string
	Revoked                        bool
	AgentIDs                       []string
	CreatedAt, UpdatedAt           time.Time
}
type SecretValue struct{ ID, EnvName, Value string }
type RunLog struct {
	ID, RunID      string
	Sequence       int
	Level, Message string
	CreatedAt      time.Time
}
type RunTraceItem struct {
	At     time.Time
	Kind   string
	Detail string
}
type RunTrace struct {
	RunID, TaskID, RuleID, BatchID, EventID, Status string
	Items                                           []RunTraceItem
}
type AutomationEvent struct {
	ID, Type, TaskID, BoardID string
	Payload                   json.RawMessage
	OccurredAt                time.Time
}
type AutomationPreviewTask struct {
	ID, Title, ColumnName string
}
type WorkflowRun struct {
	ID, BoardID, RootTaskID, Name, Status, IdempotencyKey string
	CreatedAt, UpdatedAt                                  time.Time
}
type WorkflowStep struct {
	ID, WorkflowRunID, StepKey, TaskID, AgentID, Status, PromptSnapshot, OutputSummary, ErrorMessage string
	DependsOn                                                                                        []string
	MaxAttempts, Attempts                                                                            int
	StartedAt, FinishedAt                                                                            *time.Time
	CreatedAt                                                                                        time.Time
}
type WorkflowApproval struct {
	ID, WorkflowStepID, Status, Note string
	RequestedAt                      time.Time
	DecidedAt                        *time.Time
}
