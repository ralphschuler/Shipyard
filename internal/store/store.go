package store

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"path/filepath"
	"regexp"
	"strings"
	"taskboard/internal/domain"
	"taskboard/internal/sandbox"
	workspacecfg "taskboard/internal/workspace"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct {
	DB  *pgxpool.Pool
	DSN string
}

var ErrNoRunCreated = errors.New("agent run already exists for this event")
var ErrWorkspaceBusy = errors.New("workspace is busy")
var ErrTargetSelectionRequired = errors.New("an explicit repository target selection is required")

var canonicalUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

func acquireWorkspaceRunGate() (*workspacecfg.RunGate, error) {
	gate, err := workspacecfg.AcquireRunGate()
	if err != nil {
		return nil, err
	}
	if _, err := workspacecfg.Validate(); err != nil {
		_ = gate.Close()
		return nil, fmt.Errorf("Workspace-Preflight blockiert neue Runs: %w", err)
	}
	return gate, nil
}

// Keep every positional AutomationRule query in one canonical order. pgx's
// RowToStructByPos deliberately rejects a partial row; centralising this list
// prevents a newly added rule field from silently disabling the worker.
var automationRuleColumns = []string{
	"id", "name", "COALESCE(board_id::text,'')", "trigger_type",
	"COALESCE(target_column_id::text,'')", "COALESCE(label_id::text,'')",
	"COALESCE(agent_id::text,'')", "COALESCE(success_column_id::text,'')",
	"COALESCE(failure_column_id::text,'')", "enabled", "require_delivery_approval",
	"COALESCE(schedule_every_minutes,0)", "COALESCE(due_within_hours,0)",
	"cooldown_minutes", "created_at",
}

var automationRuleSelect = strings.Join(automationRuleColumns, ",")

// ErrAutomationActive means that this rule already owns work for the task.
// It is intentionally not an automation failure: a duplicate event can be
// marked processed without creating a second coding run.
var ErrAutomationActive = errors.New("automation already has an active run for this task")
var ErrTaskAgentActive = errors.New("agent already has an active run for this task")

// AutomationEventFingerprint is the durable semantic identity used by the
// automation claim table. All payload fields are relevant unless they are
// transport metadata (event_id, delivery_id, transport_id, occurred_at, or
// received_at). JSON object key order is insignificant, while array order is
// preserved: arrays can represent ordered transitions or requested targets.
func AutomationEventFingerprint(event domain.AutomationEvent, rule domain.AutomationRule) (string, error) {
	var payload any
	if len(event.Payload) == 0 {
		payload = map[string]any{}
	} else if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return "", fmt.Errorf("automation payload is invalid JSON: %w", err)
	}
	canonical := canonicalAutomationPayload(payload)
	object := map[string]any{
		"version": 1, "task_id": event.TaskID, "rule_id": rule.ID,
		"trigger_type": event.Type, "target_column_id": rule.TargetColumnID,
		"return_generation": automationReturnGeneration(canonical), "payload": canonical,
	}
	raw, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:]), nil
}

func canonicalAutomationPayload(value any) any {
	switch v := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, child := range v {
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "event_id", "delivery_id", "transport_id", "occurred_at", "received_at":
				continue
			}
			result[key] = canonicalAutomationPayload(child)
		}
		return result
	case []any:
		items := make([]any, len(v))
		for i, child := range v {
			items[i] = canonicalAutomationPayload(child)
		}
		return items
	default:
		return value
	}
}

func automationReturnGeneration(payload any) string {
	object, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"return_generation", "qa_return_generation", "rollback_generation", "generation"} {
		if value, ok := object[key]; ok {
			return fmt.Sprint(value)
		}
	}
	return ""
}

func Open(ctx context.Context, url string) (*Store, error) {
	p, e := pgxpool.New(ctx, url)
	if e != nil {
		return nil, e
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, e
	}
	return &Store{DB: p, DSN: url}, nil
}
func (s *Store) Ping(ctx context.Context) error { return s.DB.Ping(ctx) }
func (s *Store) HasUsers(c context.Context) (bool, error) {
	var ok bool
	err := s.DB.QueryRow(c, `SELECT EXISTS(SELECT 1 FROM users)`).Scan(&ok)
	return ok, err
}
func (s *Store) CreateOwner(c context.Context, email, name, passwordHash string) (domain.User, error) {
	tx, err := s.DB.Begin(c)
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback(c)
	var u domain.User
	err = tx.QueryRow(c, `INSERT INTO users(email,display_name,password_hash,role) SELECT $1,$2,$3,'owner' WHERE NOT EXISTS(SELECT 1 FROM users) RETURNING id,email,display_name,password_hash,role,active,created_at,last_login_at`, strings.ToLower(strings.TrimSpace(email)), strings.TrimSpace(name), passwordHash).Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.Role, &u.Active, &u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return u, err
	}
	if _, err = tx.Exec(c, `INSERT INTO workspace_members(workspace_id,user_id,role) SELECT id,$1,'owner' FROM workspaces LIMIT 1`, u.ID); err != nil {
		return u, err
	}
	if _, err = tx.Exec(c, `INSERT INTO audit_events(user_id,kind,resource_type,resource_id) VALUES($1::uuid,'owner.setup','user',$1::text)`, u.ID); err != nil {
		return u, err
	}
	return u, tx.Commit(c)
}
func (s *Store) UserByEmail(c context.Context, email string) (domain.User, error) {
	var u domain.User
	err := s.DB.QueryRow(c, `SELECT id,email,display_name,password_hash,role,active,created_at,last_login_at FROM users WHERE lower(email)=lower($1)`, strings.TrimSpace(email)).Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.Role, &u.Active, &u.CreatedAt, &u.LastLoginAt)
	return u, err
}
func (s *Store) UserBySession(c context.Context, tokenHash string) (domain.User, domain.Session, error) {
	var u domain.User
	var x domain.Session
	err := s.DB.QueryRow(c, `SELECT u.id,u.email,u.display_name,u.password_hash,u.role,u.active,u.created_at,u.last_login_at,s.id,s.user_id,s.csrf_token,s.expires_at FROM user_sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>now() AND u.active`, tokenHash).Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.Role, &u.Active, &u.CreatedAt, &u.LastLoginAt, &x.ID, &x.UserID, &x.CSRFToken, &x.ExpiresAt)
	return u, x, err
}
func (s *Store) CreateSession(c context.Context, userID, tokenHash, csrf string, expiry time.Time) error {
	// Session cookies are intentionally long-lived for the local SSO bridge.
	// Bound their durable representation nevertheless: otherwise short-lived
	// browser profiles, health probes and verification runs can grow this table
	// forever. Keep a useful multi-device window while removing only expired or
	// oldest sessions for the same user.
	tx, err := s.DB.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if _, err = tx.Exec(c, `DELETE FROM user_sessions WHERE expires_at <= now()`); err != nil {
		return err
	}
	if _, err = tx.Exec(c, `DELETE FROM user_sessions
		WHERE id IN (
			SELECT id FROM user_sessions
			WHERE user_id=$1
			ORDER BY created_at DESC, id DESC
			OFFSET 15
		)`, userID); err != nil {
		return err
	}
	if _, err = tx.Exec(c, `INSERT INTO user_sessions(user_id,token_hash,csrf_token,expires_at) VALUES($1,$2,$3,$4)`, userID, tokenHash, csrf, expiry); err != nil {
		return err
	}
	return tx.Commit(c)
}
func (s *Store) DeleteSession(c context.Context, tokenHash string) error {
	_, err := s.DB.Exec(c, `DELETE FROM user_sessions WHERE token_hash=$1`, tokenHash)
	return err
}
func (s *Store) MarkLogin(c context.Context, id string) error {
	_, err := s.DB.Exec(c, `UPDATE users SET last_login_at=now() WHERE id=$1`, id)
	return err
}
func (s *Store) UserPreferences(c context.Context, userID string) (domain.UserPreferences, error) {
	var p domain.UserPreferences
	err := s.DB.QueryRow(c, `SELECT theme,shortcut_hints,language FROM workspace_preferences WHERE user_id=$1`, userID).Scan(&p.Theme, &p.ShortcutHints, &p.Language)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.UserPreferences{Theme: "system", ShortcutHints: true, Language: "de"}, nil
	}
	if p.Language != "de" && p.Language != "en" {
		p.Language = "de"
	}
	return p, err
}
func (s *Store) SaveUserPreferences(c context.Context, userID, theme string, hints bool, language string) error {
	if theme != "system" && theme != "light" && theme != "dark" {
		return errors.New("invalid theme")
	}
	if language != "de" && language != "en" {
		return errors.New("invalid language")
	}
	_, err := s.DB.Exec(c, `INSERT INTO workspace_preferences(user_id,theme,shortcut_hints,language,updated_at) VALUES($1,$2,$3,$4,now()) ON CONFLICT(user_id) DO UPDATE SET theme=EXCLUDED.theme,shortcut_hints=EXCLUDED.shortcut_hints,language=EXCLUDED.language,updated_at=now()`, userID, theme, hints, language)
	return err
}
func (s *Store) CreateAPIToken(c context.Context, userID, name, hash, prefix string, expiry *time.Time) (domain.APIToken, error) {
	var t domain.APIToken
	err := s.DB.QueryRow(c, `INSERT INTO api_tokens(user_id,name,token_hash,prefix,expires_at) VALUES($1,$2,$3,$4,$5) RETURNING id,user_id,name,prefix,expires_at,last_used_at,created_at`, userID, strings.TrimSpace(name), hash, prefix, expiry).Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &t.ExpiresAt, &t.LastUsedAt, &t.CreatedAt)
	return t, err
}
func (s *Store) APITokens(c context.Context, userID string) ([]domain.APIToken, error) {
	rows, err := s.DB.Query(c, `SELECT id,user_id,name,prefix,expires_at,last_used_at,created_at FROM api_tokens WHERE user_id=$1 AND revoked_at IS NULL ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.APIToken])
}
func (s *Store) RevokeAPIToken(ctx context.Context, id, userID string) error {
	_, err := s.DB.Exec(ctx, `UPDATE api_tokens SET revoked_at=now() WHERE id=$1 AND user_id=$2`, id, userID)
	return err
}
func (s *Store) UserForAPIToken(ctx context.Context, hash string) (domain.User, error) {
	var u domain.User
	err := s.DB.QueryRow(ctx, `UPDATE api_tokens t SET last_used_at=now() FROM users u WHERE t.user_id=u.id AND t.token_hash=$1 AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at>now()) AND u.active RETURNING u.id,u.email,u.display_name,u.password_hash,u.role,u.active,u.created_at,u.last_login_at`, hash).Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.Role, &u.Active, &u.CreatedAt, &u.LastLoginAt)
	return u, err
}

func (s *Store) UserAndAPITokenForHash(ctx context.Context, hash string) (domain.User, domain.APIToken, error) {
	var u domain.User
	var token domain.APIToken
	err := s.DB.QueryRow(ctx, `UPDATE api_tokens t SET last_used_at=now() FROM users u
		WHERE t.user_id=u.id AND t.token_hash=$1 AND t.revoked_at IS NULL
		AND (t.expires_at IS NULL OR t.expires_at>now()) AND u.active
		RETURNING u.id,u.email,u.display_name,u.password_hash,u.role,u.active,u.created_at,u.last_login_at,
		t.id,t.user_id,t.name,t.prefix,t.expires_at,t.last_used_at,t.created_at`, hash).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.Role, &u.Active, &u.CreatedAt, &u.LastLoginAt,
			&token.ID, &token.UserID, &token.Name, &token.Prefix, &token.ExpiresAt, &token.LastUsedAt, &token.CreatedAt)
	return u, token, err
}

func (s *Store) RecordAudit(ctx context.Context, userID, kind, resourceType, resourceID string, metadata map[string]string) error {
	return recordAudit(ctx, s.DB, userID, kind, resourceType, resourceID, metadata)
}

type auditExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func recordAudit(ctx context.Context, exec auditExecutor, userID, kind, resourceType, resourceID string, metadata map[string]string) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = exec.Exec(ctx, `INSERT INTO audit_events(user_id,kind,resource_type,resource_id,metadata) VALUES(NULLIF($1,'')::uuid,$2,$3,$4,$5)`, userID, kind, resourceType, resourceID, raw)
	return err
}

func (s *Store) UsagePrices(ctx context.Context) ([]domain.UsagePrice, error) {
	rows, err := s.DB.Query(ctx, `SELECT id::text,provider,model,service_tier,version,valid_from,valid_until,input_microusd_per_million,output_microusd_per_million,cached_input_microusd_per_million,cache_write_microusd_per_million,reasoning_microusd_per_million,created_at FROM usage_price_catalog ORDER BY provider,model,service_tier,valid_from DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.UsagePrice])
}

func (s *Store) SaveUsagePrice(ctx context.Context, p domain.UsagePrice) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO usage_price_catalog(provider,model,service_tier,valid_from,valid_until,input_microusd_per_million,output_microusd_per_million,cached_input_microusd_per_million,cache_write_microusd_per_million,reasoning_microusd_per_million,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, p.Provider, p.Model, p.ServiceTier, p.ValidFrom, p.ValidUntil, p.Input, p.Output, p.CachedInput, p.CacheWrite, p.Reasoning, p.Version)
	return err
}

// SaveUsagePriceWithAudit keeps the catalog mutation and its audit record in
// one transaction. A price must never become visible without its history.
func (s *Store) SaveUsagePriceWithAudit(ctx context.Context, p domain.UsagePrice, actor string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO usage_price_catalog(provider,model,service_tier,valid_from,valid_until,input_microusd_per_million,output_microusd_per_million,cached_input_microusd_per_million,cache_write_microusd_per_million,reasoning_microusd_per_million,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, p.Provider, p.Model, p.ServiceTier, p.ValidFrom, p.ValidUntil, p.Input, p.Output, p.CachedInput, p.CacheWrite, p.Reasoning, p.Version); err != nil {
		return err
	}
	if err = recordAudit(ctx, tx, actor, "usage_price.created", "usage_price", p.Version, map[string]string{"provider": p.Provider, "model": p.Model, "version": p.Version}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) UpdateUsagePrice(ctx context.Context, p domain.UsagePrice) error {
	_, err := s.DB.Exec(ctx, `UPDATE usage_price_catalog SET provider=$2,model=$3,service_tier=$4,valid_from=$5,valid_until=$6,input_microusd_per_million=$7,output_microusd_per_million=$8,cached_input_microusd_per_million=$9,cache_write_microusd_per_million=$10,reasoning_microusd_per_million=$11,version=$12 WHERE id=$1`, p.ID, p.Provider, p.Model, p.ServiceTier, p.ValidFrom, p.ValidUntil, p.Input, p.Output, p.CachedInput, p.CacheWrite, p.Reasoning, p.Version)
	return err
}

// UpdateUsagePriceWithAudit rolls back the catalog update if its audit insert
// or the final commit fails.
func (s *Store) UpdateUsagePriceWithAudit(ctx context.Context, p domain.UsagePrice, actor string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE usage_price_catalog SET provider=$2,model=$3,service_tier=$4,valid_from=$5,valid_until=$6,input_microusd_per_million=$7,output_microusd_per_million=$8,cached_input_microusd_per_million=$9,cache_write_microusd_per_million=$10,reasoning_microusd_per_million=$11,version=$12 WHERE id=$1`, p.ID, p.Provider, p.Model, p.ServiceTier, p.ValidFrom, p.ValidUntil, p.Input, p.Output, p.CachedInput, p.CacheWrite, p.Reasoning, p.Version)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	if err = recordAudit(ctx, tx, actor, "usage_price.updated", "usage_price", p.ID, map[string]string{"provider": p.Provider, "model": p.Model, "version": p.Version}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteUsagePrice(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM usage_price_catalog WHERE id=$1`, id)
	return err
}

// DeleteUsagePriceWithAudit makes deletion and its audit trail atomic.
func (s *Store) DeleteUsagePriceWithAudit(ctx context.Context, id, actor string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = recordAudit(ctx, tx, actor, "usage_price.deleted", "usage_price", id, nil); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `DELETE FROM usage_price_catalog WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return tx.Commit(ctx)
}

func (s *Store) ResolveUsagePrice(ctx context.Context, provider, model, tier string, at time.Time) (domain.UsagePrice, error) {
	var p domain.UsagePrice
	err := s.DB.QueryRow(ctx, `SELECT id::text,provider,model,service_tier,version,valid_from,valid_until,input_microusd_per_million,output_microusd_per_million,cached_input_microusd_per_million,cache_write_microusd_per_million,reasoning_microusd_per_million,created_at FROM usage_price_catalog WHERE provider=$1 AND model=$2 AND service_tier=$3 AND valid_from <= $4 AND (valid_until IS NULL OR valid_until > $4) ORDER BY valid_from DESC LIMIT 1`, provider, model, tier, at).Scan(&p.ID, &p.Provider, &p.Model, &p.ServiceTier, &p.Version, &p.ValidFrom, &p.ValidUntil, &p.Input, &p.Output, &p.CachedInput, &p.CacheWrite, &p.Reasoning, &p.CreatedAt)
	return p, err
}

func (s *Store) AuditEvents(ctx context.Context, limit int) ([]domain.AuditEvent, error) {
	return s.AuditEventsBefore(ctx, limit, nil, "")
}

// AuditEventsBefore uses a stable keyset cursor rather than an offset. New
// audit records therefore cannot make an operator skip or duplicate an older
// record while paging through a busy system.
func (s *Store) AuditEventsBefore(ctx context.Context, limit int, before *time.Time, beforeID string) ([]domain.AuditEvent, error) {
	if limit < 1 || limit > 500 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx, `SELECT e.id,e.kind,e.resource_type,e.resource_id,
		COALESCE(u.display_name,u.email,'System'),COALESCE(e.metadata->>'token_name',''),e.metadata::text,e.created_at
		FROM audit_events e LEFT JOIN users u ON u.id=e.user_id
		WHERE ($2::timestamptz IS NULL OR e.created_at<$2 OR (e.created_at=$2 AND e.id::text<$3))
		ORDER BY e.created_at DESC,e.id DESC LIMIT $1`, limit, before, beforeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.AuditEvent])
}

func (s *Store) AllRuns(ctx context.Context, limit int) ([]domain.RunOverview, error) {
	return s.AllRunsBefore(ctx, limit, nil, "")
}

// AllRunsBefore keeps the runs page responsive as history grows. The cursor
// is based on the immutable creation timestamp and ID, so concurrent runs do
// not shift an operator's page while they investigate an incident.
func (s *Store) AllRunsBefore(ctx context.Context, limit int, before *time.Time, beforeID string) ([]domain.RunOverview, error) {
	if limit < 1 || limit > 500 {
		limit = 50
	}
	if err := s.RefreshQueueState(ctx); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT r.id,r.task_id,t.title,r.agent_id,a.name,r.status,r.summary,r.error_message,
		CASE WHEN r.status='queued' THEN 1+(SELECT count(*) FROM agent_runs prior WHERE prior.workspace_snapshot=r.workspace_snapshot AND prior.status='queued' AND (prior.created_at<r.created_at OR (prior.created_at=r.created_at AND prior.id<r.id))) ELSE 0 END,
		CASE WHEN r.status='queued' THEN r.queue_wait_reason ELSE '' END,
		COALESCE((SELECT active.id::text FROM agent_runs active WHERE active.workspace_snapshot=r.workspace_snapshot AND active.status='running' ORDER BY active.started_at,active.created_at,active.id LIMIT 1),''),
		COALESCE((SELECT activeAgent.name FROM agents activeAgent JOIN agent_runs active ON active.agent_id=activeAgent.id WHERE active.workspace_snapshot=r.workspace_snapshot AND active.status='running' ORDER BY active.started_at,active.created_at,active.id LIMIT 1),''),
		CASE WHEN r.status='queued' THEN r.workspace_snapshot ELSE '' END,
		CASE WHEN r.status='queued' AND r.queue_wait_reason <> '' THEN r.queue_wait_started_at ELSE NULL END,
		CASE WHEN r.status='queued' THEN r.queue_next_attempt_at ELSE NULL END,
		r.started_at,r.finished_at,r.created_at,r.duration_seconds
		FROM agent_runs r JOIN tasks t ON t.id=r.task_id JOIN agents a ON a.id=r.agent_id
		WHERE ($2::timestamptz IS NULL OR r.created_at<$2 OR (r.created_at=$2 AND r.id::text<$3))
		ORDER BY r.created_at DESC,r.id DESC LIMIT $1`, limit, before, beforeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.RunOverview])
}
func (s *Store) IntegrationConnections(c context.Context, userID string) ([]domain.IntegrationConnection, error) {
	rows, err := s.DB.Query(c, `SELECT id,user_id,provider,label,base_url,account_login,status,last_error,last_synced_at,created_at,updated_at FROM integration_connections WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByNameLax[domain.IntegrationConnection])
}
func (s *Store) CreateIntegrationConnection(c context.Context, userID, provider, label, baseURL string) (domain.IntegrationConnection, error) {
	var connection domain.IntegrationConnection
	err := s.DB.QueryRow(c, `INSERT INTO integration_connections(user_id,provider,label,base_url) VALUES($1,$2,$3,$4) RETURNING id,user_id,provider,label,base_url,account_login,status,last_error,last_synced_at,created_at,updated_at`, userID, provider, strings.TrimSpace(label), strings.TrimSpace(baseURL)).Scan(&connection.ID, &connection.UserID, &connection.Provider, &connection.Label, &connection.BaseURL, &connection.AccountLogin, &connection.Status, &connection.LastError, &connection.LastSyncedAt, &connection.CreatedAt, &connection.UpdatedAt)
	return connection, err
}
func (s *Store) DeleteIntegrationConnection(c context.Context, id, userID string) error {
	_, err := s.DB.Exec(c, `DELETE FROM integration_connections WHERE id=$1 AND user_id=$2`, id, userID)
	return err
}
func (s *Store) CreateWorkflowRun(c context.Context, boardID, taskID, name, key string) (domain.WorkflowRun, error) {
	var r domain.WorkflowRun
	err := s.DB.QueryRow(c, `INSERT INTO workflow_runs(board_id,root_task_id,name,idempotency_key) VALUES(NULLIF($1,'')::uuid,NULLIF($2,'')::uuid,$3,$4) RETURNING id,COALESCE(board_id::text,''),COALESCE(root_task_id::text,''),name,status,idempotency_key,created_at,updated_at`, boardID, taskID, name, key).Scan(&r.ID, &r.BoardID, &r.RootTaskID, &r.Name, &r.Status, &r.IdempotencyKey, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}
func (s *Store) AddWorkflowStep(c context.Context, runID, key, taskID, agentID, prompt string, deps []string, max int) (domain.WorkflowStep, error) {
	if max < 1 {
		max = 1
	}
	raw, _ := json.Marshal(deps)
	var step domain.WorkflowStep
	err := s.DB.QueryRow(c, `INSERT INTO workflow_steps(workflow_run_id,step_key,task_id,agent_id,prompt_snapshot,depends_on,max_attempts) VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,$6,$7) RETURNING id,workflow_run_id::text,step_key,COALESCE(task_id::text,''),COALESCE(agent_id::text,''),status,prompt_snapshot,output_summary,error_message,depends_on,max_attempts,attempts,started_at,finished_at,created_at`, runID, key, taskID, agentID, prompt, raw, max).Scan(&step.ID, &step.WorkflowRunID, &step.StepKey, &step.TaskID, &step.AgentID, &step.Status, &step.PromptSnapshot, &step.OutputSummary, &step.ErrorMessage, &step.DependsOn, &step.MaxAttempts, &step.Attempts, &step.StartedAt, &step.FinishedAt, &step.CreatedAt)
	return step, err
}

// ListenChanges forwards PostgreSQL NOTIFY payloads to the caller. It owns a
// dedicated connection so normal request traffic never blocks on LISTEN.
func (s *Store) ListenChanges(ctx context.Context, publish func(string)) {
	for ctx.Err() == nil {
		conn, err := pgx.Connect(ctx, s.DSN)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			}
		}
		if _, err = conn.Exec(ctx, "LISTEN taskboard_events"); err != nil {
			conn.Close(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			}
		}
		for ctx.Err() == nil {
			notification, waitErr := conn.WaitForNotification(ctx)
			if waitErr != nil {
				break
			}
			publish(notification.Payload)
		}
		conn.Close(ctx)
	}
}
func (s *Store) Migrate(ctx context.Context) error {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	if _, err = s.DB.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())"); err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Package-level Go tests run concurrently. Serialize migrations so two
	// fresh test connections cannot both observe a missing version and execute
	// CREATE EXTENSION/DDL at the same time.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('taskboard schema migrations'))"); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var applied bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)", entry.Name()).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		body, readErr := migrationFiles.ReadFile("migrations/" + entry.Name())
		if readErr != nil {
			return readErr
		}
		if _, err = tx.Exec(ctx, string(body)); err == nil {
			_, err = tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES($1)", entry.Name())
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) ListBoards(c context.Context) ([]domain.Board, error) {
	rows, e := s.DB.Query(c, "SELECT id,name,created_at FROM boards ORDER BY created_at")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.Board])
}
func (s *Store) Projects(c context.Context) ([]domain.Project, error) {
	rows, err := s.DB.Query(c, `SELECT id,name,repository_url,default_branch,local_path,last_synced_at,last_sync_error,created_at,updated_at FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[domain.Project])
	if err != nil {
		return nil, err
	}
	for i := range projects {
		boards, boardErr := s.ProjectBoards(c, projects[i].ID)
		if boardErr != nil {
			return nil, boardErr
		}
		projects[i].Boards = boards
	}
	return projects, nil
}
func (s *Store) ProjectGroups(c context.Context) ([]domain.ProjectGroup, error) {
	rows, err := s.DB.Query(c, `SELECT id,name,description,color,created_at,updated_at FROM project_groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[domain.ProjectGroup])
	if err != nil {
		return nil, err
	}
	for i := range groups {
		projects, getErr := s.GroupProjects(c, groups[i].ID)
		if getErr != nil {
			return nil, getErr
		}
		groups[i].Projects = projects
	}
	return groups, nil
}
func (s *Store) GroupProjects(c context.Context, groupID string) ([]domain.Project, error) {
	rows, err := s.DB.Query(c, `SELECT p.id,p.name,p.repository_url,p.default_branch,p.local_path,p.last_synced_at,p.last_sync_error,p.created_at,p.updated_at FROM projects p JOIN project_group_members m ON m.project_id=p.id WHERE m.group_id=$1 ORDER BY p.name`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByNameLax[domain.Project])
}
func (s *Store) CreateProjectGroup(c context.Context, name, description, color string, projectIDs []string) (domain.ProjectGroup, error) {
	if color == "" {
		color = "#3158d4"
	}
	tx, err := s.DB.Begin(c)
	if err != nil {
		return domain.ProjectGroup{}, err
	}
	defer tx.Rollback(c)
	var group domain.ProjectGroup
	err = tx.QueryRow(c, `INSERT INTO project_groups(name,description,color) VALUES($1,$2,$3) RETURNING id,name,description,color,created_at,updated_at`, strings.TrimSpace(name), strings.TrimSpace(description), color).Scan(&group.ID, &group.Name, &group.Description, &group.Color, &group.CreatedAt, &group.UpdatedAt)
	if err != nil {
		return group, err
	}
	for _, id := range projectIDs {
		if _, err = tx.Exec(c, `INSERT INTO project_group_members(group_id,project_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, group.ID, id); err != nil {
			return group, err
		}
	}
	return group, tx.Commit(c)
}
func (s *Store) UpdateProjectGroup(c context.Context, id, name, description, color string, projectIDs []string) error {
	tx, err := s.DB.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	// Groups are derived catalogue metadata: without members there is no group
	// to preserve. Existing task targets are repository snapshots and therefore
	// remain intact when their former source group is removed.
	if len(projectIDs) == 0 {
		if _, err = tx.Exec(c, `DELETE FROM project_groups WHERE id=$1`, id); err != nil {
			return err
		}
		return tx.Commit(c)
	}
	if _, err = tx.Exec(c, `UPDATE project_groups SET name=$2,description=$3,color=$4,updated_at=now() WHERE id=$1`, id, strings.TrimSpace(name), strings.TrimSpace(description), color); err != nil {
		return err
	}
	if _, err = tx.Exec(c, `DELETE FROM project_group_members WHERE group_id=$1`, id); err != nil {
		return err
	}
	for _, projectID := range projectIDs {
		if _, err = tx.Exec(c, `INSERT INTO project_group_members(group_id,project_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, projectID); err != nil {
			return err
		}
	}
	return tx.Commit(c)
}
func (s *Store) DeleteProjectGroup(c context.Context, id string) error {
	_, err := s.DB.Exec(c, `DELETE FROM project_groups WHERE id=$1`, id)
	return err
}

// SetProjectGroups changes only the project catalogue. Task repository targets
// are snapshots, so this cannot silently broaden or shrink an existing task.
func (s *Store) SetProjectGroups(c context.Context, projectID string, groupIDs []string) error {
	tx, err := s.DB.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if _, err = tx.Exec(c, `DELETE FROM project_group_members WHERE project_id=$1`, projectID); err != nil {
		return err
	}
	for _, groupID := range groupIDs {
		if _, err = tx.Exec(c, `INSERT INTO project_group_members(group_id,project_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, groupID, projectID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(c, `DELETE FROM project_groups g WHERE NOT EXISTS (SELECT 1 FROM project_group_members m WHERE m.group_id=g.id)`); err != nil {
		return err
	}
	return tx.Commit(c)
}
func (s *Store) EnsureProjectGroup(c context.Context, name, color string) (domain.ProjectGroup, error) {
	var group domain.ProjectGroup
	err := s.DB.QueryRow(c, `INSERT INTO project_groups(name,description,color) VALUES($1,'',$2)
		ON CONFLICT ((lower(name))) DO UPDATE SET name=EXCLUDED.name
		RETURNING id,name,description,color,created_at,updated_at`, strings.TrimSpace(name), color).
		Scan(&group.ID, &group.Name, &group.Description, &group.Color, &group.CreatedAt, &group.UpdatedAt)
	return group, err
}
func (s *Store) SetTaskTargets(c context.Context, taskID string, projectIDs, groupIDs []string) error {
	var err error
	if projectIDs, err = normalizeTargetIDs("project", projectIDs); err != nil {
		return err
	}
	if groupIDs, err = normalizeTargetIDs("group", groupIDs); err != nil {
		return err
	}
	tx, err := s.DB.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if _, err = tx.Exec(c, `DELETE FROM task_target_projects WHERE task_id=$1`, taskID); err != nil {
		return err
	}
	if _, err = tx.Exec(c, `DELETE FROM task_target_groups WHERE task_id=$1`, taskID); err != nil {
		return err
	}
	if _, err = tx.Exec(c, `DELETE FROM task_repository_targets WHERE task_id=$1`, taskID); err != nil {
		return err
	}
	targetSource := "explicit"
	if len(projectIDs) == 0 && len(groupIDs) == 0 {
		targetSource = "inherited"
	}
	for _, id := range projectIDs {
		if _, err = tx.Exec(c, `INSERT INTO task_target_projects(task_id,project_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, taskID, id); err != nil {
			return err
		}
	}
	for _, id := range groupIDs {
		if _, err = tx.Exec(c, `INSERT INTO task_target_groups(task_id,group_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, taskID, id); err != nil {
			return err
		}
	}
	// Resolve direct projects and group membership now. Agent runs deliberately
	// read this immutable snapshot, not the mutable group membership.
	if _, err = tx.Exec(c, `INSERT INTO task_repository_targets(task_id,project_id,project_name,repository_url,default_branch,local_path,source_groups,target_source)
		SELECT $1,p.id,p.name,p.repository_url,p.default_branch,p.local_path,'[]'::jsonb,$2
		FROM projects p JOIN task_target_projects t ON t.project_id=p.id WHERE t.task_id=$1
		ON CONFLICT (task_id,project_id) DO NOTHING`, taskID, targetSource); err != nil {
		return err
	}
	if _, err = tx.Exec(c, `INSERT INTO task_repository_targets(task_id,project_id,project_name,repository_url,default_branch,local_path,source_groups,target_source)
		SELECT $1,p.id,p.name,p.repository_url,p.default_branch,p.local_path,
		jsonb_agg(jsonb_build_object('id',g.id,'name',g.name,'color',g.color)),'explicit'
		FROM task_target_groups t JOIN project_groups g ON g.id=t.group_id
		JOIN project_group_members m ON m.group_id=g.id JOIN projects p ON p.id=m.project_id
		WHERE t.task_id=$1 GROUP BY p.id,p.name,p.repository_url,p.default_branch,p.local_path
		ON CONFLICT (task_id,project_id) DO UPDATE SET source_groups=EXCLUDED.source_groups`, taskID); err != nil {
		return err
	}
	if targetSource == "inherited" {
		if _, err = tx.Exec(c, `INSERT INTO task_repository_targets(task_id,project_id,project_name,repository_url,default_branch,local_path,source_groups,target_source)
			SELECT t.id,p.id,p.name,p.repository_url,p.default_branch,p.local_path,'[]'::jsonb,'inherited'
			FROM tasks t JOIN board_projects bp ON bp.board_id=t.board_id JOIN projects p ON p.id=bp.project_id
			WHERE t.id=$1 AND (SELECT count(*) FROM board_projects WHERE board_id=t.board_id)=1`, taskID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(c, `UPDATE agent_interactions SET status='cancelled',answered_at=now()
		WHERE task_id=$1 AND status='open' AND (decision_key ILIKE '%project%' OR decision_key ILIKE '%repository%' OR title ILIKE '%project%' OR title ILIKE '%repository%')
		AND EXISTS (SELECT 1 FROM task_repository_targets WHERE task_id=$1)`, taskID); err != nil {
		return err
	}
	return tx.Commit(c)
}

func requireRunTargets(targets []domain.RepositoryTarget) error {
	if len(targets) == 0 {
		return ErrTargetSelectionRequired
	}
	return nil
}

// runTargets resolves the durable snapshot first and permits board inheritance
// only when the board has exactly one project. Ambiguous boards are surfaced as
// a selection error instead of falling back to an agent workspace.
func (s *Store) runTargets(c context.Context, taskID string) ([]domain.RepositoryTarget, error) {
	targets, err := s.EffectiveTaskRepositoryTargets(c, taskID)
	if err != nil {
		return nil, err
	}
	if len(targets) > 0 {
		return targets, nil
	}
	task, err := s.GetTask(c, taskID)
	if err != nil {
		return nil, err
	}
	projects, err := s.BoardProjects(c, task.BoardID)
	if err != nil {
		return nil, err
	}
	if len(projects) != 1 {
		if len(projects) == 0 {
			return nil, fmt.Errorf("%w: board has no linked repository project", ErrTargetSelectionRequired)
		}
		choices := make([]string, 0, len(projects))
		for _, project := range projects {
			choices = append(choices, project.Name+" ("+project.RepositoryURL+")")
		}
		return nil, fmt.Errorf("%w: choose one repository target: %s", ErrTargetSelectionRequired, strings.Join(choices, ", "))
	}
	project := projects[0]
	return []domain.RepositoryTarget{{TaskID: taskID, ProjectID: project.ID, ProjectName: project.Name, RepositoryURL: project.RepositoryURL, DefaultBranch: project.DefaultBranch, LocalPath: project.LocalPath, TargetSource: "inherited"}}, nil
}

func normalizeTargetIDs(kind string, rawIDs []string) ([]string, error) {
	ids := make([]string, len(rawIDs))
	for i, rawID := range rawIDs {
		id := strings.TrimSpace(rawID)
		if !canonicalUUID.MatchString(id) {
			return nil, fmt.Errorf("ungültige %s-ID %q: erwartet wird eine kanonische UUID (z. B. 123e4567-e89b-12d3-a456-426614174000), keine Repository-URL", kind, rawID)
		}
		ids[i] = id
	}
	return ids, nil
}

// runRepositoryTargets is the only source of a run's checkout path. Agent
// profiles intentionally do not participate in repository resolution.
func runRepositoryTargets(targets []domain.RepositoryTarget) ([]domain.RepositoryTarget, error) {
	if len(targets) == 0 {
		return nil, errors.New("Kein verfügbares Projekt-Repository für diese Aufgabe. Weise ein eindeutiges Projektziel zu und starte den Run erneut.")
	}
	resolved := make([]domain.RepositoryTarget, len(targets))
	copy(resolved, targets)
	for i := range resolved {
		if strings.TrimSpace(resolved[i].RepositoryURL) == "" || strings.TrimSpace(resolved[i].ProjectID) == "" {
			return nil, errors.New("Das Projektziel ist nicht verfügbar. Wähle ein registriertes Projekt mit Repository und starte den Run erneut.")
		}
		if strings.TrimSpace(resolved[i].LocalPath) == "" {
			resolved[i].LocalPath = workspacecfg.ProjectPath(resolved[i].ProjectID)
		} else if workspacecfg.Root() != "" {
			resolved[i].LocalPath = workspacecfg.ProjectPath(resolved[i].ProjectID)
		}
	}
	return resolved, nil
}

func (s *Store) TaskRepositoryTargets(c context.Context, taskID string) ([]domain.RepositoryTarget, error) {
	rows, err := s.DB.Query(c, `SELECT id,task_id,COALESCE(project_id::text,''),project_name,repository_url,default_branch,local_path,target_source,source_groups,created_at FROM task_repository_targets WHERE task_id=$1 ORDER BY project_name`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.RepositoryTarget])
}

// EffectiveTaskRepositoryTargets prefers explicit task targets. If none were
// selected, exactly one project on the task's board is an unambiguous default.
// Boards with zero or multiple projects never fan out implicitly.
func (s *Store) EffectiveTaskRepositoryTargets(c context.Context, taskID string) ([]domain.RepositoryTarget, error) {
	targets, err := s.TaskRepositoryTargets(c, taskID)
	if err != nil || len(targets) > 0 {
		return targets, err
	}
	task, err := s.GetTask(c, taskID)
	if err != nil {
		return nil, err
	}
	projects, err := s.BoardProjects(c, task.BoardID)
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return nil, errors.New("Kein Projekt-Repository für diese Aufgabe zugewiesen. Weise ein eindeutiges Projektziel zu und starte den Run erneut.")
	}
	if len(projects) != 1 {
		return nil, errors.New("Mehrere Projekt-Repositories sind möglich. Weise der Aufgabe ein eindeutiges Projektziel oder eine Projektgruppe zu.")
	}
	project := projects[0]
	return []domain.RepositoryTarget{{TaskID: taskID, ProjectID: project.ID, ProjectName: project.Name, RepositoryURL: project.RepositoryURL, DefaultBranch: project.DefaultBranch, LocalPath: project.LocalPath, TargetSource: "inherited"}}, nil
}

func boardInheritanceEligibleColumn(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "backlog", "entwicklung", "in progress":
		return true
	default:
		return false
	}
}

func qaReviewColumn(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "qa", "review":
		return true
	default:
		return false
	}
}

// qaReviewReturn is true only when work is sent backward out of a QA/Review
// gate. Forward quality-chain steps (Review→QA) and completions (QA→Done,
// Review→Erledigt) must not be tagged as returns: they are not rework and
// must not be swallowed by the worker's return no-op filter.
func qaReviewReturn(currentName string, currentPosition int, targetType string, targetPosition int) bool {
	if !qaReviewColumn(currentName) || strings.EqualFold(strings.TrimSpace(targetType), "done") {
		return false
	}
	return targetPosition < currentPosition
}

// explicitReworkTransition increments the persistent rework/escalation
// counter. Column drag, MCP moves, and other generic sources must not: only a
// QA release decision or an agent_review return from a Review/QA gate is a
// counted rework.
func explicitReworkTransition(source string, qaReturn bool) bool {
	switch strings.TrimSpace(source) {
	case "qa_rework":
		return true
	case "agent_review":
		return qaReturn
	default:
		return false
	}
}
func (s *Store) TaskTargetProjects(c context.Context, taskID string) ([]domain.Project, error) {
	rows, err := s.DB.Query(c, `SELECT DISTINCT p.id,p.name,p.repository_url,p.default_branch,p.local_path,p.last_synced_at,p.last_sync_error,p.created_at,p.updated_at FROM projects p WHERE p.id IN (SELECT project_id FROM task_target_projects WHERE task_id=$1 UNION SELECT m.project_id FROM project_group_members m JOIN task_target_groups g ON g.group_id=m.group_id WHERE g.task_id=$1) ORDER BY p.name`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByNameLax[domain.Project])
}

func (s *Store) EffectiveTaskTargetProjects(c context.Context, taskID string) ([]domain.Project, error) {
	projects, err := s.TaskTargetProjects(c, taskID)
	if err != nil || len(projects) > 0 {
		return projects, err
	}
	task, err := s.GetTask(c, taskID)
	if err != nil {
		return nil, err
	}
	projects, err = s.BoardProjects(c, task.BoardID)
	if err != nil || len(projects) != 1 {
		return nil, err
	}
	return projects, nil
}

// EffectiveTaskTargetProjectsForTasks loads effective project targets for a
// board projection in bounded queries. It preserves the single-project board
// fallback used by EffectiveTaskTargetProjects without issuing one query per
// task.
func (s *Store) EffectiveTaskTargetProjectsForTasks(c context.Context, tasks []domain.Task) (map[string][]domain.Project, error) {
	result := make(map[string][]domain.Project, len(tasks))
	if len(tasks) == 0 {
		return result, nil
	}
	taskIDs := make([]string, 0, len(tasks))
	boardIDs := make([]string, 0, len(tasks))
	seenBoards := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		taskIDs = append(taskIDs, task.ID)
		if _, ok := seenBoards[task.BoardID]; !ok {
			seenBoards[task.BoardID] = struct{}{}
			boardIDs = append(boardIDs, task.BoardID)
		}
	}

	rows, err := s.DB.Query(c, `
		SELECT targets.task_id,p.id,p.name,p.repository_url,p.default_branch,p.local_path,p.last_synced_at,p.last_sync_error,p.created_at,p.updated_at
		FROM (
			SELECT task_id,project_id FROM task_target_projects WHERE task_id = ANY($1)
			UNION
			SELECT g.task_id,m.project_id
			FROM task_target_groups g JOIN project_group_members m ON m.group_id=g.group_id
			WHERE g.task_id = ANY($1)
		) targets
		JOIN projects p ON p.id=targets.project_id
		ORDER BY targets.task_id,p.name`, taskIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var taskID string
		var project domain.Project
		if err := rows.Scan(&taskID, &project.ID, &project.Name, &project.RepositoryURL, &project.DefaultBranch, &project.LocalPath, &project.LastSyncedAt, &project.LastSyncError, &project.CreatedAt, &project.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		result[taskID] = append(result[taskID], project)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	boardProjects := make(map[string][]domain.Project, len(boardIDs))
	boardRows, err := s.DB.Query(c, `
		SELECT bp.board_id,p.id,p.name,p.repository_url,p.default_branch,p.local_path,p.last_synced_at,p.last_sync_error,p.created_at,p.updated_at
		FROM board_projects bp JOIN projects p ON p.id=bp.project_id
		WHERE bp.board_id = ANY($1)
		ORDER BY bp.board_id,p.name`, boardIDs)
	if err != nil {
		return nil, err
	}
	for boardRows.Next() {
		var boardID string
		var project domain.Project
		if err := boardRows.Scan(&boardID, &project.ID, &project.Name, &project.RepositoryURL, &project.DefaultBranch, &project.LocalPath, &project.LastSyncedAt, &project.LastSyncError, &project.CreatedAt, &project.UpdatedAt); err != nil {
			boardRows.Close()
			return nil, err
		}
		boardProjects[boardID] = append(boardProjects[boardID], project)
	}
	if err := boardRows.Err(); err != nil {
		boardRows.Close()
		return nil, err
	}
	boardRows.Close()

	for _, task := range tasks {
		if len(result[task.ID]) == 0 && len(boardProjects[task.BoardID]) == 1 {
			result[task.ID] = boardProjects[task.BoardID]
		}
	}
	return result, nil
}
func (s *Store) TaskTargetGroups(c context.Context, taskID string) ([]domain.ProjectGroup, error) {
	rows, err := s.DB.Query(c, `SELECT g.id,g.name,g.description,g.color,g.created_at,g.updated_at FROM project_groups g JOIN task_target_groups t ON t.group_id=g.id WHERE t.task_id=$1 ORDER BY g.name`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByNameLax[domain.ProjectGroup])
}
func (s *Store) Project(c context.Context, id string) (domain.Project, error) {
	var p domain.Project
	err := s.DB.QueryRow(c, `SELECT id,name,repository_url,default_branch,local_path,last_synced_at,last_sync_error,created_at,updated_at FROM projects WHERE id=$1`, id).Scan(&p.ID, &p.Name, &p.RepositoryURL, &p.DefaultBranch, &p.LocalPath, &p.LastSyncedAt, &p.LastSyncError, &p.CreatedAt, &p.UpdatedAt)
	if err == nil {
		p.Boards, err = s.ProjectBoards(c, p.ID)
	}
	return p, err
}
func (s *Store) ProjectBoards(c context.Context, projectID string) ([]domain.Board, error) {
	rows, err := s.DB.Query(c, `SELECT b.id,b.name,b.created_at FROM boards b JOIN board_projects bp ON bp.board_id=b.id WHERE bp.project_id=$1 ORDER BY b.name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.Board])
}
func (s *Store) BoardProjects(c context.Context, boardID string) ([]domain.Project, error) {
	rows, err := s.DB.Query(c, `SELECT p.id,p.name,p.repository_url,p.default_branch,p.local_path,p.last_synced_at,p.last_sync_error,p.created_at,p.updated_at FROM projects p JOIN board_projects bp ON bp.project_id=p.id WHERE bp.board_id=$1 ORDER BY p.name`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByNameLax[domain.Project])
}
func (s *Store) CreateProject(c context.Context, name, repo, branch, path string, boardIDs []string) (domain.Project, error) {
	if branch == "" {
		branch = "main"
	}
	tx, err := s.DB.Begin(c)
	if err != nil {
		return domain.Project{}, err
	}
	defer tx.Rollback(c)
	var p domain.Project
	err = tx.QueryRow(c, `INSERT INTO projects(name,repository_url,default_branch,local_path) VALUES($1,$2,$3,$4) RETURNING id,name,repository_url,default_branch,local_path,last_synced_at,last_sync_error,created_at,updated_at`, strings.TrimSpace(name), strings.TrimSpace(repo), strings.TrimSpace(branch), strings.TrimSpace(path)).Scan(&p.ID, &p.Name, &p.RepositoryURL, &p.DefaultBranch, &p.LocalPath, &p.LastSyncedAt, &p.LastSyncError, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, err
	}
	if p.RepositoryURL != "" && p.LocalPath == "" {
		p.LocalPath = workspacecfg.ProjectPath(p.ID)
		if _, err = tx.Exec(c, `UPDATE projects SET local_path=$2 WHERE id=$1`, p.ID, p.LocalPath); err != nil {
			return p, err
		}
	}
	for _, boardID := range boardIDs {
		if _, err = tx.Exec(c, `INSERT INTO board_projects(board_id,project_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, boardID, p.ID); err != nil {
			return p, err
		}
	}
	return p, tx.Commit(c)
}
func (s *Store) UpdateProject(c context.Context, id, name, repo, branch, path string, boardIDs []string) error {
	if branch == "" {
		branch = "main"
	}
	tx, err := s.DB.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if _, err = tx.Exec(c, `UPDATE projects SET name=$2,repository_url=$3,default_branch=$4,local_path=$5,updated_at=now() WHERE id=$1`, id, strings.TrimSpace(name), strings.TrimSpace(repo), strings.TrimSpace(branch), strings.TrimSpace(path)); err != nil {
		return err
	}
	if _, err = tx.Exec(c, `DELETE FROM board_projects WHERE project_id=$1`, id); err != nil {
		return err
	}
	for _, boardID := range boardIDs {
		if _, err = tx.Exec(c, `INSERT INTO board_projects(board_id,project_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, boardID, id); err != nil {
			return err
		}
	}
	return tx.Commit(c)
}

// SetProjectDefaultBranch records the branch discovered from a repository's
// remote HEAD. This prevents a project imported with the conventional "main"
// default from remaining permanently unsynchronisable when its repository
// still uses another default such as "master".
func (s *Store) SetProjectDefaultBranch(c context.Context, id, branch string) error {
	_, err := s.DB.Exec(c, `UPDATE projects SET default_branch=$2,updated_at=now() WHERE id=$1`, id, strings.TrimSpace(branch))
	return err
}
func (s *Store) DeleteProject(c context.Context, id string) error {
	// A project group is catalogue metadata, not a durable task target. Task
	// repository targets are resolved and snapshotted when the task is saved,
	// so removing an otherwise unused group here cannot alter an existing
	// task's scope. Keep the catalogue free of empty groups after deletion.
	tx, err := s.DB.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if _, err = tx.Exec(c, `DELETE FROM projects WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(c, `DELETE FROM project_groups g WHERE NOT EXISTS (SELECT 1 FROM project_group_members m WHERE m.group_id=g.id)`); err != nil {
		return err
	}
	return tx.Commit(c)
}
func (s *Store) RecordProjectSync(c context.Context, id, problem string) error {
	_, err := s.DB.Exec(c, `UPDATE projects SET last_synced_at=now(),last_sync_error=$2,updated_at=now() WHERE id=$1`, id, problem)
	return err
}
func (s *Store) Dashboard(c context.Context) (domain.Dashboard, error) {
	var d domain.Dashboard
	if err := s.DB.QueryRow(c, "SELECT count(*),count(*) FILTER (WHERE completed_at IS NULL),count(*) FILTER (WHERE completed_at IS NOT NULL) FROM tasks").Scan(&d.Total, &d.Active, &d.Completed); err != nil {
		return d, err
	}
	rows, err := s.DB.Query(c, "SELECT b.name || ' / ' || c.name,count(t.id) FROM workflow_columns c JOIN boards b ON b.id=c.board_id LEFT JOIN tasks t ON t.column_id=c.id GROUP BY b.name,c.id,c.name,c.position HAVING count(t.id) > 0 ORDER BY b.name,c.position")
	if err != nil {
		return d, err
	}
	defer rows.Close()
	d.ByColumn, err = pgx.CollectRows(rows, pgx.RowToStructByPos[domain.Metric])
	if err != nil {
		return d, err
	}
	rows, err = s.DB.Query(c, "SELECT priority,count(*) FROM tasks GROUP BY priority ORDER BY priority")
	if err != nil {
		return d, err
	}
	defer rows.Close()
	d.ByPriority, err = pgx.CollectRows(rows, pgx.RowToStructByPos[domain.Metric])
	if err != nil {
		return d, err
	}
	if err = s.RefreshQueueState(c); err != nil {
		return d, err
	}
	if err = s.DB.QueryRow(c, `SELECT count(*) FILTER (WHERE status='queued'),count(*) FILTER (WHERE status='queued' AND queue_wait_reason <> ''),count(*) FILTER (WHERE status='running'),count(*) FILTER (WHERE status='succeeded'),count(*) FILTER (WHERE status='failed') FROM agent_runs`).Scan(&d.Runs.Queued, &d.Runs.ResourceWaiting, &d.Runs.Running, &d.Runs.Succeeded, &d.Runs.Failed); err != nil {
		return d, err
	}
	if err = s.DB.QueryRow(c, `SELECT COALESCE(sum(estimated_cost_microusd),0) FROM agent_runs`).Scan(&d.EstimatedCostMicrousd); err != nil {
		return d, err
	}
	if err = s.DB.QueryRow(c, `SELECT COALESCE(sum(calculated_cost_microusd) FILTER (WHERE cost_source='reported'),0), COALESCE(sum(calculated_cost_microusd) FILTER (WHERE cost_source='estimated'),0), count(*) FILTER (WHERE cost_source IN ('included','unknown')), COALESCE(sum(COALESCE(usage_total_tokens, token_usage)) FILTER (WHERE cost_source IN ('included','unknown')),0) FROM agent_runs`).Scan(&d.KnownActualCostMicrousd, &d.EstimatedCostMicrousdV2, &d.IncludedOrUnknownRuns, &d.IncludedOrUnknownTokens); err != nil {
		return d, err
	}
	if err = s.DB.QueryRow(c, `SELECT sum(usage_input_tokens),sum(usage_output_tokens),sum(usage_cached_input_tokens),sum(usage_cache_write_tokens),sum(usage_reasoning_tokens),sum(COALESCE(usage_total_tokens,token_usage)) FROM agent_runs`).Scan(&d.UsageTokenBreakdown.InputTokens, &d.UsageTokenBreakdown.OutputTokens, &d.UsageTokenBreakdown.CachedInputTokens, &d.UsageTokenBreakdown.CacheWriteTokens, &d.UsageTokenBreakdown.ReasoningTokens, &d.UsageTokenBreakdown.TotalTokens); err != nil {
		return d, err
	}
	costs, err := s.DB.Query(c, `SELECT a.name,COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source IN ('reported','estimated')),0) FROM agent_runs r JOIN agents a ON a.id=r.agent_id GROUP BY a.id,a.name HAVING COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source IN ('reported','estimated')),0)>0 ORDER BY 2 DESC,1`)
	if err != nil {
		return d, err
	}
	defer costs.Close()
	d.CostByAgent, err = pgx.CollectRows(costs, pgx.RowToStructByPos[domain.CostMetric])
	if err != nil {
		return d, err
	}
	dimensions, err := s.DB.Query(c, `SELECT 'provider',COALESCE(NULLIF(usage_provider,''),'unknown'),COALESCE(sum(COALESCE(usage_total_tokens,token_usage)),0),COALESCE(sum(calculated_cost_microusd) FILTER (WHERE cost_source='reported'),0),COALESCE(sum(calculated_cost_microusd) FILTER (WHERE cost_source='estimated'),0) FROM agent_runs GROUP BY usage_provider UNION ALL SELECT 'model',COALESCE(NULLIF(usage_model,''),'unknown'),COALESCE(sum(COALESCE(usage_total_tokens,token_usage)),0),COALESCE(sum(calculated_cost_microusd) FILTER (WHERE cost_source='reported'),0),COALESCE(sum(calculated_cost_microusd) FILTER (WHERE cost_source='estimated'),0) FROM agent_runs GROUP BY usage_model UNION ALL SELECT 'agent',a.name,COALESCE(sum(COALESCE(r.usage_total_tokens,r.token_usage)),0),COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source='reported'),0),COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source='estimated'),0) FROM agent_runs r JOIN agents a ON a.id=r.agent_id GROUP BY a.name ORDER BY 1,2`)
	if err != nil {
		return d, err
	}
	defer dimensions.Close()
	d.UsageByDimension, err = pgx.CollectRows(dimensions, pgx.RowToStructByPos[domain.UsageMetric])
	if err != nil {
		return d, err
	}
	n, err := s.DB.Query(c, "SELECT id,COALESCE(task_id::text,''),COALESCE(agent_run_id::text,''),kind,message,created_at FROM notifications WHERE read_at IS NULL ORDER BY created_at DESC LIMIT 6")
	if err != nil {
		return d, err
	}
	defer n.Close()
	d.Notifications, err = pgx.CollectRows(n, pgx.RowToStructByPos[domain.Notification])
	if err != nil {
		return d, err
	}
	created, err := s.DB.Query(c, `SELECT to_char(day,'DD.MM'),count(t.id)::int FROM generate_series(current_date-13,current_date,interval '1 day') day LEFT JOIN tasks t ON t.created_at >= day AND t.created_at < day + interval '1 day' GROUP BY day ORDER BY day`)
	if err != nil {
		return d, err
	}
	defer created.Close()
	d.CreatedSeries, err = pgx.CollectRows(created, pgx.RowToStructByPos[domain.Metric])
	if err != nil {
		return d, err
	}
	completed, err := s.DB.Query(c, `SELECT to_char(day,'DD.MM'),count(t.id)::int FROM generate_series(current_date-13,current_date,interval '1 day') day LEFT JOIN tasks t ON t.completed_at >= day AND t.completed_at < day + interval '1 day' GROUP BY day ORDER BY day`)
	if err != nil {
		return d, err
	}
	defer completed.Close()
	d.CompletedSeries, err = pgx.CollectRows(completed, pgx.RowToStructByPos[domain.Metric])
	return d, err
}

// DashboardFiltered keeps the operational dashboard stable while allowing
// telemetry consumers to request a reproducible historical slice.
func (s *Store) DashboardFiltered(c context.Context, from, to *time.Time, provider, model, agent, board string) (domain.Dashboard, error) {
	d, err := s.Dashboard(c)
	if err != nil {
		return d, err
	}
	// The legacy dashboard contains operational task metrics as well as
	// telemetry. Keep those metrics on the same filtered run/task population
	// when a telemetry filter is active. For an entirely unfiltered request,
	// preserve the operational population, including tasks without agent runs.
	taskConditions, taskArgs := dashboardTaskFilter(from, to, provider, model, agent, board)
	taskWhere := `WHERE ` + taskConditions
	where := `WHERE ($1::timestamptz IS NULL OR r.created_at >= $1) AND ($2::timestamptz IS NULL OR r.created_at < $2) AND ($3='' OR r.usage_provider=$3) AND ($4='' OR r.usage_model=$4) AND (NULLIF($5,'')::uuid IS NULL OR r.agent_id=NULLIF($5,'')::uuid) AND (NULLIF($6,'')::uuid IS NULL OR r.task_id IN (SELECT id FROM tasks WHERE board_id=NULLIF($6,'')::uuid))`
	args := []any{from, to, provider, model, agent, board}
	if err = s.DB.QueryRow(c, `SELECT count(*),count(*) FILTER (WHERE t.completed_at IS NULL),count(*) FILTER (WHERE t.completed_at IS NOT NULL) FROM tasks t `+taskWhere, taskArgs...).Scan(&d.Total, &d.Active, &d.Completed); err != nil {
		return d, err
	}
	columns, err := s.DB.Query(c, `SELECT b.name || ' / ' || c.name,count(t.id) FROM workflow_columns c JOIN boards b ON b.id=c.board_id LEFT JOIN tasks t ON t.column_id=c.id `+taskWhere+` GROUP BY b.name,c.id,c.name,c.position HAVING count(t.id)>0 ORDER BY b.name,c.position`, taskArgs...)
	if err != nil {
		return d, err
	}
	d.ByColumn, err = pgx.CollectRows(columns, pgx.RowToStructByPos[domain.Metric])
	columns.Close()
	if err != nil {
		return d, err
	}
	priorities, err := s.DB.Query(c, `SELECT t.priority,count(*) FROM tasks t `+taskWhere+` GROUP BY t.priority ORDER BY t.priority`, taskArgs...)
	if err != nil {
		return d, err
	}
	d.ByPriority, err = pgx.CollectRows(priorities, pgx.RowToStructByPos[domain.Metric])
	priorities.Close()
	if err != nil {
		return d, err
	}
	if err = s.DB.QueryRow(c, `SELECT count(*) FILTER (WHERE r.status='queued'),count(*) FILTER (WHERE r.status='running'),count(*) FILTER (WHERE r.status='succeeded'),count(*) FILTER (WHERE r.status='failed') FROM agent_runs r `+where, args...).Scan(&d.Runs.Queued, &d.Runs.Running, &d.Runs.Succeeded, &d.Runs.Failed); err != nil {
		return d, err
	}
	if err = s.DB.QueryRow(c, `SELECT COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source='reported'),0),COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source='estimated'),0),count(*) FILTER (WHERE r.cost_source IN ('included','unknown')),COALESCE(sum(COALESCE(r.usage_total_tokens,r.token_usage)) FILTER (WHERE r.cost_source IN ('included','unknown')),0) FROM agent_runs r `+where, args...).Scan(&d.KnownActualCostMicrousd, &d.EstimatedCostMicrousdV2, &d.IncludedOrUnknownRuns, &d.IncludedOrUnknownTokens); err != nil {
		return d, err
	}
	if err = s.DB.QueryRow(c, `SELECT sum(r.usage_input_tokens),sum(r.usage_output_tokens),sum(r.usage_cached_input_tokens),sum(r.usage_cache_write_tokens),sum(r.usage_reasoning_tokens),sum(COALESCE(r.usage_total_tokens,r.token_usage)) FROM agent_runs r `+where, args...).Scan(&d.UsageTokenBreakdown.InputTokens, &d.UsageTokenBreakdown.OutputTokens, &d.UsageTokenBreakdown.CachedInputTokens, &d.UsageTokenBreakdown.CacheWriteTokens, &d.UsageTokenBreakdown.ReasoningTokens, &d.UsageTokenBreakdown.TotalTokens); err != nil {
		return d, err
	}
	rows, err := s.DB.Query(c, `SELECT 'provider',COALESCE(NULLIF(r.usage_provider,''),'unknown'),COALESCE(sum(COALESCE(r.usage_total_tokens,r.token_usage)),0),COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source='reported'),0),COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source='estimated'),0) FROM agent_runs r `+where+` GROUP BY r.usage_provider UNION ALL SELECT 'model',COALESCE(NULLIF(r.usage_model,''),'unknown'),COALESCE(sum(COALESCE(r.usage_total_tokens,r.token_usage)),0),COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source='reported'),0),COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source='estimated'),0) FROM agent_runs r `+where+` GROUP BY r.usage_model UNION ALL SELECT 'agent',a.name,COALESCE(sum(COALESCE(r.usage_total_tokens,r.token_usage)),0),COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source='reported'),0),COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source='estimated'),0) FROM agent_runs r JOIN agents a ON a.id=r.agent_id `+where+` GROUP BY a.name UNION ALL SELECT 'board',b.name,COALESCE(sum(COALESCE(r.usage_total_tokens,r.token_usage)),0),COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source='reported'),0),COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source='estimated'),0) FROM agent_runs r JOIN tasks t ON t.id=r.task_id JOIN boards b ON b.id=t.board_id `+where+` GROUP BY b.name ORDER BY 1,2`, args...)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	d.UsageByDimension, err = pgx.CollectRows(rows, pgx.RowToStructByPos[domain.UsageMetric])
	if err != nil {
		return d, err
	}
	costRows, err := s.DB.Query(c, `SELECT a.name,COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source IN ('reported','estimated')),0) FROM agent_runs r JOIN agents a ON a.id=r.agent_id `+where+` GROUP BY a.id,a.name HAVING COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source IN ('reported','estimated')),0)>0 ORDER BY 2 DESC,1`, args...)
	if err != nil {
		return d, err
	}
	defer costRows.Close()
	d.CostByAgent, err = pgx.CollectRows(costRows, pgx.RowToStructByPos[domain.CostMetric])
	if err != nil {
		return d, err
	}
	seriesFrom, seriesTo := from, to
	if seriesFrom == nil {
		var start time.Time
		if err = s.DB.QueryRow(c, `SELECT COALESCE(min(r.created_at), current_date) FROM agent_runs r `+where, args...).Scan(&start); err != nil {
			return d, err
		}
		seriesFrom = &start
	}
	if seriesTo == nil {
		end := time.Now().AddDate(0, 0, 1)
		seriesTo = &end
	}
	series, err := s.DB.Query(c, `SELECT to_char(day,'YYYY-MM-DD'),COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source='reported'),0),COALESCE(sum(r.calculated_cost_microusd) FILTER (WHERE r.cost_source='estimated'),0),COALESCE(sum(COALESCE(r.usage_total_tokens,r.token_usage)),0) FROM generate_series($1::timestamptz,$2::timestamptz-interval '1 day',interval '1 day') day LEFT JOIN agent_runs r ON r.created_at >= day AND r.created_at < day + interval '1 day' AND r.created_at >= $1 AND r.created_at < $2 AND ($3='' OR r.usage_provider=$3) AND ($4='' OR r.usage_model=$4) AND (NULLIF($5,'')::uuid IS NULL OR r.agent_id=NULLIF($5,'')::uuid) AND (NULLIF($6,'')::uuid IS NULL OR r.task_id IN (SELECT id FROM tasks WHERE board_id=NULLIF($6,'')::uuid)) GROUP BY day ORDER BY day`, seriesFrom, seriesTo, provider, model, agent, board)
	if err != nil {
		return d, err
	}
	defer series.Close()
	d.TelemetrySeries, err = pgx.CollectRows(series, pgx.RowToStructByPos[domain.TelemetryPoint])
	if err != nil {
		return d, err
	}
	// Task throughput follows the same selected interval and dimensions. For
	// an unbounded request retain the legacy 14-day chart window.
	taskSeriesFrom, taskSeriesTo := from, to
	if taskSeriesFrom == nil {
		start := time.Now().AddDate(0, 0, -13)
		taskSeriesFrom = &start
	}
	if taskSeriesTo == nil {
		end := time.Now().AddDate(0, 0, 1)
		taskSeriesTo = &end
	}
	taskSeriesConditions := "TRUE"
	seriesArgs := []any{taskSeriesFrom, taskSeriesTo}
	if from != nil || to != nil || provider != "" || model != "" || agent != "" {
		taskSeriesConditions = `EXISTS (SELECT 1 FROM agent_runs ar WHERE ar.task_id=t.id AND ($3::timestamptz IS NULL OR ar.created_at >= $3) AND ($4::timestamptz IS NULL OR ar.created_at < $4) AND ($5='' OR ar.usage_provider=$5) AND ($6='' OR ar.usage_model=$6) AND (NULLIF($7,'')::uuid IS NULL OR ar.agent_id=NULLIF($7,'')::uuid))`
		seriesArgs = append(seriesArgs, from, to, provider, model, agent)
	}
	if board != "" {
		placeholder := len(seriesArgs) + 1
		taskSeriesConditions += fmt.Sprintf(` AND (NULLIF($%d,'')::uuid IS NULL OR t.board_id=NULLIF($%d,'')::uuid)`, placeholder, placeholder)
		seriesArgs = append(seriesArgs, board)
	}
	createdTasks, err := s.DB.Query(c, `SELECT to_char(day,'DD.MM'),count(t.id)::int FROM generate_series($1::timestamptz,$2::timestamptz-interval '1 day',interval '1 day') day LEFT JOIN tasks t ON t.created_at >= day AND t.created_at < day + interval '1 day' AND `+taskSeriesConditions+` GROUP BY day ORDER BY day`, seriesArgs...)
	if err != nil {
		return d, err
	}
	d.CreatedSeries, err = pgx.CollectRows(createdTasks, pgx.RowToStructByPos[domain.Metric])
	createdTasks.Close()
	if err != nil {
		return d, err
	}
	completedTasks, err := s.DB.Query(c, `SELECT to_char(day,'DD.MM'),count(t.id)::int FROM generate_series($1::timestamptz,$2::timestamptz-interval '1 day',interval '1 day') day LEFT JOIN tasks t ON t.completed_at >= day AND t.completed_at < day + interval '1 day' AND `+taskSeriesConditions+` GROUP BY day ORDER BY day`, seriesArgs...)
	if err != nil {
		return d, err
	}
	d.CompletedSeries, err = pgx.CollectRows(completedTasks, pgx.RowToStructByPos[domain.Metric])
	completedTasks.Close()
	return d, err
}

func dashboardTaskFilter(from, to *time.Time, provider, model, agent, board string) (string, []any) {
	conditions := "TRUE"
	args := make([]any, 0, 6)
	if from != nil || to != nil || provider != "" || model != "" || agent != "" {
		conditions = `EXISTS (SELECT 1 FROM agent_runs ar WHERE ar.task_id=t.id
			AND ($1::timestamptz IS NULL OR ar.created_at >= $1)
			AND ($2::timestamptz IS NULL OR ar.created_at < $2)
			AND ($3='' OR ar.usage_provider=$3)
			AND ($4='' OR ar.usage_model=$4)
			AND (NULLIF($5,'')::uuid IS NULL OR ar.agent_id=NULLIF($5,'')::uuid))`
		args = append(args, from, to, provider, model, agent)
	}
	if board != "" {
		placeholder := len(args) + 1
		conditions += fmt.Sprintf(` AND (NULLIF($%d,'')::uuid IS NULL OR t.board_id=NULLIF($%d,'')::uuid)`, placeholder, placeholder)
		args = append(args, board)
	}
	return conditions, args
}

func (s *Store) DashboardAttention(c context.Context) (domain.DashboardAttention, error) {
	var a domain.DashboardAttention
	err := s.DB.QueryRow(c, `SELECT
		(SELECT count(*) FROM tasks t JOIN workflow_columns c ON c.id=t.column_id WHERE c.column_type='needs_action' AND t.completed_at IS NULL),
		(SELECT count(*) FROM (
			SELECT DISTINCT ON (task_id) task_id,status,COALESCE(finished_at,created_at) AS terminal_at
			FROM agent_runs ORDER BY task_id,created_at DESC,id DESC
		) latest_run WHERE status='failed' AND terminal_at>=now()-interval '7 days'),
		(SELECT count(*) FROM agent_interactions i JOIN tasks t ON t.id=i.task_id WHERE i.status='open' AND t.completed_at IS NULL),
		(SELECT count(*) FROM tasks WHERE completed_at IS NULL AND due_date IS NOT NULL AND due_date<=now()+interval '24 hours')`).
		Scan(&a.BlockedTasks, &a.FailedRuns7d, &a.OpenInteractions, &a.DueNext24h)
	return a, err
}
func (s *Store) CreateBoard(c context.Context, name string) (domain.Board, error) {
	return s.CreateBoardWithTemplate(c, name, "software")
}

type workflowColumnSpec struct {
	name              string
	initial, terminal bool
}
type workflowTransitionSpec struct{ from, to, action string }
type workflowSpec struct {
	columns     []workflowColumnSpec
	transitions []workflowTransitionSpec
	labels      []labelSpec
}
type labelSpec struct{ name, color string }

var workflowTemplates = map[string]workflowSpec{
	"software":   {columns: []workflowColumnSpec{{"Inbox", true, false}, {"Backlog", false, false}, {"Entwicklung", false, false}, {"Review", false, false}, {"Erledigt", false, true}}, transitions: []workflowTransitionSpec{{"Inbox", "Backlog", "Einplanen"}, {"Backlog", "Entwicklung", "Starten"}, {"Entwicklung", "Review", "Zur Prüfung"}, {"Review", "Entwicklung", "Überarbeiten"}, {"Review", "Erledigt", "Abschließen"}}},
	"product":    {columns: []workflowColumnSpec{{"Ideen", true, false}, {"Discovery", false, false}, {"Bereit", false, false}, {"Umsetzung", false, false}, {"Ausgeliefert", false, true}}, transitions: []workflowTransitionSpec{{"Ideen", "Discovery", "Validieren"}, {"Discovery", "Bereit", "Priorisieren"}, {"Bereit", "Umsetzung", "Starten"}, {"Umsetzung", "Ausgeliefert", "Liefern"}, {"Discovery", "Ideen", "Zurückstellen"}}},
	"bug-triage": {columns: []workflowColumnSpec{{"Eingang", true, false}, {"Triage", false, false}, {"Reproduzierbar", false, false}, {"Fix", false, false}, {"Verifiziert", false, true}}, transitions: []workflowTransitionSpec{{"Eingang", "Triage", "Sichten"}, {"Triage", "Reproduzierbar", "Bestätigen"}, {"Triage", "Verifiziert", "Kein Fehler"}, {"Reproduzierbar", "Fix", "Beheben"}, {"Fix", "Verifiziert", "Prüfen"}, {"Verifiziert", "Fix", "Wieder öffnen"}}},
	"support":    {columns: []workflowColumnSpec{{"Neu", true, false}, {"Analyse", false, false}, {"Warten auf Kunde", false, false}, {"Lösung", false, false}, {"Gelöst", false, true}}, transitions: []workflowTransitionSpec{{"Neu", "Analyse", "Übernehmen"}, {"Analyse", "Warten auf Kunde", "Rückfrage"}, {"Warten auf Kunde", "Analyse", "Antwort erhalten"}, {"Analyse", "Lösung", "Lösung erstellen"}, {"Lösung", "Gelöst", "Schließen"}}},
	"content":    {columns: []workflowColumnSpec{{"Themen", true, false}, {"Recherche", false, false}, {"Entwurf", false, false}, {"Redaktion", false, false}, {"Veröffentlicht", false, true}}, transitions: []workflowTransitionSpec{{"Themen", "Recherche", "Auswählen"}, {"Recherche", "Entwurf", "Schreiben"}, {"Entwurf", "Redaktion", "Einreichen"}, {"Redaktion", "Entwurf", "Korrigieren"}, {"Redaktion", "Veröffentlicht", "Veröffentlichen"}}},
	"marketing":  {columns: []workflowColumnSpec{{"Briefing", true, false}, {"Konzept", false, false}, {"Produktion", false, false}, {"Freigabe", false, false}, {"Live", false, true}}, transitions: []workflowTransitionSpec{{"Briefing", "Konzept", "Planen"}, {"Konzept", "Produktion", "Produzieren"}, {"Produktion", "Freigabe", "Freigeben lassen"}, {"Freigabe", "Produktion", "Anpassen"}, {"Freigabe", "Live", "Aktivieren"}}},
	"research":   {columns: []workflowColumnSpec{{"Fragen", true, false}, {"Untersuchung", false, false}, {"Auswertung", false, false}, {"Entscheidung", false, false}, {"Archiv", false, true}}, transitions: []workflowTransitionSpec{{"Fragen", "Untersuchung", "Untersuchen"}, {"Untersuchung", "Auswertung", "Auswerten"}, {"Auswertung", "Entscheidung", "Entscheiden"}, {"Entscheidung", "Archiv", "Dokumentieren"}, {"Entscheidung", "Untersuchung", "Vertiefen"}}},
	"planning":   {columns: []workflowColumnSpec{{"Sammlung", true, false}, {"Schätzung", false, false}, {"Geplant", false, false}, {"In Arbeit", false, false}, {"Fertig", false, true}}, transitions: []workflowTransitionSpec{{"Sammlung", "Schätzung", "Schätzen"}, {"Schätzung", "Geplant", "Einplanen"}, {"Geplant", "In Arbeit", "Beginnen"}, {"In Arbeit", "Fertig", "Fertigstellen"}, {"In Arbeit", "Geplant", "Zurückplanen"}}},
	"release":    {columns: []workflowColumnSpec{{"Vorschlag", true, false}, {"Vorbereitung", false, false}, {"Staging", false, false}, {"Freigabe", false, false}, {"Produktion", false, true}}, transitions: []workflowTransitionSpec{{"Vorschlag", "Vorbereitung", "Einplanen"}, {"Vorbereitung", "Staging", "Deployen"}, {"Staging", "Freigabe", "Abnehmen"}, {"Freigabe", "Produktion", "Veröffentlichen"}, {"Staging", "Vorbereitung", "Rollback"}}},
	"personal": {columns: []workflowColumnSpec{{"Inbox", true, false}, {"Backlog", false, false}, {"In Progress", false, false}, {"Review", false, false}, {"QA", false, false}, {"Blocked", false, false}, {"Done", false, true}}, transitions: []workflowTransitionSpec{
		{"Inbox", "Backlog", "Einordnen"},
		{"Backlog", "In Progress", "Implementierung starten"}, {"Backlog", "Blocked", "Rückfrage erforderlich"},
		{"In Progress", "Review", "Änderung zur Review geben"}, {"In Progress", "Blocked", "Arbeit blockiert"},
		{"Review", "In Progress", "Nacharbeit anfordern"}, {"Review", "QA", "Zur QA geben"}, {"Review", "Blocked", "Rückfrage erforderlich"},
		{"QA", "Done", "Freigeben"}, {"QA", "In Progress", "Zur Entwicklung zurück"}, {"QA", "Blocked", "Rückfrage erforderlich"},
		{"Blocked", "In Progress", "Antwort: sofort fortsetzen"}, {"Blocked", "Backlog", "Antwort: erneut planen"}, {"Blocked", "QA", "QA erneut prüfen"}, {"Blocked", "Done", "Antwort: freigeben"},
		{"Done", "Backlog", "Wieder öffnen"},
	}, labels: []labelSpec{{"Frontend", "#176f8a"}, {"Backend", "#52627f"}, {"Infrastruktur", "#83683b"}, {"Sonstiges", "#64748b"}}},
}

func BoardTemplates() []domain.BoardTemplate {
	return []domain.BoardTemplate{
		{ID: "software", Name: "Software-Delivery", Summary: "Vom Eingang über Entwicklung und Review bis zum Abschluss.", Detail: "Für Produkt- und Plattformteams mit klarer Qualitätsprüfung und Rückweg aus dem Review.", Columns: []string{"Inbox", "Backlog", "Entwicklung", "Review", "Erledigt"}},
		{ID: "product", Name: "Produkt-Discovery", Summary: "Ideen validieren, priorisieren und ausliefern.", Detail: "Für Produktentscheidungen, bei denen Discovery vor der Umsetzung sichtbar bleiben soll.", Columns: []string{"Ideen", "Discovery", "Bereit", "Umsetzung", "Ausgeliefert"}},
		{ID: "bug-triage", Name: "Bug-Triage", Summary: "Meldungen reproduzieren, beheben und verifizieren.", Detail: "Für Fehlerströme mit klarer Triage und einer expliziten Verifikation des Fixes.", Columns: []string{"Eingang", "Triage", "Reproduzierbar", "Fix", "Verifiziert"}},
		{ID: "support", Name: "Support", Summary: "Tickets analysieren, Rückfragen steuern und lösen.", Detail: "Für Kunden- oder interne Supportfälle mit einem sichtbaren Wartezustand.", Columns: []string{"Neu", "Analyse", "Warten auf Kunde", "Lösung", "Gelöst"}},
		{ID: "content", Name: "Content-Redaktion", Summary: "Von der Themenidee zum veröffentlichten Inhalt.", Detail: "Für Artikel, Dokumentation, Newsletter und andere redaktionelle Abläufe.", Columns: []string{"Themen", "Recherche", "Entwurf", "Redaktion", "Veröffentlicht"}},
		{ID: "marketing", Name: "Kampagne", Summary: "Briefing, Produktion, Freigabe und Livegang.", Detail: "Für Marketing- und Kommunikationsmaßnahmen mit verbindlicher Freigabestufe.", Columns: []string{"Briefing", "Konzept", "Produktion", "Freigabe", "Live"}},
		{ID: "research", Name: "Research", Summary: "Fragen untersuchen und Entscheidungen dokumentieren.", Detail: "Für technische Recherche, Architekturentscheidungen und Experimente.", Columns: []string{"Fragen", "Untersuchung", "Auswertung", "Entscheidung", "Archiv"}},
		{ID: "planning", Name: "Sprint-Planung", Summary: "Arbeit sammeln, schätzen, planen und erledigen.", Detail: "Für kleine Teams, die eine leichte Planungsschleife vor der Umsetzung brauchen.", Columns: []string{"Sammlung", "Schätzung", "Geplant", "In Arbeit", "Fertig"}},
		{ID: "release", Name: "Release-Management", Summary: "Änderungen sicher über Staging in Produktion bringen.", Detail: "Für Releases mit Abnahme und kontrolliertem Rückweg bei Problemen.", Columns: []string{"Vorschlag", "Vorbereitung", "Staging", "Freigabe", "Produktion"}},
		{ID: "personal", Name: "Persönliche Entwicklung", Summary: "Manuell einordnen, mit spezialisierten Agents umsetzen und menschlich freigeben.", Detail: "Für persönliche Softwarearbeit: Inbox bleibt manuell, Backlog kann nach Fachgebiet automatisiert werden, Review und QA bilden eine klare Qualitätskette. Agenten und Automationen werden nach dem Erstellen gezielt zugewiesen.", Columns: []string{"Inbox", "Backlog", "In Progress", "Review", "QA", "Blocked", "Done"}},
	}
}

func columnTypeForTemplate(workflow string, column workflowColumnSpec) string {
	if column.initial {
		return "inbox"
	}
	if column.terminal {
		return "done"
	}
	if workflow == "personal" && column.name == "Blocked" {
		return "needs_action"
	}
	return "standard"
}

func (s *Store) CreateBoardWithTemplate(c context.Context, name, workflow string) (domain.Board, error) {
	var b domain.Board
	tx, err := s.DB.Begin(c)
	if err != nil {
		return b, err
	}
	defer tx.Rollback(c)
	err = tx.QueryRow(c, "INSERT INTO boards(name) VALUES($1) RETURNING id,name,created_at", strings.TrimSpace(name)).Scan(&b.ID, &b.Name, &b.CreatedAt)
	if err != nil || workflow == "empty" {
		if err != nil {
			return b, err
		}
		return b, tx.Commit(c)
	}
	spec, ok := workflowTemplates[workflow]
	if !ok {
		return b, fmt.Errorf("unbekannte Board-Vorlage: %s", workflow)
	}
	columns := spec.columns
	ids := make([]string, len(columns))
	for i, col := range columns {
		columnType := columnTypeForTemplate(workflow, col)
		if err = tx.QueryRow(c, "INSERT INTO workflow_columns(board_id,name,column_type,position,is_initial,is_terminal,canvas_x,canvas_y) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id", b.ID, col.name, columnType, i, col.initial, col.terminal, 80+i*280, 160).Scan(&ids[i]); err != nil {
			return b, err
		}
	}
	byName := make(map[string]string, len(columns))
	for index, column := range columns {
		byName[column.name] = ids[index]
	}
	for _, edge := range spec.transitions {
		if _, err = tx.Exec(c, "INSERT INTO transitions(board_id,from_column_id,to_column_id,action_name) VALUES($1,$2,$3,$4)", b.ID, byName[edge.from], byName[edge.to], edge.action); err != nil {
			return b, err
		}
	}
	for _, label := range spec.labels {
		if _, err = tx.Exec(c, "INSERT INTO labels(board_id,name,color) VALUES($1,$2,$3)", b.ID, label.name, label.color); err != nil {
			return b, err
		}
	}
	return b, tx.Commit(c)
}
func (s *Store) UpdateBoard(c context.Context, id, name string) error {
	_, err := s.DB.Exec(c, "UPDATE boards SET name=$2 WHERE id=$1", id, strings.TrimSpace(name))
	return err
}
func (s *Store) DeleteBoard(c context.Context, id string) error {
	_, err := s.DB.Exec(c, "DELETE FROM boards WHERE id=$1", id)
	return err
}
func (s *Store) GetBoard(c context.Context, id string) (domain.Board, error) {
	var b domain.Board
	e := s.DB.QueryRow(c, "SELECT id,name,created_at FROM boards WHERE id=$1", id).Scan(&b.ID, &b.Name, &b.CreatedAt)
	return b, e
}
func (s *Store) Columns(c context.Context, board string) ([]domain.Column, error) {
	r, e := s.DB.Query(c, "SELECT id,board_id,name,column_type,position,canvas_x,canvas_y,is_initial,is_terminal FROM workflow_columns WHERE board_id=$1 ORDER BY position,name", board)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.Column])
}
func (s *Store) Column(c context.Context, id string) (domain.Column, error) {
	var col domain.Column
	err := s.DB.QueryRow(c, "SELECT id,board_id,name,column_type,position,canvas_x,canvas_y,is_initial,is_terminal FROM workflow_columns WHERE id=$1", id).Scan(&col.ID, &col.BoardID, &col.Name, &col.Type, &col.Position, &col.CanvasX, &col.CanvasY, &col.IsInitial, &col.IsTerminal)
	return col, err
}
func normalizeColumnType(value string) (string, error) {
	typeName := strings.ToLower(strings.TrimSpace(value))
	if typeName == "" {
		typeName = "standard"
	}
	switch typeName {
	case "standard", "inbox", "done", "needs_action":
		return typeName, nil
	default:
		return "", errors.New("invalid column type")
	}
}

func (s *Store) AddColumn(c context.Context, board, name, typeName string) (domain.Column, error) {
	typeName, e := normalizeColumnType(typeName)
	if e != nil {
		return domain.Column{}, e
	}
	var x domain.Column
	if typeName == "standard" {
		var hasColumns bool
		if e = s.DB.QueryRow(c, "SELECT EXISTS(SELECT 1 FROM workflow_columns WHERE board_id=$1)", board).Scan(&hasColumns); e != nil {
			return x, e
		}
		if !hasColumns {
			typeName = "inbox"
		}
	}
	e = s.DB.QueryRow(c, `INSERT INTO workflow_columns(board_id,name,column_type,position,is_initial,is_terminal) VALUES($1,$2,$3,(SELECT COALESCE(max(position),-1)+1 FROM workflow_columns WHERE board_id=$1),$3='inbox',$3='done') RETURNING id,board_id,name,column_type,position,canvas_x,canvas_y,is_initial,is_terminal`, board, strings.TrimSpace(name), typeName).Scan(&x.ID, &x.BoardID, &x.Name, &x.Type, &x.Position, &x.CanvasX, &x.CanvasY, &x.IsInitial, &x.IsTerminal)
	return x, e
}
func (s *Store) UpdateColumn(c context.Context, id, name, typeName string, x, y int) error {
	typeName, e := normalizeColumnType(typeName)
	if e != nil {
		return e
	}
	tx, e := s.DB.Begin(c)
	if e != nil {
		return e
	}
	defer tx.Rollback(c)
	var board string
	if e = tx.QueryRow(c, "SELECT board_id FROM workflow_columns WHERE id=$1 FOR UPDATE", id).Scan(&board); e != nil {
		return e
	}
	if typeName != "standard" {
		var exists bool
		if e = tx.QueryRow(c, "SELECT EXISTS(SELECT 1 FROM workflow_columns WHERE board_id=$1 AND column_type=$2 AND id<>$3)", board, typeName, id).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return fmt.Errorf("board already has a %s column", typeName)
		}
	}
	if typeName == "inbox" {
		if _, e = tx.Exec(c, "UPDATE workflow_columns SET is_initial=false WHERE board_id=$1", board); e != nil {
			return e
		}
	}
	_, e = tx.Exec(c, "UPDATE workflow_columns SET name=$2,column_type=$3,is_initial=($3='inbox'),is_terminal=($3='done'),canvas_x=$4,canvas_y=$5 WHERE id=$1", id, strings.TrimSpace(name), typeName, x, y)
	if e != nil {
		return e
	}
	return tx.Commit(c)
}

func (s *Store) UpdateColumnPosition(c context.Context, id string, x, y int) error {
	_, err := s.DB.Exec(c, "UPDATE workflow_columns SET canvas_x=$2, canvas_y=$3 WHERE id=$1", id, x, y)
	return err
}
func (s *Store) DeleteColumn(c context.Context, id string) error {
	var n int
	if e := s.DB.QueryRow(c, "SELECT count(*) FROM tasks WHERE column_id=$1", id).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return errors.New("column still contains tasks")
	}
	_, e := s.DB.Exec(c, "DELETE FROM workflow_columns WHERE id=$1", id)
	return e
}
func (s *Store) Transitions(c context.Context, b string) ([]domain.Transition, error) {
	r, e := s.DB.Query(c, "SELECT id,board_id,from_column_id,to_column_id,action_name FROM transitions WHERE board_id=$1", b)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.Transition])
}
func (s *Store) AddTransition(c context.Context, b, from, to, action string) (domain.Transition, error) {
	var t domain.Transition
	e := s.DB.QueryRow(c, `INSERT INTO transitions(board_id,from_column_id,to_column_id,action_name)
		SELECT $1,$2,$3,$4
		WHERE EXISTS (SELECT 1 FROM workflow_columns WHERE id=$2 AND board_id=$1)
		  AND EXISTS (SELECT 1 FROM workflow_columns WHERE id=$3 AND board_id=$1)
		RETURNING id,board_id,from_column_id,to_column_id,action_name`, b, from, to, strings.TrimSpace(action)).Scan(&t.ID, &t.BoardID, &t.FromColumnID, &t.ToColumnID, &t.ActionName)
	if errors.Is(e, pgx.ErrNoRows) {
		return t, errors.New("transition columns must belong to this board")
	}
	return t, e
}
func (s *Store) DeleteTransition(c context.Context, id string) error {
	_, e := s.DB.Exec(c, "DELETE FROM transitions WHERE id=$1", id)
	return e
}
func (s *Store) UpdateTransition(c context.Context, id, from, to, action string) error {
	result, err := s.DB.Exec(c, `UPDATE transitions tr SET from_column_id=$2,to_column_id=$3,action_name=$4
		WHERE tr.id=$1
		  AND EXISTS (SELECT 1 FROM workflow_columns WHERE id=$2 AND board_id=tr.board_id)
		  AND EXISTS (SELECT 1 FROM workflow_columns WHERE id=$3 AND board_id=tr.board_id)`, id, from, to, strings.TrimSpace(action))
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return errors.New("transition columns must belong to this board")
	}
	return nil
}
func validPriority(priority string) bool {
	return priority == "low" || priority == "normal" || priority == "high" || priority == "urgent"
}
func dates(start, due string) (any, any, error) {
	var startVal, dueVal any
	var s, d time.Time
	var err error
	if start != "" {
		s, err = time.Parse("2006-01-02", start)
		if err != nil {
			return nil, nil, err
		}
		startVal = s
	}
	if due != "" {
		d, err = time.Parse("2006-01-02", due)
		if err != nil {
			return nil, nil, err
		}
		dueVal = d
	}
	if !s.IsZero() && !d.IsZero() && s.After(d) {
		return nil, nil, errors.New("start date must not be after due date")
	}
	return startVal, dueVal, nil
}
func (s *Store) CreateTask(c context.Context, b, title, desc, priority, start, due, source string) (domain.Task, error) {
	if strings.TrimSpace(title) == "" {
		return domain.Task{}, errors.New("task title is required")
	}
	if priority == "" {
		priority = "normal"
	}
	if !validPriority(priority) {
		return domain.Task{}, errors.New("invalid priority")
	}
	var col string
	e := s.DB.QueryRow(c, "SELECT id FROM workflow_columns WHERE board_id=$1 AND column_type='inbox'", b).Scan(&col)
	if e != nil {
		return domain.Task{}, errors.New("board has no initial column")
	}
	startVal, dueVal, e := dates(start, due)
	if e != nil {
		return domain.Task{}, e
	}
	tx, e := s.DB.Begin(c)
	if e != nil {
		return domain.Task{}, e
	}
	defer tx.Rollback(c)
	var t domain.Task
	e = tx.QueryRow(c, "INSERT INTO tasks(board_id,column_id,title,description,priority,start_date,due_date) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id,board_id,column_id,title,description,priority,start_date,due_date,completed_at,created_at", b, col, strings.TrimSpace(title), desc, priority, startVal, dueVal).Scan(&t.ID, &t.BoardID, &t.ColumnID, &t.Title, &t.Description, &t.Priority, &t.StartDate, &t.DueDate, &t.CompletedAt, &t.CreatedAt)
	if e == nil {
		_, e = tx.Exec(c, `INSERT INTO task_repository_targets(task_id,project_id,project_name,repository_url,default_branch,local_path,source_groups,target_source)
			SELECT $1,p.id,p.name,p.repository_url,p.default_branch,p.local_path,'[]'::jsonb,'inherited'
			FROM board_projects bp JOIN projects p ON p.id=bp.project_id
			WHERE bp.board_id=$2 AND (SELECT count(*) FROM board_projects WHERE board_id=$2)=1`, t.ID, b)
	}
	if e == nil {
		_, e = tx.Exec(c, "INSERT INTO task_transitions(task_id,to_column_id,source) VALUES($1,$2,$3)", t.ID, col, source)
	}
	if e == nil {
		_, e = tx.Exec(c, "INSERT INTO automation_events(type,task_id,board_id,payload) VALUES('task.created',$1,$2,jsonb_build_object('target_column_id',$3::text))", t.ID, b, col)
	}
	if e != nil {
		return t, e
	}
	return t, tx.Commit(c)
}
func (s *Store) Tasks(c context.Context, b string) ([]domain.Task, error) {
	r, e := s.DB.Query(c, `SELECT t.id,t.board_id,t.column_id,c.name,t.title,t.description,t.priority,t.start_date,t.due_date,t.completed_at,t.created_at,(c.column_type='done'),t.rework_count FROM tasks t JOIN workflow_columns c ON c.id=t.column_id WHERE t.board_id=$1 ORDER BY t.created_at DESC`, b)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	tasks := []domain.Task{}
	for r.Next() {
		var task domain.Task
		if e := r.Scan(&task.ID, &task.BoardID, &task.ColumnID, &task.ColumnName, &task.Title, &task.Description, &task.Priority, &task.StartDate, &task.DueDate, &task.CompletedAt, &task.CreatedAt, &task.IsTerminal, &task.ReworkCount); e != nil {
			return nil, e
		}
		tasks = append(tasks, task)
	}
	if err := r.Err(); err != nil {
		return nil, err
	}
	for i := range tasks {
		if tasks[i].Labels, e = s.TaskLabels(c, tasks[i].ID); e != nil {
			return nil, e
		}
	}
	return tasks, nil
}
func (s *Store) GetTask(c context.Context, id string) (domain.Task, error) {
	var t domain.Task
	e := s.DB.QueryRow(c, `SELECT t.id,t.board_id,t.column_id,c.name,t.title,t.description,t.priority,t.start_date,t.due_date,t.completed_at,t.created_at,(c.column_type='done'),t.rework_count FROM tasks t JOIN workflow_columns c ON c.id=t.column_id WHERE t.id=$1`, id).Scan(&t.ID, &t.BoardID, &t.ColumnID, &t.ColumnName, &t.Title, &t.Description, &t.Priority, &t.StartDate, &t.DueDate, &t.CompletedAt, &t.CreatedAt, &t.IsTerminal, &t.ReworkCount)
	if e != nil {
		return t, e
	}
	t.Labels, e = s.TaskLabels(c, t.ID)
	return t, e
}
func (s *Store) UpdateTask(c context.Context, id, title, description, priority, start, due string) error {
	if !validPriority(priority) {
		return errors.New("invalid priority")
	}
	startValue, dueValue, err := dates(start, due)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(c, "UPDATE tasks SET title=$2,description=$3,priority=$4,start_date=$5,due_date=$6,updated_at=now() WHERE id=$1", id, strings.TrimSpace(title), description, priority, startValue, dueValue)
	return err
}

// UpdateTaskWording is the deliberately narrow mutation available to the
// Triage Agent. It cannot alter priority, schedule, labels or workflow state.
func (s *Store) UpdateTaskWording(c context.Context, id, title, description string) error {
	return s.UpdateTaskWordingPartial(c, id, &title, &description)
}

// UpdateTaskWordingPartial atomically updates only the supplied, validated
// wording fields. Nil fields are deliberately left untouched.
func (s *Store) UpdateTaskWordingPartial(c context.Context, id string, title, description *string) error {
	if title == nil && description == nil {
		return errors.New("task title and description are required")
	}
	if title != nil {
		*title = strings.TrimSpace(*title)
		if *title == "" || *title == "…" || *title == "..." || len(*title) > 300 {
			return errors.New("invalid task title")
		}
	}
	if description != nil {
		*description = strings.TrimSpace(*description)
		if *description == "" || *description == "…" || *description == "..." || len(*description) > 12000 {
			return errors.New("invalid task description")
		}
	}
	var query string
	var args []any
	switch {
	case title != nil && description != nil:
		query = "UPDATE tasks SET title=$2,description=$3,updated_at=now() WHERE id=$1"
		args = []any{id, *title, *description}
	case title != nil:
		query = "UPDATE tasks SET title=$2,updated_at=now() WHERE id=$1"
		args = []any{id, *title}
	default:
		query = "UPDATE tasks SET description=$2,updated_at=now() WHERE id=$1"
		args = []any{id, *description}
	}
	_, err := s.DB.Exec(c, query, args...)
	return err
}
func (s *Store) DeleteTask(c context.Context, id string) error {
	_, err := s.DB.Exec(c, "DELETE FROM tasks WHERE id=$1", id)
	return err
}

// moveTaskTx is the single workflow transition primitive. Keeping it usable
// inside a larger transaction lets an interaction answer, its continuation
// run and an optional user-selected next step become one atomic hand-off.
func moveTaskTx(c context.Context, tx pgx.Tx, id, target, source string) error {
	return moveTaskTxWithPolicy(c, tx, id, target, source, false)
}

// moveTaskTxWithPolicy records an implicit transition only for a narrowly
// scoped human safety override. Agent and automation paths always pass false
// and therefore remain governed by the board's explicit graph.
func moveTaskTxWithPolicy(c context.Context, tx pgx.Tx, id, target, source string, allowImplicit bool) error {
	var current, board, currentName, targetName, transition string
	var currentPosition int
	err := tx.QueryRow(c, `SELECT t.column_id,t.board_id,c.name,c.position
		FROM tasks t JOIN workflow_columns c ON c.id=t.column_id WHERE t.id=$1 FOR UPDATE`, id).Scan(&current, &board, &currentName, &currentPosition)
	if err != nil {
		return err
	}
	err = tx.QueryRow(c, "SELECT id FROM transitions WHERE board_id=$1 AND from_column_id=$2 AND to_column_id=$3", board, current, target).Scan(&transition)
	if err != nil {
		if !allowImplicit || !errors.Is(err, pgx.ErrNoRows) {
			return errors.New("transition is not allowed")
		}
		transition = ""
	}
	var terminal bool
	var targetType string
	var targetPosition int
	err = tx.QueryRow(c, "SELECT name,column_type,position FROM workflow_columns WHERE id=$1 AND board_id=$2", target, board).Scan(&targetName, &targetType, &targetPosition)
	if err != nil {
		return err
	}
	terminal = targetType == "done"
	// Count a return generation exactly once at the transition that produced
	// it. Rule claims and multi-target deliveries must not multiply this.
	qaReturn := qaReviewReturn(currentName, currentPosition, targetType, targetPosition)
	incrementRework := explicitReworkTransition(source, qaReturn)
	_, err = tx.Exec(c, `UPDATE tasks SET column_id=$2,completed_at=CASE WHEN $3 THEN now() ELSE NULL END,rework_count=rework_count+(CASE WHEN $4 THEN 1 ELSE 0 END),updated_at=now() WHERE id=$1`, id, target, terminal, incrementRework)
	if err != nil {
		return err
	}
	if boardInheritanceEligibleColumn(targetName) {
		// Historical tasks may predate durable target snapshots. Inherit only
		// when the task has no explicit project/group target and its board has
		// exactly one project. The guards make retries and concurrent transitions
		// idempotent without ever guessing for ambiguous boards.
		if _, err = tx.Exec(c, `INSERT INTO task_repository_targets(task_id,project_id,project_name,repository_url,default_branch,local_path,source_groups,target_source)
			SELECT t.id,p.id,p.name,p.repository_url,p.default_branch,p.local_path,'[]'::jsonb,'inherited'
			FROM tasks t JOIN board_projects bp ON bp.board_id=t.board_id JOIN projects p ON p.id=bp.project_id
			WHERE t.id=$1
			  AND NOT EXISTS (SELECT 1 FROM task_target_projects WHERE task_id=t.id)
			  AND NOT EXISTS (SELECT 1 FROM task_target_groups WHERE task_id=t.id)
			  AND NOT EXISTS (SELECT 1 FROM task_repository_targets WHERE task_id=t.id)
			  AND (SELECT count(*) FROM board_projects WHERE board_id=t.board_id)=1
			ON CONFLICT (task_id,project_id) DO NOTHING`, id); err != nil {
			return err
		}
		if _, err = tx.Exec(c, `UPDATE agent_interactions SET status='cancelled',answered_at=now()
			WHERE task_id=$1 AND status='open' AND (decision_key ILIKE '%project%' OR decision_key ILIKE '%repository%' OR title ILIKE '%project%' OR title ILIKE '%repository%')
			  AND EXISTS (SELECT 1 FROM task_repository_targets WHERE task_id=$1 AND target_source='inherited')`, id); err != nil {
			return err
		}
	}
	// A new QA entry starts a new human release cycle. Keep the previous
	// decision for auditability, but prevent it from satisfying the next
	// cycle's release gate.
	if strings.EqualFold(strings.TrimSpace(targetName), "qa") {
		if _, err = tx.Exec(c, `UPDATE task_decisions
			SET superseded_at=now(), reopen_reason='Neuer QA-Zyklus nach Nacharbeit'
			WHERE task_id=$1 AND decision_key='qa_release' AND superseded_at IS NULL`, id); err != nil {
			return err
		}
	}
	var taskTransitionID string
	err = tx.QueryRow(c, `INSERT INTO task_transitions(task_id,from_column_id,to_column_id,transition_id,source)
		VALUES($1,$2,$3,$4,$5) RETURNING id`, id, current, target, transition, source).Scan(&taskTransitionID)
	if err != nil {
		return err
	}
	eventType := "task.entered_column"
	if terminal {
		eventType = "task.completed"
	}
	// A QA/review return is only automation-eligible when a previous delivery
	// really changed the repository and was applied. This flag is written in
	// the same transaction as the transition, making concurrent/replayed
	// deliveries deterministic and preventing no-op return loops. Forward
	// exits from Review/QA (Review→QA, QA→Done) are not returns.
	changeAvailable := false
	if qaReturn {
		// Bind the evidence to this concrete return generation. A task-wide
		// EXISTS check would let an old applied run resurrect later unchanged
		// QA/review returns indefinitely. Previous forward quality-chain
		// steps must not look like return generations either.
		err = tx.QueryRow(c, `SELECT EXISTS(
			SELECT 1 FROM agent_runs r
			WHERE r.task_id=$1 AND r.applied_at IS NOT NULL
			  AND r.applied_at > COALESCE((
				SELECT MAX(previous.occurred_at)
				FROM task_transitions previous
				JOIN workflow_columns previous_from ON previous_from.id=previous.from_column_id
				JOIN workflow_columns previous_to ON previous_to.id=previous.to_column_id
				WHERE previous.task_id=$1
				  AND previous.id<>$2
				  AND lower(trim(previous_from.name)) IN ('qa','review')
				  AND previous_to.column_type<>'done'
				  AND previous_from.position > previous_to.position
			  ), '-infinity'::timestamptz)
		)`, id, taskTransitionID).Scan(&changeAvailable)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(c, `INSERT INTO automation_events(type,task_id,board_id,payload)
		VALUES($1,$2,$3,jsonb_build_object('target_column_id',$4::text,'qa_return',$5::boolean,'change_available',$6::boolean,'return_generation',$7::text,'rework_requested',$8::boolean))`, eventType, id, board, target, qaReturn, changeAvailable, taskTransitionID, incrementRework)
	if err != nil {
		return err
	}
	if incrementRework {
		_, err = tx.Exec(c, `INSERT INTO audit_events(kind,resource_type,resource_id,metadata)
			VALUES('task.rework.incremented','task',$1,jsonb_build_object('reason','explicit_review_return','return_generation',$2::text,'source',$3::text))`, id, taskTransitionID, source)
	}
	return err
}

func (s *Store) MoveTask(c context.Context, id, target, source string) (domain.Task, error) {
	tx, err := s.DB.Begin(c)
	if err != nil {
		return domain.Task{}, err
	}
	defer tx.Rollback(c)
	if err = moveTaskTx(c, tx, id, target, source); err != nil {
		return domain.Task{}, err
	}
	if err = tx.Commit(c); err != nil {
		return domain.Task{}, err
	}
	return s.GetTask(c, id)
}

// MoveTaskToNamedColumn moves only when the board explicitly models the
// requested target and its transition is allowed. It never bypasses workflow
// policy merely because an automation observed an error.
// MoveTaskToColumnID resolves workflow transitions by stable column ID.
// A self-transition is an intentional, silent no-op.
func (s *Store) MoveTaskToColumnID(c context.Context, taskID, target, source string) (bool, error) {
	var current, board string
	if err := s.DB.QueryRow(c, "SELECT column_id,board_id FROM tasks WHERE id=$1", taskID).Scan(&current, &board); err != nil {
		return false, err
	}
	target = strings.TrimSpace(target)
	if target == current {
		return false, nil
	}
	var targetName string
	if err := s.DB.QueryRow(c, "SELECT name FROM workflow_columns WHERE id=$1 AND board_id=$2", target, board).Scan(&targetName); errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("unbekannte Zielspalten-ID %q; erlaubte Übergänge: %s", target, s.allowedTransitionDiagnosis(c, taskID))
	} else if err != nil {
		return false, err
	}
	if _, err := s.MoveTask(c, taskID, target, source); err != nil {
		if err.Error() == "transition is not allowed" {
			return false, fmt.Errorf("Transition zur Zielspalte %q (%s) ist nicht erlaubt; erlaubte Übergänge: %s", target, targetName, s.allowedTransitionDiagnosis(c, taskID))
		}
		return false, err
	}
	return true, nil
}

func (s *Store) allowedTransitionDiagnosis(c context.Context, taskID string) string {
	rows, err := s.DB.Query(c, `SELECT tr.to_column_id,c.name FROM transitions tr JOIN tasks t ON t.column_id=tr.from_column_id JOIN workflow_columns c ON c.id=tr.to_column_id WHERE t.id=$1 ORDER BY tr.to_column_id`, taskID)
	if err != nil {
		return "nicht verfügbar"
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var id, name string
		if rows.Scan(&id, &name) == nil {
			values = append(values, id+" ("+name+")")
		}
	}
	if len(values) == 0 {
		return "keine"
	}
	return strings.Join(values, ", ")
}

func (s *Store) MoveTaskToNamedColumn(c context.Context, taskID, name, source string) (bool, error) {
	name = strings.TrimSpace(name)
	var target string
	err := s.DB.QueryRow(c, `SELECT c.id FROM tasks t JOIN workflow_columns c ON c.board_id=t.board_id
		WHERE t.id=$1 AND lower(c.name)=lower($2) ORDER BY c.id LIMIT 1`, taskID, name).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.DB.QueryRow(c, `SELECT tr.to_column_id FROM tasks t
			JOIN transitions tr ON tr.board_id=t.board_id AND tr.from_column_id=t.column_id
			WHERE t.id=$1 AND lower(tr.action_name)=lower($2)
			ORDER BY tr.to_column_id LIMIT 1`, taskID, name).Scan(&target)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("unbekannte Zielspalte %q; erlaubte Übergänge: %s", name, s.allowedTransitionDiagnosis(c, taskID))
	}
	if err != nil {
		return false, err
	}
	return s.MoveTaskToColumnID(c, taskID, target, source)
}

// MoveTaskToColumnType moves a task only through an explicitly configured
// workflow transition. Agents use this semantic lookup, never display names.
func (s *Store) MoveTaskToColumnType(c context.Context, taskID, typeName, source string) (bool, error) {
	typeName, err := normalizeColumnType(typeName)
	if err != nil || typeName == "standard" {
		return false, errors.New("invalid target column type")
	}
	var target string
	err = s.DB.QueryRow(c, `SELECT c.id FROM tasks t JOIN workflow_columns c ON c.board_id=t.board_id
		WHERE t.id=$1 AND c.column_type=$2`, taskID, typeName).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = s.MoveTask(c, taskID, target, source); err != nil {
		if err.Error() == "transition is not allowed" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// MoveTaskToNeedsActionForHumanDecision is the deliberate exception to the
// workflow graph: a person may flag any task for attention even when its
// current column has no regular edge to the board's needs_action column. The
// transition is recorded with no transition_id, while normal actions and all
// agent paths remain strict.
func (s *Store) MoveTaskToNeedsActionForHumanDecision(c context.Context, taskID string) (bool, error) {
	var target string
	err := s.DB.QueryRow(c, `SELECT c.id FROM tasks t JOIN workflow_columns c ON c.board_id=t.board_id
		WHERE t.id=$1 AND c.column_type='needs_action'`, taskID).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tx, err := s.DB.Begin(c)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(c)
	if err = moveTaskTxWithPolicy(c, tx, taskID, target, "web", true); err != nil {
		return false, err
	}
	if err = tx.Commit(c); err != nil {
		return false, err
	}
	return true, nil
}
func (s *Store) Allowed(c context.Context, task string) ([]domain.Transition, error) {
	r, e := s.DB.Query(c, `SELECT tr.id,tr.board_id,tr.from_column_id,tr.to_column_id,tr.action_name FROM transitions tr JOIN tasks t ON t.column_id=tr.from_column_id WHERE t.id=$1`, task)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.Transition])
}
func (s *Store) History(c context.Context, task string) ([]domain.History, error) {
	r, e := s.DB.Query(c, `SELECT COALESCE(f.name,''),t.name,h.source,h.occurred_at FROM task_transitions h LEFT JOIN workflow_columns f ON f.id=h.from_column_id JOIN workflow_columns t ON t.id=h.to_column_id WHERE h.task_id=$1 ORDER BY h.occurred_at DESC`, task)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.History])
}
func (s *Store) Comments(c context.Context, task string) ([]domain.Comment, error) {
	r, err := s.DB.Query(c, "SELECT id,task_id,author,body,created_at FROM task_comments WHERE task_id=$1 ORDER BY created_at", task)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.Comment])
}
func (s *Store) AddComment(c context.Context, task, author, body string) error {
	if strings.TrimSpace(author) == "" {
		author = "Du"
	}
	_, err := s.DB.Exec(c, "INSERT INTO task_comments(task_id,author,body) VALUES($1,$2,$3)", task, strings.TrimSpace(author), strings.TrimSpace(body))
	return err
}
func (s *Store) CreateInteraction(c context.Context, task, agent, run, key, fingerprint, title, body string, schema []byte) (domain.AgentInteraction, error) {
	var i domain.AgentInteraction
	err := s.DB.QueryRow(c, `INSERT INTO agent_interactions(task_id,agent_id,agent_run_id,decision_key,fingerprint,title,body,schema)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (task_id,agent_id,fingerprint) WHERE status='open' AND fingerprint <> '' DO NOTHING
		RETURNING id,task_id,agent_id,COALESCE(agent_run_id::text,''),title,body,status,answered_by,schema,COALESCE(response,'null'::jsonb),created_at,answered_at,decision_key,fingerprint,COALESCE(continuation_run_id::text,'')`, task, agent, run, strings.TrimSpace(key), strings.TrimSpace(fingerprint), strings.TrimSpace(title), body, schema).
		Scan(&i.ID, &i.TaskID, &i.AgentID, &i.AgentRunID, &i.Title, &i.Body, &i.Status, &i.AnsweredBy, &i.Schema, &i.Response, &i.CreatedAt, &i.AnsweredAt, &i.DecisionKey, &i.Fingerprint, &i.ContinuationRunID)
	return i, err
}
func (s *Store) OpenInteractions(c context.Context, task string) ([]domain.AgentInteraction, error) {
	r, err := s.DB.Query(c, `SELECT i.id,i.task_id,i.agent_id,COALESCE(i.agent_run_id::text,''),i.title,i.body,i.status,i.answered_by,i.schema,COALESCE(i.response,'null'::jsonb),i.created_at,i.answered_at,i.decision_key,i.fingerprint,COALESCE(i.continuation_run_id::text,'')
		FROM agent_interactions i JOIN tasks t ON t.id=i.task_id JOIN workflow_columns c ON c.id=t.column_id
		WHERE i.task_id=$1 AND i.status='open' AND t.completed_at IS NULL AND c.column_type <> 'done' ORDER BY i.created_at`, task)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.AgentInteraction])
}

// Interaction returns the original schema before an answer is accepted. The
// web layer uses it to preserve compatibility with interaction buttons that
// were rendered before field identifiers were added to the client markup.
func (s *Store) Interaction(c context.Context, id string) (domain.AgentInteraction, error) {
	var i domain.AgentInteraction
	err := s.DB.QueryRow(c, `SELECT id,task_id,agent_id,COALESCE(agent_run_id::text,''),title,body,status,answered_by,schema,COALESCE(response,'null'::jsonb),created_at,answered_at,decision_key,fingerprint,COALESCE(continuation_run_id::text,'') FROM agent_interactions WHERE id=$1`, id).
		Scan(&i.ID, &i.TaskID, &i.AgentID, &i.AgentRunID, &i.Title, &i.Body, &i.Status, &i.AnsweredBy, &i.Schema, &i.Response, &i.CreatedAt, &i.AnsweredAt, &i.DecisionKey, &i.Fingerprint, &i.ContinuationRunID)
	return i, err
}
func (s *Store) AnswerInteraction(c context.Context, id, answerer string, response []byte) (domain.AgentInteraction, error) {
	var i domain.AgentInteraction
	err := s.DB.QueryRow(c, `UPDATE agent_interactions SET status='answered',response=$2,answered_by=$3,answered_at=now() WHERE id=$1 AND status='open'
		RETURNING id,task_id,agent_id,COALESCE(agent_run_id::text,''),title,body,status,answered_by,schema,COALESCE(response,'null'::jsonb),created_at,answered_at,decision_key,fingerprint,COALESCE(continuation_run_id::text,'')`, id, response, strings.TrimSpace(answerer)).
		Scan(&i.ID, &i.TaskID, &i.AgentID, &i.AgentRunID, &i.Title, &i.Body, &i.Status, &i.AnsweredBy, &i.Schema, &i.Response, &i.CreatedAt, &i.AnsweredAt, &i.DecisionKey, &i.Fingerprint, &i.ContinuationRunID)
	return i, err
}

// ResolveInteraction queues the continuation in the same transaction as the
// answer. A failed capacity or workspace check therefore leaves the question
// open and retryable instead of silently losing the hand-off.
func (s *Store) ResolveInteraction(c context.Context, id, answerer, freeform string, response []byte) (domain.AgentInteraction, []domain.AgentRun, error) {
	return s.ResolveInteractionAndMove(c, id, answerer, freeform, response, "")
}

// ResolveInteractionAndMove makes a human answer, a continuation run and an
// optional allowed workflow transition atomic. A capacity failure therefore
// leaves both the interaction and its task state unchanged and retryable.
func (s *Store) ResolveInteractionAndMove(c context.Context, id, answerer, freeform string, response []byte, targetColumnID string) (domain.AgentInteraction, []domain.AgentRun, error) {
	tx, err := s.DB.Begin(c)
	if err != nil {
		return domain.AgentInteraction{}, nil, err
	}
	defer tx.Rollback(c)
	var i domain.AgentInteraction
	err = tx.QueryRow(c, `SELECT id,task_id,agent_id,COALESCE(agent_run_id::text,''),title,body,status,answered_by,schema,COALESCE(response,'null'::jsonb),created_at,answered_at,decision_key,fingerprint,COALESCE(continuation_run_id::text,'') FROM agent_interactions WHERE id=$1 FOR UPDATE`, id).
		Scan(&i.ID, &i.TaskID, &i.AgentID, &i.AgentRunID, &i.Title, &i.Body, &i.Status, &i.AnsweredBy, &i.Schema, &i.Response, &i.CreatedAt, &i.AnsweredAt, &i.DecisionKey, &i.Fingerprint, &i.ContinuationRunID)
	if err != nil {
		return i, nil, err
	}
	if i.Status != "open" {
		return i, nil, errors.New("interaction is not open")
	}
	if i.DecisionKey == "qa_release" {
		var currentColumnID, boardID string
		if err = tx.QueryRow(c, `SELECT t.column_id,t.board_id FROM tasks t WHERE t.id=$1 FOR UPDATE`, i.TaskID).Scan(&currentColumnID, &boardID); err != nil {
			return i, nil, err
		}
		columns, columnsErr := workflowColumnsTx(c, tx, boardID)
		transitions, transitionsErr := workflowTransitionsFromTx(c, tx, boardID, currentColumnID)
		if columnsErr != nil || transitionsErr != nil {
			if columnsErr != nil {
				return i, nil, columnsErr
			}
			return i, nil, transitionsErr
		}
		qaTarget, qaErr := resolveQADecisionTarget(i.DecisionKey, response, currentColumnID, columns, transitions)
		if qaErr != nil {
			// QA answers own the route. Never fall back to an optional legacy
			// target when the semantic answer is invalid or ambiguous.
			return i, nil, qaErr
		}
		// The semantic QA decision owns the route. An optional legacy form
		// target must not turn "Überarbeiten" into a release or vice versa.
		targetColumnID = qaTarget
	}
	qaRework := i.DecisionKey == "qa_release" && qaDecisionIsRework(response)
	qaApprove := i.DecisionKey == "qa_release" && qaDecisionIsApproved(response)
	// Approval records the human release decision but deliberately keeps the
	// task in QA. The distinct Apply action performs repository integration and
	// only then advances the task to Done. Rework remains an immediate route
	// back to Development.
	if qaApprove {
		targetColumnID = ""
	}
	// A selected workflow step hands the task back to the workflow itself. Its
	// entered-column event will pick the appropriate specialist exactly once;
	// creating an additional manual continuation here would race that rule.
	if target := strings.TrimSpace(targetColumnID); target != "" || qaApprove {
		moveSource := "web"
		if qaRework {
			moveSource = "qa_rework"
		}
		if target != "" {
			if err = moveTaskTx(c, tx, i.TaskID, target, moveSource); err != nil {
				return i, nil, err
			}
		}
		key := i.DecisionKey
		if key == "" {
			key = "legacy:" + strings.ToLower(strings.ReplaceAll(strings.TrimSpace(i.Title), " ", "-"))
		}
		if _, err = tx.Exec(c, `INSERT INTO task_decisions(task_id,agent_id,interaction_id,decision_key,title,response,freeform_answer,resolved_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (task_id,agent_id,decision_key) WHERE superseded_at IS NULL DO UPDATE SET response=EXCLUDED.response,freeform_answer=EXCLUDED.freeform_answer,resolved_by=EXCLUDED.resolved_by,resolved_at=now(),interaction_id=EXCLUDED.interaction_id`, i.TaskID, i.AgentID, i.ID, key, i.Title, response, strings.TrimSpace(freeform), strings.TrimSpace(answerer)); err != nil {
			return i, nil, err
		}
		comment := "Antwort auf Agentenfrage „" + i.Title + "“:\n" + string(response)
		if strings.TrimSpace(freeform) != "" {
			comment += "\nEigene Antwort: " + strings.TrimSpace(freeform)
		}
		if _, err = tx.Exec(c, "INSERT INTO task_comments(task_id,author,body) VALUES($1,$2,$3)", i.TaskID, strings.TrimSpace(answerer), comment); err != nil {
			return i, nil, err
		}
		var answeredAt time.Time
		err = tx.QueryRow(c, `UPDATE agent_interactions SET status='answered',response=$2,answered_by=$3,answered_at=now(),continuation_run_id=NULL WHERE id=$1 AND status='open' RETURNING answered_at`, i.ID, response, strings.TrimSpace(answerer)).Scan(&answeredAt)
		if err != nil {
			return i, nil, err
		}
		i.Status, i.Response, i.AnsweredBy, i.AnsweredAt, i.ContinuationRunID = "answered", response, strings.TrimSpace(answerer), &answeredAt, ""
		if err = tx.Commit(c); err != nil {
			return i, nil, err
		}
		return i, nil, nil
	}
	var a domain.Agent
	err = tx.QueryRow(c, "SELECT id,name,description,adapter,prompt,prompt_prefix,prompt_suffix,enabled,max_parallel_runs,created_at FROM agents WHERE id=$1", i.AgentID).
		Scan(&a.ID, &a.Name, &a.Description, &a.Adapter, &a.Prompt, &a.PromptPrefix, &a.PromptSuffix, &a.Enabled, &a.MaxParallelRuns, &a.CreatedAt)
	if err != nil {
		return i, nil, err
	}
	if !a.Enabled {
		return i, nil, errors.New("agent is disabled")
	}
	targets, err := s.runTargets(c, i.TaskID)
	if err != nil {
		return i, nil, err
	}
	if len(targets) == 0 {
		return i, nil, ErrTargetSelectionRequired
	}
	targets, err = runRepositoryTargets(targets)
	if err != nil {
		return i, nil, err
	}
	if _, err = tx.Exec(c, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", i.TaskID+"|manual|"+a.ID); err != nil {
		return i, nil, err
	}
	var active bool
	if err = tx.QueryRow(c, `SELECT EXISTS(SELECT 1 FROM agent_run_batches WHERE task_id=$1 AND agent_id=$2 AND rule_id IS NULL AND status IN('queued','running'))`, i.TaskID, a.ID).Scan(&active); err != nil {
		return i, nil, err
	}
	if active {
		return i, nil, ErrTaskAgentActive
	}
	batch, err := createRunBatchTx(c, tx, i.TaskID, a.ID, "", "", len(targets))
	if err != nil {
		return i, nil, err
	}
	runs := make([]domain.AgentRun, 0, len(targets))
	for _, target := range targets {
		workspace := target.LocalPath
		if err = lockWorkspaceTx(c, tx, workspace); err != nil {
			return i, nil, err
		}
		run, created, createErr := createRunTx(c, tx, i.TaskID, a, a.ID, "", "", batch.ID, workspace, target.ProjectID)
		if createErr != nil {
			return i, nil, createErr
		}
		if !created {
			return i, nil, ErrNoRunCreated
		}
		runs = append(runs, run)
	}
	if len(runs) == 0 {
		return i, nil, ErrNoRunCreated
	}
	key := i.DecisionKey
	if key == "" {
		key = "legacy:" + strings.ToLower(strings.ReplaceAll(strings.TrimSpace(i.Title), " ", "-"))
	}
	if _, err = tx.Exec(c, `INSERT INTO task_decisions(task_id,agent_id,interaction_id,decision_key,title,response,freeform_answer,resolved_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (task_id,agent_id,decision_key) WHERE superseded_at IS NULL DO UPDATE SET response=EXCLUDED.response,freeform_answer=EXCLUDED.freeform_answer,resolved_by=EXCLUDED.resolved_by,resolved_at=now(),interaction_id=EXCLUDED.interaction_id`, i.TaskID, i.AgentID, i.ID, key, i.Title, response, strings.TrimSpace(freeform), strings.TrimSpace(answerer)); err != nil {
		return i, nil, err
	}
	comment := "Antwort auf Agentenfrage „" + i.Title + "“:\n" + string(response)
	if strings.TrimSpace(freeform) != "" {
		comment += "\nEigene Antwort: " + strings.TrimSpace(freeform)
	}
	if _, err = tx.Exec(c, "INSERT INTO task_comments(task_id,author,body) VALUES($1,$2,$3)", i.TaskID, strings.TrimSpace(answerer), comment); err != nil {
		return i, nil, err
	}
	var answeredAt time.Time
	err = tx.QueryRow(c, `UPDATE agent_interactions SET status='answered',response=$2,answered_by=$3,answered_at=now(),continuation_run_id=$4 WHERE id=$1 AND status='open' RETURNING answered_at`, i.ID, response, strings.TrimSpace(answerer), runs[0].ID).Scan(&answeredAt)
	if err != nil {
		return i, nil, err
	}
	i.Status = "answered"
	i.Response = response
	i.AnsweredBy = strings.TrimSpace(answerer)
	i.AnsweredAt = &answeredAt
	i.ContinuationRunID = runs[0].ID
	if err = tx.Commit(c); err != nil {
		return i, nil, err
	}
	return i, runs, nil
}

func workflowColumnsTx(c context.Context, tx pgx.Tx, boardID string) ([]domain.Column, error) {
	rows, err := tx.Query(c, "SELECT id,name,column_type FROM workflow_columns WHERE board_id=$1 ORDER BY position", boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := []domain.Column{}
	for rows.Next() {
		var column domain.Column
		if err := rows.Scan(&column.ID, &column.Name, &column.Type); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func workflowTransitionsFromTx(c context.Context, tx pgx.Tx, boardID, fromColumnID string) ([]domain.Transition, error) {
	rows, err := tx.Query(c, "SELECT id,board_id,from_column_id,to_column_id,action_name FROM transitions WHERE board_id=$1 AND from_column_id=$2", boardID, fromColumnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	transitions := []domain.Transition{}
	for rows.Next() {
		var transition domain.Transition
		if err := rows.Scan(&transition.ID, &transition.BoardID, &transition.FromColumnID, &transition.ToColumnID, &transition.ActionName); err != nil {
			return nil, err
		}
		transitions = append(transitions, transition)
	}
	return transitions, rows.Err()
}

func qaDecisionTarget(key string, response []byte, currentColumnID string, columns []domain.Column, transitions []domain.Transition) string {
	target, err := resolveQADecisionTarget(key, response, currentColumnID, columns, transitions)
	if err != nil {
		return ""
	}
	return target
}

func qaDecisionIsRework(response []byte) bool {
	var answers map[string][]string
	if json.Unmarshal(response, &answers) != nil || len(answers["release_decision"]) != 1 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(answers["release_decision"][0]), "rework")
}

func qaDecisionIsApproved(response []byte) bool {
	var answers map[string][]string
	if json.Unmarshal(response, &answers) != nil || len(answers["release_decision"]) != 1 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(answers["release_decision"][0]), "approve")
}

func resolveQADecisionTarget(key string, response []byte, currentColumnID string, columns []domain.Column, transitions []domain.Transition) (string, error) {
	if strings.TrimSpace(key) != "qa_release" || strings.TrimSpace(currentColumnID) == "" {
		return "", nil
	}
	var answers map[string][]string
	if json.Unmarshal(response, &answers) != nil || len(answers["release_decision"]) != 1 {
		return "", errors.New("invalid or ambiguous QA decision")
	}
	value := strings.ToLower(strings.TrimSpace(answers["release_decision"][0]))
	wantedType, wantedNames := "", map[string]bool{}
	switch value {
	case "approve":
		wantedType = "done"
	case "rework":
		wantedNames = map[string]bool{"in progress": true, "entwicklung": true, "development": true}
	default:
		return "", errors.New("invalid QA decision")
	}
	columnTypes := make(map[string]string, len(columns))
	columnNames := make(map[string]string, len(columns))
	for _, column := range columns {
		columnTypes[column.ID] = column.Type
		columnNames[column.ID] = strings.ToLower(strings.TrimSpace(column.Name))
	}
	target := ""
	for _, transition := range transitions {
		if transition.FromColumnID != currentColumnID || transition.ToColumnID == currentColumnID {
			continue
		}
		if (wantedType != "" && columnTypes[transition.ToColumnID] != wantedType) || (wantedType == "" && !wantedNames[columnNames[transition.ToColumnID]]) {
			continue
		}
		if target != "" {
			return "", errors.New("ambiguous QA transition")
		}
		target = transition.ToColumnID
	}
	if target == "" {
		return "", errors.New("QA transition is not configured")
	}
	return target, nil
}
func (s *Store) CreateLabel(c context.Context, b, n, color string) (domain.Label, error) {
	var l domain.Label
	e := s.DB.QueryRow(c, "INSERT INTO labels(board_id,name,color) VALUES($1,$2,$3) ON CONFLICT(board_id,name) DO UPDATE SET name=EXCLUDED.name RETURNING id,name,color", b, strings.TrimSpace(n), color).Scan(&l.ID, &l.Name, &l.Color)
	return l, e
}
func (s *Store) Labels(c context.Context, board string) ([]domain.Label, error) {
	r, err := s.DB.Query(c, "SELECT id,name,color FROM labels WHERE board_id=$1 ORDER BY name", board)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.Label])
}
func (s *Store) TaskLabels(c context.Context, task string) ([]domain.Label, error) {
	r, err := s.DB.Query(c, "SELECT l.id,l.name,l.color FROM labels l JOIN task_labels tl ON tl.label_id=l.id WHERE tl.task_id=$1 ORDER BY l.name", task)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.Label])
}
func (s *Store) SetLabels(c context.Context, task string, ids []string) error {
	tx, e := s.DB.Begin(c)
	if e != nil {
		return e
	}
	defer tx.Rollback(c)
	var board string
	if e = tx.QueryRow(c, "SELECT board_id FROM tasks WHERE id=$1 FOR UPDATE", task).Scan(&board); e != nil {
		return e
	}
	var classifications int
	if e = tx.QueryRow(c, `SELECT count(*) FROM labels WHERE board_id=$1 AND id=ANY($2)
		AND lower(name) IN ('bug','feature','enhancement')`, board, ids).Scan(&classifications); e != nil {
		return e
	}
	if classifications > 1 {
		return errors.New("a task may have only one classification label: Bug, Feature or Enhancement")
	}
	if _, e = tx.Exec(c, "DELETE FROM task_labels WHERE task_id=$1", task); e != nil {
		return e
	}
	for _, id := range ids {
		var valid bool
		if e = tx.QueryRow(c, "SELECT EXISTS(SELECT 1 FROM labels WHERE id=$1 AND board_id=$2)", id, board).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return errors.New("label does not belong to task board")
		}
		if _, e = tx.Exec(c, "INSERT INTO task_labels(task_id,label_id) VALUES($1,$2)", task, id); e != nil {
			return e
		}
	}
	return tx.Commit(c)
}
func (s *Store) String() string { return fmt.Sprintf("Store(%p)", s.DB) }

func (s *Store) SkillSources(c context.Context) ([]domain.SkillSource, error) {
	r, e := s.DB.Query(c, "SELECT id,name,repository_url,branch,enabled,created_at FROM skill_sources ORDER BY created_at DESC")
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.SkillSource])
}
func (s *Store) AddSkillSource(c context.Context, name, url, branch string) error {
	_, e := s.DB.Exec(c, "INSERT INTO skill_sources(name,repository_url,branch) VALUES($1,$2,$3)", strings.TrimSpace(name), strings.TrimSpace(url), defaultString(branch, "main"))
	return e
}

// EnsureSkillsSHSource gives skills.sh-installed skills one stable local
// provenance row without making the UI manage arbitrary Git repositories.
func (s *Store) EnsureSkillsSHSource(c context.Context) (domain.SkillSource, error) {
	var x domain.SkillSource
	err := s.DB.QueryRow(c, `SELECT id,name,repository_url,branch,enabled,created_at
		FROM skill_sources WHERE repository_url='https://skills.sh' ORDER BY created_at LIMIT 1`).
		Scan(&x.ID, &x.Name, &x.RepositoryURL, &x.Branch, &x.Enabled, &x.CreatedAt)
	if err == nil {
		return x, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return x, err
	}
	err = s.DB.QueryRow(c, `INSERT INTO skill_sources(name,repository_url,branch)
		VALUES('skills.sh','https://skills.sh','catalog')
		RETURNING id,name,repository_url,branch,enabled,created_at`).
		Scan(&x.ID, &x.Name, &x.RepositoryURL, &x.Branch, &x.Enabled, &x.CreatedAt)
	return x, err
}
func (s *Store) DeleteSkillSource(c context.Context, id string) error {
	_, err := s.DB.Exec(c, "DELETE FROM skill_sources WHERE id=$1", id)
	return err
}
func (s *Store) GetSkillSource(c context.Context, id string) (domain.SkillSource, error) {
	var x domain.SkillSource
	e := s.DB.QueryRow(c, "SELECT id,name,repository_url,branch,enabled,created_at FROM skill_sources WHERE id=$1", id).Scan(&x.ID, &x.Name, &x.RepositoryURL, &x.Branch, &x.Enabled, &x.CreatedAt)
	return x, e
}
func (s *Store) UpsertSkill(c context.Context, source, name, desc, path string) (domain.Skill, error) {
	var x domain.Skill
	e := s.DB.QueryRow(c, "INSERT INTO skills(source_id,name,description,repository_path) VALUES($1,$2,$3,$4) ON CONFLICT(source_id,repository_path) DO UPDATE SET name=EXCLUDED.name,description=EXCLUDED.description RETURNING id,source_id,name,description,repository_path", source, name, desc, path).Scan(&x.ID, &x.SourceID, &x.Name, &x.Description, &x.RepositoryPath)
	return x, e
}
func (s *Store) Skills(c context.Context) ([]domain.Skill, error) {
	r, e := s.DB.Query(c, "SELECT id,source_id,name,description,repository_path FROM skills ORDER BY name")
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.Skill])
}
func (s *Store) InstallSkill(c context.Context, skill, path, sha string) (domain.InstalledSkill, error) {
	var x domain.InstalledSkill
	e := s.DB.QueryRow(c, "INSERT INTO installed_skills(skill_id,install_path,commit_sha) VALUES($1,$2,$3) ON CONFLICT(skill_id) DO UPDATE SET install_path=EXCLUDED.install_path,commit_sha=EXCLUDED.commit_sha,status='installed',installed_at=now() RETURNING id,skill_id,'','', '',install_path,commit_sha,status,installed_at", skill, path, sha).Scan(&x.ID, &x.SkillID, &x.Name, &x.Description, &x.RepositoryPath, &x.InstallPath, &x.CommitSHA, &x.Status, &x.InstalledAt)
	return x, e
}
func (s *Store) GetSkill(c context.Context, id string) (domain.Skill, error) {
	var x domain.Skill
	e := s.DB.QueryRow(c, "SELECT id,source_id,name,description,repository_path FROM skills WHERE id=$1", id).Scan(&x.ID, &x.SourceID, &x.Name, &x.Description, &x.RepositoryPath)
	return x, e
}
func (s *Store) InstalledSkills(c context.Context) ([]domain.InstalledSkill, error) {
	r, e := s.DB.Query(c, `SELECT i.id,s.id,s.name,s.description,s.repository_path,i.install_path,i.commit_sha,i.status,i.installed_at FROM installed_skills i JOIN skills s ON s.id=i.skill_id ORDER BY s.name`)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.InstalledSkill])
}
func (s *Store) UninstallSkill(c context.Context, installedID string) error {
	tx, err := s.DB.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if _, err = tx.Exec(c, "DELETE FROM agent_skills WHERE installed_skill_id=$1", installedID); err != nil {
		return err
	}
	if _, err = tx.Exec(c, "DELETE FROM installed_skills WHERE id=$1", installedID); err != nil {
		return err
	}
	return tx.Commit(c)
}
func defaultString(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
func (s *Store) Agents(c context.Context) ([]domain.Agent, error) {
	r, e := s.DB.Query(c, "SELECT id,name,description,adapter,prompt,prompt_prefix,prompt_suffix,model,reasoning_effort,escalation_policy::text,COALESCE(sandbox_profile,'strict'),enabled,max_parallel_runs,created_at FROM agents WHERE retired_at IS NULL ORDER BY name")
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.Agent])
}
func (s *Store) GetAgent(c context.Context, id string) (domain.Agent, error) {
	var a domain.Agent
	e := s.DB.QueryRow(c, "SELECT id,name,description,adapter,prompt,prompt_prefix,prompt_suffix,model,reasoning_effort,escalation_policy::text,COALESCE(sandbox_profile,'strict'),enabled,max_parallel_runs,created_at FROM agents WHERE id=$1", id).Scan(&a.ID, &a.Name, &a.Description, &a.Adapter, &a.Prompt, &a.PromptPrefix, &a.PromptSuffix, &a.Model, &a.ReasoningEffort, &a.EscalationPolicy, &a.SandboxProfile, &a.Enabled, &a.MaxParallelRuns, &a.CreatedAt)
	return a, e
}
func (s *Store) CreateAgent(c context.Context, name, desc, prefix, prompt, suffix string, max int) (domain.Agent, error) {
	var a domain.Agent
	if max < 1 {
		max = 1
	}
	e := s.DB.QueryRow(c, "INSERT INTO agents(name,description,prompt_prefix,prompt,prompt_suffix,max_parallel_runs) VALUES($1,$2,$3,$4,$5,$6) RETURNING id,name,description,adapter,prompt,prompt_prefix,prompt_suffix,model,reasoning_effort,escalation_policy::text,COALESCE(sandbox_profile,'strict'),enabled,max_parallel_runs,created_at", strings.TrimSpace(name), desc, prefix, prompt, suffix, max).Scan(&a.ID, &a.Name, &a.Description, &a.Adapter, &a.Prompt, &a.PromptPrefix, &a.PromptSuffix, &a.Model, &a.ReasoningEffort, &a.EscalationPolicy, &a.SandboxProfile, &a.Enabled, &a.MaxParallelRuns, &a.CreatedAt)
	return a, e
}
func (s *Store) UpdateAgent(c context.Context, id, name, desc, prefix, prompt, suffix string, max int, enabled bool) error {
	if max < 1 {
		max = 1
	}
	_, err := s.DB.Exec(c, "UPDATE agents SET name=$2,description=$3,prompt_prefix=$4,prompt=$5,prompt_suffix=$6,max_parallel_runs=$7,enabled=$8 WHERE id=$1 AND retired_at IS NULL", id, strings.TrimSpace(name), desc, prefix, prompt, suffix, max, enabled)
	return err
}

func (s *Store) UpdateAgentAdapter(c context.Context, id, adapter string) error {
	adapter = strings.TrimSpace(adapter)
	if adapter == "" {
		adapter = "codex"
	}
	_, err := s.DB.Exec(c, "UPDATE agents SET adapter=$2 WHERE id=$1 AND retired_at IS NULL", id, adapter)
	return err
}

func (s *Store) SandboxProfiles(c context.Context) ([]sandbox.Profile, error) {
	rows, err := s.DB.Query(c, `SELECT name,description,mounts,network_mode,write_mode,active FROM sandbox_profiles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []sandbox.Profile
	for rows.Next() {
		var p sandbox.Profile
		var mounts []byte
		if err := rows.Scan(&p.Name, &p.Description, &mounts, &p.NetworkMode, &p.WriteMode, &p.Active); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(mounts, &p.Mounts); err != nil {
			return nil, err
		}
		if err := sandbox.ValidateProfile(p); err != nil {
			return nil, fmt.Errorf("sandbox profile %q is invalid: %w", p.Name, err)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *Store) SandboxProfile(c context.Context, name string) (sandbox.Profile, error) {
	var p sandbox.Profile
	var mounts []byte
	err := s.DB.QueryRow(c, `SELECT name,description,mounts,network_mode,write_mode,active FROM sandbox_profiles WHERE name=$1`, strings.TrimSpace(name)).Scan(&p.Name, &p.Description, &mounts, &p.NetworkMode, &p.WriteMode, &p.Active)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(mounts, &p.Mounts); err != nil {
		return p, err
	}
	if err := sandbox.ValidateProfile(p); err != nil {
		return p, err
	}
	return p, nil
}

func (s *Store) CreateSandboxProfile(c context.Context, p sandbox.Profile) error {
	if err := sandbox.ValidateProfile(p); err != nil {
		return err
	}
	mounts, _ := json.Marshal(p.Mounts)
	_, err := s.DB.Exec(c, `INSERT INTO sandbox_profiles(name,description,mounts,network_mode,write_mode,active) VALUES($1,$2,$3,$4,$5,$6)`, p.Name, p.Description, mounts, p.NetworkMode, p.WriteMode, p.Active)
	return err
}

func (s *Store) UpdateSandboxProfile(c context.Context, p sandbox.Profile) error {
	if err := sandbox.ValidateProfile(p); err != nil {
		return err
	}
	mounts, _ := json.Marshal(p.Mounts)
	_, err := s.DB.Exec(c, `UPDATE sandbox_profiles SET description=$2,mounts=$3,network_mode=$4,write_mode=$5,active=$6 WHERE name=$1`, p.Name, p.Description, mounts, p.NetworkMode, p.WriteMode, p.Active)
	return err
}

func (s *Store) UpdateAgentSandboxProfile(c context.Context, agentID, profile string) error {
	p, err := s.SandboxProfile(c, profile)
	if err != nil || !p.Active {
		return errors.New("sandbox profile is unknown or inactive")
	}
	var active bool
	if err = s.DB.QueryRow(c, `SELECT EXISTS(SELECT 1 FROM agent_runs WHERE agent_id=$1 AND status IN ('queued','running'))`, agentID).Scan(&active); err != nil {
		return err
	}
	if active {
		return errors.New("sandbox profile changes apply only to new runs")
	}
	_, err = s.DB.Exec(c, `UPDATE agents SET sandbox_profile=$2 WHERE id=$1 AND retired_at IS NULL`, agentID, p.Name)
	return err
}

func (s *Store) RunSandboxPolicy(c context.Context, runID string) (sandbox.Profile, error) {
	var raw []byte
	var name string
	if err := s.DB.QueryRow(c, `SELECT sandbox_profile,sandbox_effective FROM agent_runs WHERE id=$1`, runID).Scan(&name, &raw); err != nil {
		return sandbox.Profile{}, err
	}
	var p sandbox.Profile
	if len(raw) > 0 && json.Unmarshal(raw, &p) == nil && p.Name == name {
		return sandbox.EffectiveProfile(p, "/workspace")
	}
	return s.SandboxProfile(c, name)
}

func (s *Store) UpdateAgentSelection(c context.Context, id, model, effort, policy string) error {
	if strings.TrimSpace(model) == "" || !validReasoningEffort(effort) {
		return errors.New("Agent benötigt eine gültige Modell- und Effort-Auswahl")
	}
	raw := defaultString(strings.TrimSpace(policy), "{}")
	parsed, err := parseEscalationPolicyFields(raw)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if _, err = tx.Exec(c, "UPDATE agents SET model=$2,reasoning_effort=$3,escalation_policy=$4::jsonb WHERE id=$1 AND retired_at IS NULL", id, strings.TrimSpace(model), strings.TrimSpace(effort), raw); err != nil {
		return err
	}
	if parsed.persistState {
		var currentBudget int64
		var currentHuman int
		var currentVersion string
		scanErr := tx.QueryRow(c, `SELECT version,human_escalation_after,budget_limit_microusd FROM task_rework_policies WHERE id=TRUE`).
			Scan(&currentVersion, &currentHuman, &currentBudget)
		if scanErr != nil && !errors.Is(scanErr, pgx.ErrNoRows) {
			return scanErr
		}
		if !parsed.hasBudget {
			parsed.budget = currentBudget
		}
		if !parsed.hasHuman {
			parsed.humanEscalationAfter = currentHuman
		}
		if parsed.version == "rework-v1" && strings.TrimSpace(currentVersion) != "" && fieldsVersionUnspecified(raw) {
			parsed.version = currentVersion
		}
		if _, err = tx.Exec(c, `INSERT INTO task_rework_policies(id,version,policy,human_escalation_after,budget_limit_microusd,updated_at)
			VALUES(TRUE,$1,$2::jsonb,$3,$4,now())
			ON CONFLICT (id) DO UPDATE SET version=EXCLUDED.version, policy=EXCLUDED.policy, human_escalation_after=EXCLUDED.human_escalation_after, budget_limit_microusd=EXCLUDED.budget_limit_microusd, updated_at=now()`,
			parsed.version, raw, parsed.humanEscalationAfter, parsed.budget); err != nil {
			return err
		}
	}
	return tx.Commit(c)
}

type parsedEscalationPolicy struct {
	version              string
	humanEscalationAfter int
	budget               int64
	hasBudget            bool
	hasHuman             bool
	persistState         bool
}

func fieldsVersionUnspecified(raw string) bool {
	var fields map[string]any
	if json.Unmarshal([]byte(raw), &fields) != nil {
		return true
	}
	_, ok := fields["version"]
	return !ok
}

func parseEscalationPolicyFields(raw string) (parsedEscalationPolicy, error) {
	parsed := parsedEscalationPolicy{version: "rework-v1", humanEscalationAfter: 7}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return parsed, errors.New("Eskalationspolicy muss gültiges JSON sein")
	}
	if version, ok := fields["version"].(string); ok && strings.TrimSpace(version) != "" {
		parsed.version = strings.TrimSpace(version)
	}
	if _, exists := fields["human_escalation_after"]; exists {
		human, ok := jsonNonNegativeInt(fields["human_escalation_after"])
		if !ok {
			return parsed, errors.New("human_escalation_after muss eine nicht-negative ganze Zahl sein")
		}
		parsed.humanEscalationAfter = int(human)
		parsed.hasHuman = true
	}
	if _, exists := fields["budget_microusd"]; exists {
		budget, ok := jsonNonNegativeInt(fields["budget_microusd"])
		if !ok {
			return parsed, errors.New("budget_microusd muss eine nicht-negative ganze Zahl sein")
		}
		parsed.budget = budget
		parsed.hasBudget = true
		parsed.persistState = true
	}
	if rawTariffs, exists := fields["estimated_cost_microusd"]; exists {
		tariffs, ok := rawTariffs.(map[string]any)
		if !ok {
			return parsed, errors.New("estimated_cost_microusd muss ein Objekt mit nicht-negativen Kosten sein")
		}
		for choice, rawCost := range tariffs {
			_, valid := jsonNonNegativeInt(rawCost)
			if strings.TrimSpace(choice) == "" || !valid {
				return parsed, fmt.Errorf("ungültiger Kostentarif %q", choice)
			}
		}
		parsed.persistState = true
	}
	return parsed, nil
}

func jsonNonNegativeInt(value any) (int64, bool) {
	switch n := value.(type) {
	case float64:
		if n < 0 || n != float64(int64(n)) {
			return 0, false
		}
		return int64(n), true
	case int:
		if n < 0 {
			return 0, false
		}
		return int64(n), true
	case int64:
		if n < 0 {
			return 0, false
		}
		return n, true
	case json.Number:
		parsed, err := n.Int64()
		if err != nil || parsed < 0 {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func validReasoningEffort(value string) bool {
	switch strings.TrimSpace(value) {
	case "low", "medium", "high", "xhigh":
		return true
	}
	return false
}

func (s *Store) AgentPromptPolicy(c context.Context) (string, string, error) {
	var prefix, suffix string
	err := s.DB.QueryRow(c, "SELECT prompt_prefix,prompt_suffix FROM agent_prompt_policy WHERE id=true").Scan(&prefix, &suffix)
	return prefix, suffix, err
}

func (s *Store) UpdateAgentPromptPolicy(c context.Context, prefix, suffix string) error {
	_, err := s.DB.Exec(c, `INSERT INTO agent_prompt_policy(id,prompt_prefix,prompt_suffix,updated_at) VALUES(true,$1,$2,now())
		ON CONFLICT (id) DO UPDATE SET prompt_prefix=EXCLUDED.prompt_prefix,prompt_suffix=EXCLUDED.prompt_suffix,updated_at=now()`, strings.TrimSpace(prefix), strings.TrimSpace(suffix))
	return err
}

func (s *Store) TaskDecisions(c context.Context, taskID string) ([]domain.TaskDecision, error) {
	rows, err := s.DB.Query(c, `SELECT id,task_id,agent_id,COALESCE(interaction_id::text,''),decision_key,title,response,freeform_answer,resolved_by,resolved_at,reopen_reason FROM task_decisions WHERE task_id=$1 AND superseded_at IS NULL ORDER BY resolved_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.TaskDecision])
}

func (s *Store) HasTaskDecision(c context.Context, taskID, agentID, key string) (bool, error) {
	var exists bool
	err := s.DB.QueryRow(c, `SELECT EXISTS(SELECT 1 FROM task_decisions WHERE task_id=$1 AND agent_id=$2 AND decision_key=$3 AND superseded_at IS NULL)`, taskID, agentID, strings.TrimSpace(key)).Scan(&exists)
	return exists, err
}

func (s *Store) RecordTaskDecision(c context.Context, interaction domain.AgentInteraction, answerer, freeform string, response []byte) error {
	key := interaction.DecisionKey
	if key == "" {
		key = "legacy:" + strings.ToLower(strings.ReplaceAll(strings.TrimSpace(interaction.Title), " ", "-"))
	}
	_, err := s.DB.Exec(c, `INSERT INTO task_decisions(task_id,agent_id,interaction_id,decision_key,title,response,freeform_answer,resolved_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (task_id,agent_id,decision_key) WHERE superseded_at IS NULL
		DO UPDATE SET response=EXCLUDED.response,freeform_answer=EXCLUDED.freeform_answer,resolved_by=EXCLUDED.resolved_by,resolved_at=now(),interaction_id=EXCLUDED.interaction_id`, interaction.TaskID, interaction.AgentID, interaction.ID, key, interaction.Title, response, strings.TrimSpace(freeform), strings.TrimSpace(answerer))
	return err
}
func (s *Store) DeleteAgent(c context.Context, id string) error {
	tx, err := s.DB.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	// Retire instead of physically deleting: agent_runs intentionally retain
	// their agent FK so historical traces and cost attribution remain intact.
	var retired bool
	if err = tx.QueryRow(c, "UPDATE agents SET enabled=false,retired_at=COALESCE(retired_at,now()) WHERE id=$1 RETURNING true", id).Scan(&retired); err != nil {
		return err
	}
	// A retired profile must not retain secret access or skill assignments.
	// Older installations may not have the optional secrets schema yet.
	var secretTable *string
	if err = tx.QueryRow(c, "SELECT to_regclass('public.secret_agents')").Scan(&secretTable); err != nil {
		return err
	}
	if secretTable != nil {
		if _, err = tx.Exec(c, "DELETE FROM secret_agents WHERE agent_id=$1", id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(c, "DELETE FROM agent_skills WHERE agent_id=$1", id); err != nil {
		return err
	}
	return tx.Commit(c)
}
func (s *Store) SetAgentSkills(c context.Context, agentID string, installedSkillIDs []string) error {
	tx, err := s.DB.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if _, err = tx.Exec(c, "DELETE FROM agent_skills WHERE agent_id=$1", agentID); err != nil {
		return err
	}
	for _, skillID := range installedSkillIDs {
		if _, err = tx.Exec(c, "INSERT INTO agent_skills(agent_id,installed_skill_id) VALUES($1,$2) ON CONFLICT DO NOTHING", agentID, skillID); err != nil {
			return err
		}
	}
	return tx.Commit(c)
}
func (s *Store) AgentSkills(c context.Context, agentID string) ([]domain.InstalledSkill, error) {
	r, err := s.DB.Query(c, `SELECT i.id,s.id,s.name,s.description,s.repository_path,i.install_path,i.commit_sha,i.status,i.installed_at
		FROM agent_skills x JOIN installed_skills i ON i.id=x.installed_skill_id JOIN skills s ON s.id=i.skill_id WHERE x.agent_id=$1 ORDER BY s.name`, agentID)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.InstalledSkill])
}
func (s *Store) Rules(c context.Context) ([]domain.AutomationRule, error) {
	r, e := s.DB.Query(c, "SELECT "+automationRuleSelect+" FROM automation_rules ORDER BY created_at DESC")
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.AutomationRule])
}
func (s *Store) UpdateRuleWithActionsAndLabelAndDelivery(c context.Context, id, name, board, trigger, target, label, agent, success, failure string, requireDeliveryApproval bool) error {
	if err := s.validateRule(c, board, trigger, target, label, agent, success, failure); err != nil {
		return err
	}
	tag, err := s.DB.Exec(c, `UPDATE automation_rules SET name=$2,board_id=NULLIF($3,'')::uuid,trigger_type=$4,target_column_id=NULLIF($5,'')::uuid,label_id=NULLIF($6,'')::uuid,agent_id=NULLIF($7,'')::uuid,success_column_id=NULLIF($8,'')::uuid,failure_column_id=NULLIF($9,'')::uuid,require_delivery_approval=$10 WHERE id=$1`, id, strings.TrimSpace(name), board, trigger, target, label, agent, success, failure, requireDeliveryApproval)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("automation rule not found")
	}
	return nil
}

// UpdateConfiguredRule persists every editable rule setting in a single SQL
// update so a request cannot leave routing changed while timing did not save.
func (s *Store) UpdateConfiguredRule(c context.Context, id, name, board, trigger, target, label, agent, success, failure string, requireDeliveryApproval bool, every, within, cooldown int) error {
	if err := s.validateRule(c, board, trigger, target, label, agent, success, failure); err != nil {
		return err
	}
	tag, err := s.DB.Exec(c, `UPDATE automation_rules SET name=$2,board_id=NULLIF($3,'')::uuid,trigger_type=$4,target_column_id=NULLIF($5,'')::uuid,label_id=NULLIF($6,'')::uuid,agent_id=NULLIF($7,'')::uuid,success_column_id=NULLIF($8,'')::uuid,failure_column_id=NULLIF($9,'')::uuid,require_delivery_approval=$10,schedule_every_minutes=NULLIF($11,0),due_within_hours=NULLIF($12,0),cooldown_minutes=$13 WHERE id=$1`, id, strings.TrimSpace(name), board, trigger, target, label, agent, success, failure, requireDeliveryApproval, every, within, cooldown)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("automation rule not found")
	}
	return nil
}
func (s *Store) CreateRule(c context.Context, name, board, trigger, target, agent string) (domain.AutomationRule, error) {
	return s.CreateRuleWithActions(c, name, board, trigger, target, agent, "", "")
}
func (s *Store) CreateRuleWithActions(c context.Context, name, board, trigger, target, agent, success, failure string) (domain.AutomationRule, error) {
	return s.CreateRuleWithActionsAndLabel(c, name, board, trigger, target, "", agent, success, failure)
}
func (s *Store) CreateRuleWithActionsAndLabel(c context.Context, name, board, trigger, target, label, agent, success, failure string) (domain.AutomationRule, error) {
	return s.CreateRuleWithActionsAndLabelAndDelivery(c, name, board, trigger, target, label, agent, success, failure, true)
}
func (s *Store) CreateRuleWithActionsAndLabelAndDelivery(c context.Context, name, board, trigger, target, label, agent, success, failure string, requireDeliveryApproval bool) (domain.AutomationRule, error) {
	if err := s.validateRule(c, board, trigger, target, label, agent, success, failure); err != nil {
		return domain.AutomationRule{}, err
	}
	var r domain.AutomationRule
	err := s.DB.QueryRow(c, "INSERT INTO automation_rules(name,board_id,trigger_type,target_column_id,label_id,agent_id,success_column_id,failure_column_id,require_delivery_approval) VALUES($1,NULLIF($2,'')::uuid,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,NULLIF($8,'')::uuid,$9) RETURNING id,name,COALESCE(board_id::text,''),trigger_type,COALESCE(target_column_id::text,''),COALESCE(label_id::text,''),COALESCE(agent_id::text,''),COALESCE(success_column_id::text,''),COALESCE(failure_column_id::text,''),enabled,require_delivery_approval,created_at", name, board, trigger, target, label, agent, success, failure, requireDeliveryApproval).Scan(&r.ID, &r.Name, &r.BoardID, &r.TriggerType, &r.TargetColumnID, &r.LabelID, &r.AgentID, &r.SuccessColumnID, &r.FailureColumnID, &r.Enabled, &r.RequireDeliveryApproval, &r.CreatedAt)
	return r, err
}

// CreateConfiguredRule writes the whole configuration at once. Callers have
// already validated timing, while validateRule below enforces workflow safety.
func (s *Store) CreateConfiguredRule(c context.Context, name, board, trigger, target, label, agent, success, failure string, requireDeliveryApproval bool, every, within, cooldown int) (domain.AutomationRule, error) {
	if err := s.validateRule(c, board, trigger, target, label, agent, success, failure); err != nil {
		return domain.AutomationRule{}, err
	}
	var r domain.AutomationRule
	err := s.DB.QueryRow(c, `INSERT INTO automation_rules(name,board_id,trigger_type,target_column_id,label_id,agent_id,success_column_id,failure_column_id,require_delivery_approval,schedule_every_minutes,due_within_hours,cooldown_minutes) VALUES($1,NULLIF($2,'')::uuid,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,NULLIF($8,'')::uuid,$9,NULLIF($10,0),NULLIF($11,0),$12) RETURNING id,name,COALESCE(board_id::text,''),trigger_type,COALESCE(target_column_id::text,''),COALESCE(label_id::text,''),COALESCE(agent_id::text,''),COALESCE(success_column_id::text,''),COALESCE(failure_column_id::text,''),enabled,require_delivery_approval,created_at`, name, board, trigger, target, label, agent, success, failure, requireDeliveryApproval, every, within, cooldown).Scan(&r.ID, &r.Name, &r.BoardID, &r.TriggerType, &r.TargetColumnID, &r.LabelID, &r.AgentID, &r.SuccessColumnID, &r.FailureColumnID, &r.Enabled, &r.RequireDeliveryApproval, &r.CreatedAt)
	return r, err
}

// validateRule keeps lifecycle automations executable rather than merely
// referentially valid. A foreign key alone cannot prove that columns belong to
// the selected board or that an outcome can be reached from the trigger state.
func (s *Store) validateRule(c context.Context, board, trigger, target, label, agent, success, failure string) error {
	if trigger != "task.created" && trigger != "task.entered_column" && trigger != "task.completed" && trigger != "task.due_soon" {
		return errors.New("unknown automation trigger")
	}
	var active bool
	if err := s.DB.QueryRow(c, "SELECT enabled FROM agents WHERE id=$1", agent).Scan(&active); err != nil {
		return err
	}
	if !active {
		return errors.New("agent is disabled")
	}
	if board == "" {
		if target != "" || label != "" || success != "" || failure != "" {
			return errors.New("column-based automation requires a board")
		}
		return nil
	}
	var boardExists bool
	if err := s.DB.QueryRow(c, "SELECT EXISTS(SELECT 1 FROM boards WHERE id=$1)", board).Scan(&boardExists); err != nil {
		return err
	}
	if !boardExists {
		return errors.New("automation board does not exist")
	}
	if label != "" {
		var belongs bool
		if err := s.DB.QueryRow(c, "SELECT EXISTS(SELECT 1 FROM labels WHERE id=$1 AND board_id=$2)", label, board).Scan(&belongs); err != nil {
			return err
		}
		if !belongs {
			return errors.New("automation label does not belong to its board")
		}
	}
	for _, columnID := range []string{target, success, failure} {
		if columnID == "" {
			continue
		}
		var belongs bool
		if err := s.DB.QueryRow(c, "SELECT EXISTS(SELECT 1 FROM workflow_columns WHERE id=$1 AND board_id=$2)", columnID, board).Scan(&belongs); err != nil {
			return err
		}
		if !belongs {
			return errors.New("automation column does not belong to its board")
		}
	}
	if trigger == "task.due_soon" && target != "" {
		return errors.New("due-date automation cannot be restricted to a workflow column")
	}
	if (success != "" || failure != "") && target == "" {
		return errors.New("automation outcome needs a trigger column")
	}
	for _, outcome := range []string{success, failure} {
		if outcome == "" {
			continue
		}
		var allowed bool
		if err := s.DB.QueryRow(c, "SELECT EXISTS(SELECT 1 FROM transitions WHERE board_id=$1 AND from_column_id=$2 AND to_column_id=$3)", board, target, outcome).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return errors.New("automation outcome is not an allowed workflow transition")
		}
	}
	return nil
}
func (s *Store) SetRuleDueSchedule(c context.Context, id string, every, within int) error {
	_, err := s.DB.Exec(c, "UPDATE automation_rules SET schedule_every_minutes=NULLIF($2,0),due_within_hours=NULLIF($3,0) WHERE id=$1", id, every, within)
	return err
}
func (s *Store) SetRuleCooldown(c context.Context, id string, minutes int) error {
	if minutes < 0 || minutes > 7*24*60 {
		return errors.New("Cooldown muss zwischen 0 und 10080 Minuten liegen")
	}
	_, err := s.DB.Exec(c, "UPDATE automation_rules SET cooldown_minutes=$2 WHERE id=$1", id, minutes)
	return err
}
func (s *Store) CreateDueEvents(c context.Context) error {
	_, err := s.DB.Exec(c, `INSERT INTO automation_events(type,task_id,board_id,payload)
	SELECT 'task.due_soon',t.id,t.board_id,jsonb_build_object('due_at',t.due_date,'schedule_rule_id',r.id::text)
	FROM automation_rules r JOIN tasks t ON (r.board_id IS NULL OR r.board_id=t.board_id)
	WHERE r.enabled AND r.trigger_type='task.due_soon' AND r.due_within_hours IS NOT NULL AND t.completed_at IS NULL AND t.due_date <= now() + make_interval(hours => r.due_within_hours)
	AND NOT EXISTS (SELECT 1 FROM automation_events e WHERE e.type='task.due_soon' AND e.task_id=t.id
		AND e.payload->>'schedule_rule_id'=r.id::text
		AND e.occurred_at > now()-make_interval(mins => COALESCE(r.schedule_every_minutes,60)))`)
	return err
}
func (s *Store) GetRule(c context.Context, id string) (domain.AutomationRule, error) {
	var r domain.AutomationRule
	err := s.DB.QueryRow(c, "SELECT "+automationRuleSelect+" FROM automation_rules WHERE id=$1", id).Scan(&r.ID, &r.Name, &r.BoardID, &r.TriggerType, &r.TargetColumnID, &r.LabelID, &r.AgentID, &r.SuccessColumnID, &r.FailureColumnID, &r.Enabled, &r.RequireDeliveryApproval, &r.ScheduleEveryMinutes, &r.DueWithinHours, &r.CooldownMinutes, &r.CreatedAt)
	return r, err
}
func (s *Store) SetRuleEnabled(c context.Context, id string, enabled bool) error {
	_, err := s.DB.Exec(c, "UPDATE automation_rules SET enabled=$2 WHERE id=$1", id, enabled)
	return err
}
func (s *Store) DeleteRule(c context.Context, id string) error {
	_, err := s.DB.Exec(c, "DELETE FROM automation_rules WHERE id=$1", id)
	return err
}
func (s *Store) CreateRun(c context.Context, task, agent, rule string) (domain.AgentRun, error) {
	gate, err := acquireWorkspaceRunGate()
	if err != nil {
		return domain.AgentRun{}, err
	}
	defer gate.Close()
	a, e := s.GetAgent(c, agent)
	if e != nil {
		return domain.AgentRun{}, e
	}
	targets, err := s.runTargets(c, task)
	if err != nil {
		return domain.AgentRun{}, err
	}
	if err = requireRunTargets(targets); err != nil {
		return domain.AgentRun{}, err
	}
	workspace, targetProject := targets[0].LocalPath, targets[0].ProjectID
	if workspace == "" {
		workspace = filepath.Join(workspacecfg.ProjectsRoot(), targetProject)
	}
	targets, err = runRepositoryTargets(targets)
	if err != nil {
		return domain.AgentRun{}, err
	}
	return s.createRunWithWorkspace(c, task, a, agent, rule, targets[0].LocalPath, targets[0].ProjectID, "")
}
func (s *Store) createRunWithWorkspace(c context.Context, task string, a domain.Agent, agent, rule, workspace, targetProject, batchID string) (domain.AgentRun, error) {
	var r domain.AgentRun
	profile, err := s.SandboxProfile(c, defaultString(a.SandboxProfile, "strict"))
	if err != nil || !profile.Active {
		return r, errors.New("sandbox profile is unknown or inactive")
	}
	policy, err := sandbox.EffectiveProfile(profile, workspace)
	if err != nil {
		return r, err
	}
	effective, _ := json.Marshal(policy)
	e := s.DB.QueryRow(c, "INSERT INTO agent_runs(task_id,agent_id,rule_id,batch_id,prompt_snapshot,workspace_snapshot,source_workspace,target_project_id,skill_snapshot,sandbox_profile,sandbox_effective) VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,$6,$6,NULLIF($7,'')::uuid,COALESCE((SELECT jsonb_agg(jsonb_build_object('name',s.name,'path',i.install_path)) FROM agent_skills x JOIN installed_skills i ON i.id=x.installed_skill_id JOIN skills s ON s.id=i.skill_id WHERE x.agent_id=$2),'[]'::jsonb),$8,$9) RETURNING id,task_id,agent_id,COALESCE(rule_id::text,''),COALESCE(batch_id::text,''),status,prompt_snapshot,workspace_snapshot,COALESCE(target_project_id::text,''),summary,error_message,started_at,finished_at,created_at", task, agent, rule, batchID, a.Prompt, workspace, targetProject, profile.Name, effective).Scan(&r.ID, &r.TaskID, &r.AgentID, &r.RuleID, &r.BatchID, &r.Status, &r.PromptSnapshot, &r.WorkspaceSnapshot, &r.TargetProject, &r.Summary, &r.ErrorMessage, &r.StartedAt, &r.FinishedAt, &r.CreatedAt)
	if e != nil {
		return r, e
	}
	if e = s.RecordAudit(c, "", "agent_run.queued", "agent_run", r.ID, map[string]string{
		"workspace": workspace,
		"source":    "manual",
	}); e != nil {
		return domain.AgentRun{}, e
	}
	return r, nil
}
func (s *Store) CreateManualRun(c context.Context, task, agent string) (domain.AgentRun, error) {
	runs, err := s.CreateManualRuns(c, task, agent)
	if err != nil {
		return domain.AgentRun{}, err
	}
	return runs[0], nil
}
func (s *Store) CreateManualRuns(c context.Context, task, agent string) ([]domain.AgentRun, error) {
	gate, err := acquireWorkspaceRunGate()
	if err != nil {
		return nil, err
	}
	defer gate.Close()
	a, err := s.GetAgent(c, agent)
	if err != nil {
		return nil, err
	}
	if !a.Enabled {
		return nil, errors.New("agent is disabled")
	}
	targets, err := s.runTargets(c, task)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, ErrTargetSelectionRequired
	}
	for _, target := range targets {
		if target.ProjectID != "" && target.RepositoryURL == "" {
			return nil, errors.New("target project has no repository")
		}
	}
	targets, err = runRepositoryTargets(targets)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin(c)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(c)
	// A double click, a retry in another tab, or two simultaneous MCP calls
	// must not allocate a second manual batch for the same task and agent.
	if _, err = tx.Exec(c, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", task+"|manual|"+a.ID); err != nil {
		return nil, err
	}
	var active bool
	if err = tx.QueryRow(c, `SELECT EXISTS(SELECT 1 FROM agent_run_batches WHERE task_id=$1 AND agent_id=$2 AND rule_id IS NULL AND status IN('queued','running'))`, task, a.ID).Scan(&active); err != nil {
		return nil, err
	}
	if active {
		return nil, ErrTaskAgentActive
	}
	batch, err := createRunBatchTx(c, tx, task, a.ID, "", "", len(targets))
	if err != nil {
		return nil, err
	}
	runs := make([]domain.AgentRun, 0, len(targets))
	for _, target := range targets {
		workspace := target.LocalPath
		if err = lockWorkspaceTx(c, tx, workspace); err != nil {
			return nil, err
		}
		run, created, createErr := createRunTx(c, tx, task, a, agent, "", "", batch.ID, workspace, target.ProjectID)
		if createErr != nil {
			return nil, createErr
		}
		if !created {
			return nil, ErrNoRunCreated
		}
		runs = append(runs, run)
	}
	if err = tx.Commit(c); err != nil {
		return nil, err
	}
	return runs, nil
}
func (s *Store) CreateRunBatch(c context.Context, taskID, agentID, ruleID, eventID string, total int) (domain.AgentRunBatch, error) {
	var batch domain.AgentRunBatch
	err := s.DB.QueryRow(c, `INSERT INTO agent_run_batches(task_id,agent_id,rule_id,event_id,total_targets)
		VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5)
		RETURNING id,task_id,agent_id,COALESCE(rule_id::text,''),COALESCE(event_id::text,''),status,total_targets,succeeded_targets,failed_targets,cancelled_targets,created_at,updated_at`, taskID, agentID, ruleID, eventID, total).
		Scan(&batch.ID, &batch.TaskID, &batch.AgentID, &batch.RuleID, &batch.EventID, &batch.Status, &batch.TotalTargets, &batch.SucceededTargets, &batch.FailedTargets, &batch.CancelledTargets, &batch.CreatedAt, &batch.UpdatedAt)
	return batch, err
}

func createRunBatchTx(c context.Context, tx pgx.Tx, taskID, agentID, ruleID, eventID string, total int) (domain.AgentRunBatch, error) {
	var batch domain.AgentRunBatch
	err := tx.QueryRow(c, `INSERT INTO agent_run_batches(task_id,agent_id,rule_id,event_id,total_targets)
		VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5)
		RETURNING id,task_id,agent_id,COALESCE(rule_id::text,''),COALESCE(event_id::text,''),status,total_targets,succeeded_targets,failed_targets,cancelled_targets,created_at,updated_at`, taskID, agentID, ruleID, eventID, total).
		Scan(&batch.ID, &batch.TaskID, &batch.AgentID, &batch.RuleID, &batch.EventID, &batch.Status, &batch.TotalTargets, &batch.SucceededTargets, &batch.FailedTargets, &batch.CancelledTargets, &batch.CreatedAt, &batch.UpdatedAt)
	return batch, err
}

// lockWorkspaceTx serializes capacity checks and queueing for one workspace.
// The transaction-scoped lock is released on commit or rollback.
func lockWorkspaceTx(c context.Context, tx pgx.Tx, workspace string) error {
	_, err := tx.Exec(c, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", workspace)
	return err
}

// createRunTx creates one durable run inside the caller's transaction. A
// false created result denotes an idempotent event conflict.
func createRunTx(c context.Context, tx pgx.Tx, task string, a domain.Agent, agent, rule, event, batch, workspace, targetProject string) (domain.AgentRun, bool, error) {
	var run domain.AgentRun
	profileName := defaultString(a.SandboxProfile, "strict")
	var profile sandbox.Profile
	var mounts []byte
	if err := tx.QueryRow(c, `SELECT name,description,mounts,network_mode,write_mode,active FROM sandbox_profiles WHERE name=$1`, profileName).Scan(&profile.Name, &profile.Description, &mounts, &profile.NetworkMode, &profile.WriteMode, &profile.Active); err != nil {
		return run, false, err
	}
	if err := json.Unmarshal(mounts, &profile.Mounts); err != nil {
		return run, false, err
	}
	policy, err := sandbox.EffectiveProfile(profile, workspace)
	if err != nil || !profile.Active {
		return run, false, errors.New("sandbox profile is unknown or inactive")
	}
	effective, _ := json.Marshal(policy)
	err = tx.QueryRow(c, `INSERT INTO agent_runs(task_id,agent_id,rule_id,event_id,batch_id,prompt_snapshot,workspace_snapshot,source_workspace,target_project_id,skill_snapshot,sandbox_profile,sandbox_effective)
		VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,$7,$7,NULLIF($8,'')::uuid,
		COALESCE((SELECT jsonb_agg(jsonb_build_object('name',s.name,'path',i.install_path)) FROM agent_skills x JOIN installed_skills i ON i.id=x.installed_skill_id JOIN skills s ON s.id=i.skill_id WHERE x.agent_id=$2),'[]'::jsonb),$9,$10)
		ON CONFLICT DO NOTHING
		RETURNING id,task_id,agent_id,COALESCE(rule_id::text,''),COALESCE(batch_id::text,''),status,prompt_snapshot,workspace_snapshot,COALESCE(target_project_id::text,''),summary,error_message,started_at,finished_at,created_at`,
		task, agent, rule, event, batch, a.Prompt, workspace, targetProject, profile.Name, effective).
		Scan(&run.ID, &run.TaskID, &run.AgentID, &run.RuleID, &run.BatchID, &run.Status, &run.PromptSnapshot, &run.WorkspaceSnapshot, &run.TargetProject, &run.Summary, &run.ErrorMessage, &run.StartedAt, &run.FinishedAt, &run.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentRun{}, false, nil
	}
	if err == nil {
		metadata, _ := json.Marshal(map[string]string{"workspace": workspace, "source": map[bool]string{true: "automation", false: "manual"}[event != ""]})
		if _, auditErr := tx.Exec(c, `INSERT INTO audit_events(kind,resource_type,resource_id,metadata) VALUES('agent_run.queued','agent_run',$1,$2)`, run.ID, metadata); auditErr != nil {
			return domain.AgentRun{}, false, auditErr
		}
	}
	return run, err == nil, err
}
func (s *Store) RefreshRunBatch(c context.Context, id string) (domain.AgentRunBatch, error) {
	var batch domain.AgentRunBatch
	err := s.DB.QueryRow(c, `WITH counts AS (
		SELECT count(*) FILTER (WHERE status='queued') queued, count(*) FILTER (WHERE status='running') running,
		count(*) FILTER (WHERE status='succeeded') succeeded, count(*) FILTER (WHERE status='failed') failed,
		count(*) FILTER (WHERE status='cancelled') cancelled FROM agent_runs WHERE batch_id=$1
	), updated AS (UPDATE agent_run_batches b SET succeeded_targets=counts.succeeded,failed_targets=counts.failed,cancelled_targets=counts.cancelled,
		status=CASE WHEN counts.queued+counts.running>0 THEN 'running' WHEN counts.failed>0 AND (counts.succeeded>0 OR counts.cancelled>0) THEN 'partial' WHEN counts.failed>0 THEN 'failed' WHEN counts.cancelled>0 AND counts.succeeded>0 THEN 'partial' WHEN counts.cancelled>0 THEN 'cancelled' ELSE 'succeeded' END,updated_at=now()
		FROM counts WHERE b.id=$1 RETURNING b.id,b.task_id,b.agent_id,COALESCE(b.rule_id::text,''),COALESCE(b.event_id::text,''),b.status,b.total_targets,b.succeeded_targets,b.failed_targets,b.cancelled_targets,b.created_at,b.updated_at)
		SELECT * FROM updated`, id).Scan(&batch.ID, &batch.TaskID, &batch.AgentID, &batch.RuleID, &batch.EventID, &batch.Status, &batch.TotalTargets, &batch.SucceededTargets, &batch.FailedTargets, &batch.CancelledTargets, &batch.CreatedAt, &batch.UpdatedAt)
	if err == nil {
		_, err = s.DB.Exec(c, `UPDATE automation_event_claims SET status=CASE WHEN $2='partial' THEN 'failed' WHEN $2='cancelled' THEN 'blocked' ELSE $2 END,updated_at=now() WHERE batch_id=$1`, id, batch.Status)
	}
	return batch, err
}
func (s *Store) ConsumeBatchOutcome(c context.Context, id string) (bool, error) {
	tag, err := s.DB.Exec(c, `UPDATE agent_run_batches SET outcome_handled_at=now() WHERE id=$1 AND outcome_handled_at IS NULL AND status IN('succeeded','failed','cancelled','partial')`, id)
	return tag.RowsAffected() == 1, err
}

// ConsumeBatchDelivery claims the single success transition after every
// successful child run in a fan-out was explicitly applied. Failed batches
// continue to use ConsumeBatchOutcome and never reach this gate.
func (s *Store) ConsumeBatchDelivery(c context.Context, id string) (bool, error) {
	tag, err := s.DB.Exec(c, `UPDATE agent_run_batches b SET delivery_handled_at=now(),updated_at=now()
		WHERE b.id=$1 AND b.status='succeeded' AND b.delivery_handled_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM agent_runs r WHERE r.batch_id=b.id AND (r.status<>'succeeded' OR r.applied_at IS NULL))`, id)
	return tag.RowsAffected() == 1, err
}

// RecentReworkLeagueRuns lists finished attempts for one task, newest first.
// The current run is omitted. The result is a prompt summary: model, effort,
// status, gate, whether the diff was applied, and the short summary or error.
// Prompt snapshots, logs, diffs, and gate output are not loaded.
func (s *Store) RecentReworkLeagueRuns(c context.Context, taskID, excludeRunID string, limit int) ([]domain.ReworkLeagueRun, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 12 {
		limit = 12
	}
	rows, err := s.DB.Query(c, `SELECT r.id::text, a.name, r.effective_model, r.effective_effort, r.status, r.gate_status,
		(r.applied_at IS NOT NULL), r.summary, r.error_message,
		COALESCE(r.finished_at, r.started_at, r.created_at)
		FROM agent_runs r
		JOIN agents a ON a.id = r.agent_id
		WHERE r.task_id = $1
		  AND ($2 = '' OR r.id::text <> $2)
		  AND r.status IN ('succeeded', 'failed', 'cancelled')
		ORDER BY r.created_at DESC
		LIMIT $3`, taskID, strings.TrimSpace(excludeRunID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []domain.ReworkLeagueRun
	for rows.Next() {
		var run domain.ReworkLeagueRun
		if err := rows.Scan(&run.ID, &run.AgentName, &run.Model, &run.Effort, &run.Status, &run.GateStatus, &run.Applied, &run.Summary, &run.ErrorMessage, &run.OccurredAt); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// ReworkLeagueLogs loads a bounded slice of durable run-log rows for the
// attempts a rework prompt may quote. Each message is truncated in the query.
// The caller still has to prefer finding-dense lines; this only prevents a
// full protocol download.
func (s *Store) ReworkLeagueLogs(c context.Context, runIDs []string) (map[string][]domain.RunLog, error) {
	ids := make([]string, 0, len(runIDs))
	seen := map[string]struct{}{}
	for _, id := range runIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		if len(ids) == 8 {
			break
		}
	}
	grouped := map[string][]domain.RunLog{}
	if len(ids) == 0 {
		return grouped, nil
	}
	rows, err := s.DB.Query(c, `WITH ranked AS (
		SELECT l.id, l.agent_run_id::text AS run_id, l.sequence, l.level, left(l.message, 2000) AS message, l.created_at,
			row_number() OVER (PARTITION BY l.agent_run_id ORDER BY l.sequence DESC) AS tail_rank,
			(
				l.level IN ('error', 'warning')
				OR l.message ILIKE '%befund%'
				OR l.message ILIKE '%fehlgeschlagen%'
				OR l.message ILIKE '%failed%'
				OR l.message ILIKE '%panic%'
				OR l.message ILIKE '%gate%'
				OR l.message ILIKE '%nicht bestanden%'
			) AS signal
		FROM agent_run_logs l
		WHERE l.agent_run_id = ANY($1::uuid[])
	)
	SELECT id, run_id, sequence, level, message, created_at
	FROM ranked
	WHERE tail_rank <= 40 OR (signal AND tail_rank <= 200)
	ORDER BY run_id, sequence`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry domain.RunLog
		if err := rows.Scan(&entry.ID, &entry.RunID, &entry.Sequence, &entry.Level, &entry.Message, &entry.CreatedAt); err != nil {
			return nil, err
		}
		grouped[entry.RunID] = append(grouped[entry.RunID], entry)
	}
	return grouped, rows.Err()
}

func (s *Store) RunsForTask(c context.Context, task string) ([]domain.AgentRun, error) {
	r, e := s.DB.Query(c, "SELECT id,task_id,agent_id,COALESCE(rule_id::text,''),COALESCE(batch_id::text,''),status,prompt_snapshot,workspace_snapshot,COALESCE(target_project_id::text,''),summary,error_message,started_at,finished_at,created_at FROM agent_runs WHERE task_id=$1 ORDER BY created_at DESC", task)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.AgentRun])
}
func (s *Store) Run(c context.Context, id string) (domain.AgentRun, error) {
	var r domain.AgentRun
	err := s.DB.QueryRow(c, "SELECT id,task_id,agent_id,COALESCE(rule_id::text,''),COALESCE(batch_id::text,''),status,prompt_snapshot,workspace_snapshot,COALESCE(target_project_id::text,''),summary,error_message,started_at,finished_at,created_at FROM agent_runs WHERE id=$1", id).Scan(&r.ID, &r.TaskID, &r.AgentID, &r.RuleID, &r.BatchID, &r.Status, &r.PromptSnapshot, &r.WorkspaceSnapshot, &r.TargetProject, &r.Summary, &r.ErrorMessage, &r.StartedAt, &r.FinishedAt, &r.CreatedAt)
	return r, err
}

// RunQueueStatus derives operator-facing queue state from durable run rows.
// Queue position is FIFO within a workspace and the next worker poll is the
// retry point; no in-memory queue is required to recover after a restart.
func (s *Store) RunQueueStatus(c context.Context, id string) (domain.RunQueueStatus, error) {
	var status domain.RunQueueStatus
	if err := s.RefreshQueueState(c); err != nil {
		return status, err
	}
	err := s.DB.QueryRow(c, `SELECT
		CASE WHEN r.status='queued' THEN 1+(SELECT count(*) FROM agent_runs prior WHERE prior.workspace_snapshot=r.workspace_snapshot AND prior.status='queued' AND (prior.created_at<r.created_at OR (prior.created_at=r.created_at AND prior.id<r.id))) ELSE 0 END,
		CASE WHEN r.status<>'queued' THEN '' WHEN EXISTS(SELECT 1 FROM agent_runs prior WHERE prior.workspace_snapshot=r.workspace_snapshot AND prior.status='queued' AND (prior.created_at<r.created_at OR (prior.created_at=r.created_at AND prior.id<r.id))) THEN 'Wartet auf vorherige Runs im Workspace' WHEN (SELECT count(*) FROM agent_runs active WHERE active.workspace_snapshot=r.workspace_snapshot AND active.status='running') >= a.max_parallel_runs THEN 'Workspace ist ausgelastet' ELSE '' END,
		COALESCE((SELECT active.id::text FROM agent_runs active WHERE active.workspace_snapshot=r.workspace_snapshot AND active.status='running' ORDER BY active.started_at,active.created_at,active.id LIMIT 1),''),
		COALESCE((SELECT activeAgent.name FROM agents activeAgent JOIN agent_runs active ON active.agent_id=activeAgent.id WHERE active.workspace_snapshot=r.workspace_snapshot AND active.status='running' ORDER BY active.started_at,active.created_at,active.id LIMIT 1),''),
		r.workspace_snapshot, COALESCE(r.queue_wait_started_at,r.created_at), r.queue_next_attempt_at
		FROM agent_runs r JOIN agents a ON a.id=r.agent_id WHERE r.id=$1`, id).Scan(&status.Position, &status.WaitingReason, &status.BlockingRunID, &status.BlockingAgent, &status.Workspace, &status.WaitingSince, &status.NextAttemptAt)
	return status, err
}
func (s *Store) RunDelivery(c context.Context, id string) (domain.RunDelivery, error) {
	var d domain.RunDelivery
	err := s.DB.QueryRow(c, "SELECT diff_summary,gate_status,gate_output,input_tokens,output_tokens,token_usage,estimated_cost_microusd,duration_seconds,COALESCE(accepted_commit_sha,''),COALESCE(integration_branch,''),COALESCE(integration_base_sha,''),COALESCE(integration_head_sha,''),COALESCE(integration_status,''),COALESCE(pr_url,''),COALESCE(pr_number,0),applied_at FROM agent_runs WHERE id=$1", id).Scan(&d.DiffSummary, &d.GateStatus, &d.GateOutput, &d.InputTokens, &d.OutputTokens, &d.TokenUsage, &d.EstimatedCostMicrousd, &d.DurationSeconds, &d.AcceptedCommitSHA, &d.IntegrationBranch, &d.IntegrationBaseSHA, &d.IntegrationHeadSHA, &d.IntegrationStatus, &d.PRURL, &d.PRNumber, &d.AppliedAt)
	return d, err
}

func (s *Store) EnqueueIntegration(c context.Context, job domain.IntegrationJob) (domain.IntegrationJob, error) {
	var result domain.IntegrationJob
	err := s.DB.QueryRow(c, `INSERT INTO repository_integration_queue(repository_path,run_id,task_id,branch,default_branch,base_sha,head_sha)
		VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(run_id) DO UPDATE SET head_sha=EXCLUDED.head_sha,updated_at=now()
		RETURNING id,repository_path,run_id,task_id,branch,default_branch,base_sha,head_sha,status,step,pr_url,last_error,pr_number,attempts,next_attempt_at,claimed_until,created_at,updated_at`, job.RepositoryPath, job.RunID, job.TaskID, job.Branch, job.DefaultBranch, job.BaseSHA, job.HeadSHA).
		Scan(&result.ID, &result.RepositoryPath, &result.RunID, &result.TaskID, &result.Branch, &result.DefaultBranch, &result.BaseSHA, &result.HeadSHA, &result.Status, &result.Step, &result.PRURL, &result.LastError, &result.PRNumber, &result.Attempts, &result.NextAttemptAt, &result.ClaimedUntil, &result.CreatedAt, &result.UpdatedAt)
	return result, err
}

func (s *Store) UpdateIntegration(c context.Context, id, status, step, baseSHA, headSHA, prURL, lastError string, prNumber, attempts int) error {
	_, err := s.DB.Exec(c, `UPDATE repository_integration_queue SET status=$2,step=$3,base_sha=$4,head_sha=$5,pr_url=$6,last_error=$7,pr_number=$8,attempts=$9,claimed_until=NULL,next_attempt_at=CASE WHEN $7='' THEN now() ELSE now()+LEAST(make_interval(secs => 5 * greatest($9,1)),interval '5 minutes') END,updated_at=now() WHERE id=$1`, id, status, step, baseSHA, headSHA, prURL, lastError, prNumber, attempts)
	return err
}

func (s *Store) IntegrationJobs(c context.Context, limit int) ([]domain.IntegrationJob, error) {
	rows, err := s.DB.Query(c, `WITH candidates AS (
		SELECT id FROM repository_integration_queue
		WHERE status IN ('queued','running','pushed','pr_open') AND next_attempt_at<=now()
		  AND (claimed_until IS NULL OR claimed_until<=now())
		ORDER BY repository_path,created_at
		FOR UPDATE SKIP LOCKED LIMIT $1
	), claimed AS (
		UPDATE repository_integration_queue q
		SET claimed_until=now()+interval '2 minutes',status=CASE WHEN q.status='queued' THEN 'running' ELSE q.status END,updated_at=now()
		FROM candidates c WHERE q.id=c.id
		RETURNING q.id,q.repository_path,q.run_id,q.task_id,q.branch,q.default_branch,q.base_sha,q.head_sha,q.status,q.step,q.pr_url,q.last_error,q.pr_number,q.attempts,q.next_attempt_at,q.claimed_until,q.created_at,q.updated_at
	)
	SELECT id,repository_path,run_id,task_id,branch,default_branch,base_sha,head_sha,status,step,pr_url,last_error,pr_number,attempts,next_attempt_at,claimed_until,created_at,updated_at FROM claimed`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.IntegrationJob])
}

func (s *Store) ReleasePublication(c context.Context, taskID, projectID, runID string) (domain.ReleasePublication, bool, error) {
	var p domain.ReleasePublication
	err := s.DB.QueryRow(c, `SELECT task_id::text,project_id::text,run_id::text,repository_url,source_branch,target_branch,commit_sha,pr_number,pr_url,comment_body,audit_recorded,comment_recorded
		FROM release_publications WHERE task_id=$1 AND project_id=$2 AND run_id=$3`, taskID, projectID, runID).Scan(
		&p.TaskID, &p.ProjectID, &p.RunID, &p.RepositoryURL, &p.SourceBranch, &p.TargetBranch, &p.CommitSHA, &p.PRNumber, &p.PRURL, &p.CommentBody, &p.AuditRecorded, &p.CommentRecorded)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReleasePublication{}, false, nil
	}
	return p, err == nil, err
}

// FinalizeReleasePublication atomically records the external PR and the two
// taskboard side effects. A retry for the same task/project/run is a no-op.
func (s *Store) FinalizeReleasePublication(c context.Context, p domain.ReleasePublication, metadata map[string]string) (bool, error) {
	tx, err := s.DB.Begin(c)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(c)
	tag, err := tx.Exec(c, `INSERT INTO release_publications(task_id,project_id,run_id,repository_url,source_branch,target_branch,commit_sha,pr_number,pr_url,comment_body,audit_recorded,comment_recorded)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,TRUE,TRUE) ON CONFLICT(task_id,project_id,run_id) DO NOTHING`, p.TaskID, p.ProjectID, p.RunID, p.RepositoryURL, p.SourceBranch, p.TargetBranch, p.CommitSHA, p.PRNumber, p.PRURL, p.CommentBody)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		if err := tx.Commit(c); err != nil {
			return false, err
		}
		return false, nil
	}
	if err = recordAudit(c, tx, "", "release.pr.published", "task", p.TaskID, metadata); err != nil {
		return false, err
	}
	if _, err = tx.Exec(c, "INSERT INTO task_comments(task_id,author,body) VALUES($1,'Release-Agent',$2)", p.TaskID, p.CommentBody); err != nil {
		return false, err
	}
	if err = tx.Commit(c); err != nil {
		return false, err
	}
	return true, nil
}

// AcceptedRunCommitSHAs is the trust boundary for follow-up delivery runs.
// Only commits recorded by a successful acceptance for this exact managed
// checkout are allowed to remain ahead of origin.
func (s *Store) AcceptedRunCommitSHAs(c context.Context, source string) ([]string, error) {
	rows, err := s.DB.Query(c, `SELECT accepted_commit_sha FROM agent_runs
		WHERE source_workspace=$1 AND accepted_commit_sha IS NOT NULL AND accepted_commit_sha <> ''`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var shas []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		shas = append(shas, sha)
	}
	return shas, rows.Err()
}

func (s *Store) RunUsage(c context.Context, id string) (domain.UsageReport, error) {
	var u domain.UsageReport
	// token_usage is the pre-telemetry column. Keep it visible for legacy
	// runs, but mark the report incomplete: it contains no reliable token
	// class breakdown and must not become a synthetic cost estimate.
	err := s.DB.QueryRow(c, `SELECT usage_provider,usage_model,usage_service_tier,
		CASE WHEN usage_total_tokens IS NULL AND token_usage > 0 THEN 'incomplete' ELSE usage_status END,
		cost_source,COALESCE(price_version,''),usage_api_calls,usage_input_tokens,usage_output_tokens,
		usage_cached_input_tokens,usage_cache_write_tokens,usage_reasoning_tokens,
		COALESCE(usage_total_tokens,NULLIF(token_usage,0)),native_cost_microusd,calculated_cost_microusd,
		COALESCE(raw_usage,'{}'::jsonb),cost_calculated_at
		FROM agent_runs WHERE id=$1`, id).Scan(&u.Provider, &u.Model, &u.ServiceTier, &u.Status, &u.CostSource, &u.PriceVersion, &u.APICalls, &u.InputTokens, &u.OutputTokens, &u.CachedInputTokens, &u.CacheWriteTokens, &u.ReasoningTokens, &u.TotalTokens, &u.NativeCostMicrousd, &u.CalculatedCostMicrousd, &u.RawUsage, &u.CostCalculatedAt)
	return u, err
}

func (s *Store) SetRunUsage(c context.Context, id string, u domain.UsageReport) error {
	_, err := s.DB.Exec(c, `UPDATE agent_runs SET usage_provider=$2,usage_model=$3,usage_service_tier=$4,usage_status=$5,cost_source=$6,price_version=NULLIF($7,''),usage_api_calls=$8,usage_input_tokens=$9,usage_output_tokens=$10,usage_cached_input_tokens=$11,usage_cache_write_tokens=$12,usage_reasoning_tokens=$13,usage_total_tokens=$14,native_cost_microusd=$15,calculated_cost_microusd=$16,raw_usage=$17,cost_calculated_at=$18 WHERE id=$1`, id, u.Provider, u.Model, u.ServiceTier, u.Status, u.CostSource, u.PriceVersion, u.APICalls, u.InputTokens, u.OutputTokens, u.CachedInputTokens, u.CacheWriteTokens, u.ReasoningTokens, u.TotalTokens, u.NativeCostMicrousd, u.CalculatedCostMicrousd, u.RawUsage, u.CostCalculatedAt)
	return err
}
func (s *Store) SetRunDelivery(c context.Context, id, diff, gate, output string, inputTokens, outputTokens, tokens int, estimatedCostMicrousd int64, duration int) error {
	_, err := s.DB.Exec(c, "UPDATE agent_runs SET diff_summary=$2,gate_status=$3,gate_output=$4,input_tokens=$5,output_tokens=$6,token_usage=$7,estimated_cost_microusd=$8,duration_seconds=$9 WHERE id=$1", id, diff, gate, output, inputTokens, outputTokens, tokens, estimatedCostMicrousd, duration)
	return err
}
func (s *Store) RunSource(c context.Context, id string) (string, error) {
	var source string
	err := s.DB.QueryRow(c, "SELECT source_workspace FROM agent_runs WHERE id=$1", id).Scan(&source)
	return source, err
}
func (s *Store) RunWorktree(c context.Context, id string) (string, error) {
	var path string
	err := s.DB.QueryRow(c, "SELECT worktree_path FROM agent_runs WHERE id=$1", id).Scan(&path)
	return path, err
}

func (s *Store) RunStartSHA(c context.Context, id string) (string, error) {
	var sha string
	err := s.DB.QueryRow(c, "SELECT run_start_sha FROM agent_runs WHERE id=$1", id).Scan(&sha)
	return sha, err
}

// LatestSucceededDeliveryWorktree returns the newest succeeded Delivery run
// for a task, including runs that have not been applied. The name predicate
// must stay aligned with isDeliveryAgent. An empty worktree path is returned
// as found so Review can ask for a new Delivery instead of falling back to
// an older checkout or to master.
func (s *Store) LatestSucceededDeliveryWorktree(c context.Context, taskID, targetProject string) (domain.DeliveryWorktree, bool, error) {
	var d domain.DeliveryWorktree
	err := s.DB.QueryRow(c, `SELECT r.id::text, COALESCE(r.worktree_path,''), COALESCE(r.source_workspace,''), COALESCE(r.run_start_sha,''), COALESCE(r.target_project_id::text,'')
		FROM agent_runs r
		JOIN agents a ON a.id = r.agent_id
		WHERE r.task_id = $1
		  AND r.status = 'succeeded'
		  AND ($2 = '' OR COALESCE(r.target_project_id::text,'') = $2)
		  AND (
		    lower(btrim(a.name)) = 'delivery agent'
		    OR lower(btrim(a.name)) LIKE 'delivery agent %'
		  )
		ORDER BY COALESCE(r.finished_at, r.created_at) DESC, r.created_at DESC, r.id DESC
		LIMIT 1`, taskID, strings.TrimSpace(targetProject)).Scan(&d.RunID, &d.WorktreePath, &d.SourceWorkspace, &d.StartSHA, &d.TargetProject)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DeliveryWorktree{}, false, nil
	}
	if err != nil {
		return domain.DeliveryWorktree{}, false, err
	}
	return d, true, nil
}

func (s *Store) SetRunStartSHA(c context.Context, id, sha string) error {
	_, err := s.DB.Exec(c, "UPDATE agent_runs SET run_start_sha=$2 WHERE id=$1 AND run_start_sha=''", id, sha)
	return err
}

// ActiveRunWorktreePaths are excluded from a workspace migration. Their
// providers may still hold files open, so the old checkout remains the safe
// source of truth until the run reaches a terminal state.
func (s *Store) ActiveRunWorktreePaths(c context.Context) (map[string]bool, error) {
	rows, err := s.DB.Query(c, `SELECT worktree_path FROM agent_runs WHERE status IN ('queued','running') AND worktree_path <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := map[string]bool{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths[filepath.Clean(path)] = true
	}
	return paths, rows.Err()
}

// ReclaimableRunWorktrees returns only terminal deliveries which can never be
// accepted again: failed/cancelled runs, failed quality gates, or an already
// applied run. The caller retains logs and run history, then removes only the
// isolated checkout after the configured retention period.
func (s *Store) ReclaimableRunWorktrees(c context.Context, before time.Time, limit int) ([]domain.WorktreeCleanupCandidate, error) {
	if limit < 1 {
		limit = 1
	}
	rows, err := s.DB.Query(c, `SELECT id,source_workspace,worktree_path FROM agent_runs
		WHERE worktree_path <> '' AND finished_at IS NOT NULL AND finished_at < $1
		  AND (status IN ('failed','cancelled') OR gate_status='failed' OR applied_at IS NOT NULL)
		ORDER BY finished_at LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.WorktreeCleanupCandidate])
}
func (s *Store) MarkRunApplied(c context.Context, id, commitSHA string) (bool, error) {
	tag, err := s.DB.Exec(c, "UPDATE agent_runs SET accepted_commit_sha=$2,applied_at=now(),summary='Änderungen übernommen' WHERE id=$1 AND applied_at IS NULL AND $2 <> ''", id, commitSHA)
	return tag.RowsAffected() == 1, err
}

// HumanQAApproved verifies the current task gate before repository work starts.
func (s *Store) HumanQAApproved(c context.Context, id string) (bool, error) {
	var approved bool
	err := s.DB.QueryRow(c, `SELECT EXISTS (SELECT 1 FROM agent_runs r JOIN tasks t ON t.id=r.task_id JOIN workflow_columns col ON col.id=t.column_id JOIN task_decisions d ON d.task_id=r.task_id WHERE r.id=$1 AND r.applied_at IS NULL AND lower(trim(col.name))='qa' AND d.decision_key='qa_release' AND d.superseded_at IS NULL AND d.response->'release_decision' ? 'approve')`, id).Scan(&approved)
	return approved, err
}

// MarkRunAppliedByHumanQA is the atomic Human-QA integration boundary.
func (s *Store) MarkRunAppliedByHumanQA(c context.Context, id, commitSHA, actor string) (bool, error) {
	tx, err := s.DB.Begin(c)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(c)
	var taskID string
	err = tx.QueryRow(c, `SELECT r.task_id::text FROM agent_runs r JOIN tasks t ON t.id=r.task_id JOIN workflow_columns col ON col.id=t.column_id WHERE r.id=$1 AND r.applied_at IS NULL AND lower(trim(col.name))='qa' AND EXISTS (SELECT 1 FROM task_decisions d WHERE d.task_id=r.task_id AND d.decision_key='qa_release' AND d.superseded_at IS NULL AND d.response->'release_decision' ? 'approve') FOR UPDATE OF r`, id).Scan(&taskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, errors.New("Run darf erst nach positiver Human-QA in der QA-Spalte übernommen werden")
		}
		return false, err
	}
	result, err := tx.Exec(c, `UPDATE agent_runs SET accepted_commit_sha=$2,applied_at=now(),summary='Änderungen übernommen' WHERE id=$1 AND applied_at IS NULL AND $2 <> ''`, id, strings.TrimSpace(commitSHA))
	if err != nil {
		return false, err
	}
	if result.RowsAffected() != 1 {
		return false, errors.New("Änderungen dieses Runs wurden bereits übernommen")
	}
	if err = recordAudit(c, tx, actor, "delivery.applied", "agent_run", id, map[string]string{"run_id": id, "task_id": taskID, "commit_sha": strings.TrimSpace(commitSHA), "qa_decision": "approve"}); err != nil {
		return false, err
	}
	if err = tx.Commit(c); err != nil {
		return false, err
	}
	return true, nil
}

// UpdateRunAcceptedCommitSHA stores the replacement identity after a clean
// rebase. Applied runs are immutable so retries cannot rewrite history.
func (s *Store) UpdateRunAcceptedCommitSHA(c context.Context, id, commitSHA string) (bool, error) {
	if strings.TrimSpace(commitSHA) == "" {
		return false, errors.New("accepted commit SHA darf nicht leer sein")
	}
	tag, err := s.DB.Exec(c, "UPDATE agent_runs SET accepted_commit_sha=$2 WHERE id=$1 AND applied_at IS NULL", id, strings.TrimSpace(commitSHA))
	return tag.RowsAffected() == 1, err
}

func (s *Store) SetRunIntegration(c context.Context, id, branch, baseSHA, headSHA, status, prURL string, prNumber int) error {
	_, err := s.DB.Exec(c, `UPDATE agent_runs SET integration_branch=$2,integration_base_sha=$3,integration_head_sha=$4,integration_status=$5,pr_url=$6,pr_number=$7 WHERE id=$1`, id, branch, baseSHA, headSHA, status, prURL, prNumber)
	return err
}
func (s *Store) MarkRunDiscarded(c context.Context, id string) error {
	_, err := s.DB.Exec(c, "UPDATE agent_runs SET summary='Änderungen verworfen' WHERE id=$1", id)
	return err
}
func (s *Store) RunLogs(c context.Context, id string) ([]domain.RunLog, error) {
	rows, err := s.DB.Query(c, "SELECT id,agent_run_id,sequence,level,message,created_at FROM agent_run_logs WHERE agent_run_id=$1 ORDER BY sequence", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.RunLog])
}

// RecentRunLogs returns the chronological tail for browser rendering. The
// complete append-only log remains available through RunLogs for MCP, export
// and structured agent-output parsing.
func (s *Store) RecentRunLogs(c context.Context, id string, limit int) ([]domain.RunLog, bool, error) {
	if limit < 1 {
		limit = 1
	}
	rows, err := s.DB.Query(c, "SELECT id,agent_run_id,sequence,level,message,created_at FROM agent_run_logs WHERE agent_run_id=$1 ORDER BY sequence DESC LIMIT $2", id, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	logs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[domain.RunLog])
	if err != nil {
		return nil, false, err
	}
	truncated := len(logs) > limit
	if truncated {
		logs = logs[:limit]
	}
	for left, right := 0, len(logs)-1; left < right; left, right = left+1, right-1 {
		logs[left], logs[right] = logs[right], logs[left]
	}
	return logs, truncated, nil
}

// RunLogsBefore returns the chronological page immediately before sequence.
// Keeping pagination in the database prevents a long-lived run console from
// repeatedly transferring its complete history to the browser.
func (s *Store) RunLogsBefore(c context.Context, id string, before, limit int) ([]domain.RunLog, bool, error) {
	if before < 2 {
		return nil, false, nil
	}
	if limit < 1 {
		limit = 1
	}
	rows, err := s.DB.Query(c, "SELECT id,agent_run_id,sequence,level,message,created_at FROM agent_run_logs WHERE agent_run_id=$1 AND sequence < $2 ORDER BY sequence DESC LIMIT $3", id, before, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	logs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[domain.RunLog])
	if err != nil {
		return nil, false, err
	}
	truncated := len(logs) > limit
	if truncated {
		logs = logs[:limit]
	}
	for left, right := 0, len(logs)-1; left < right; left, right = left+1, right-1 {
		logs[left], logs[right] = logs[right], logs[left]
	}
	return logs, truncated, nil
}

// RunLogsAfter returns only entries written after a browser's latest known
// sequence number.  The live console uses this instead of re-downloading its
// complete visible tail whenever the terminal emits another chunk.
func (s *Store) RunLogsAfter(c context.Context, id string, after, limit int) ([]domain.RunLog, bool, error) {
	if after < 0 {
		after = 0
	}
	if limit < 1 {
		limit = 1
	}
	rows, err := s.DB.Query(c, `SELECT id,agent_run_id,sequence,level,message,created_at
		FROM agent_run_logs WHERE agent_run_id=$1 AND sequence > $2
		ORDER BY sequence LIMIT $3`, id, after, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	logs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[domain.RunLog])
	if err != nil {
		return nil, false, err
	}
	hasMore := len(logs) > limit
	if hasMore {
		logs = logs[:limit]
	}
	return logs, hasMore, nil
}

func (s *Store) RunLog(c context.Context, id string, sequence int) (domain.RunLog, error) {
	var log domain.RunLog
	err := s.DB.QueryRow(c, "SELECT id,agent_run_id,sequence,level,message,created_at FROM agent_run_logs WHERE agent_run_id=$1 AND sequence=$2", id, sequence).Scan(&log.ID, &log.RunID, &log.Sequence, &log.Level, &log.Message, &log.CreatedAt)
	return log, err
}

// RunTrace presents the durable orchestration chain without exposing raw
// prompts or provider secrets. It is deliberately assembled from persisted
// state so a page refresh never loses provenance.
func (s *Store) RunTrace(c context.Context, id string) (domain.RunTrace, error) {
	var trace domain.RunTrace
	var created time.Time
	var started, finished, applied *time.Time
	var gate string
	err := s.DB.QueryRow(c, `SELECT r.id,r.task_id,COALESCE(r.rule_id::text,''),COALESCE(r.batch_id::text,''),COALESCE(r.event_id::text,''),r.status,r.created_at,r.started_at,r.finished_at,r.gate_status,r.applied_at
		FROM agent_runs r WHERE r.id=$1`, id).Scan(&trace.RunID, &trace.TaskID, &trace.RuleID, &trace.BatchID, &trace.EventID, &trace.Status, &created, &started, &finished, &gate, &applied)
	if err != nil {
		return trace, err
	}
	trace.Items = append(trace.Items, domain.RunTraceItem{At: created, Kind: "Run eingeplant", Detail: "Agent-Run wurde in die Warteschlange gelegt."})
	var waitStarted *time.Time
	var waitReason string
	if queueErr := s.DB.QueryRow(c, "SELECT queue_wait_started_at,queue_wait_reason FROM agent_runs WHERE id=$1", id).Scan(&waitStarted, &waitReason); queueErr == nil && waitStarted != nil && waitReason != "" {
		trace.Items = append(trace.Items, domain.RunTraceItem{At: *waitStarted, Kind: "Warten auf Workspace", Detail: waitReason})
	}
	if trace.EventID != "" {
		var eventType string
		var occurred time.Time
		if err = s.DB.QueryRow(c, "SELECT type,occurred_at FROM automation_events WHERE id=$1", trace.EventID).Scan(&eventType, &occurred); err == nil {
			trace.Items = append(trace.Items, domain.RunTraceItem{At: occurred, Kind: "Automation ausgelöst", Detail: eventType})
		}
	}
	if trace.BatchID != "" {
		var total int
		var batchStatus string
		var batchCreated time.Time
		if err = s.DB.QueryRow(c, "SELECT total_targets,status,created_at FROM agent_run_batches WHERE id=$1", trace.BatchID).Scan(&total, &batchStatus, &batchCreated); err == nil {
			trace.Items = append(trace.Items, domain.RunTraceItem{At: batchCreated, Kind: "Ziel-Batch", Detail: fmt.Sprintf("%d Repository-Ziel(e) · %s", total, batchStatus)})
		}
	}
	if started != nil {
		trace.Items = append(trace.Items, domain.RunTraceItem{At: *started, Kind: "Agent gestartet", Detail: "Isolierter Worktree wurde angelegt."})
	}
	if finished != nil {
		detail := "Run beendet"
		if gate != "" {
			detail += " · Qualitäts-Gate: " + gate
		}
		trace.Items = append(trace.Items, domain.RunTraceItem{At: *finished, Kind: "Agent beendet", Detail: detail})
	}
	if applied != nil {
		trace.Items = append(trace.Items, domain.RunTraceItem{At: *applied, Kind: "Änderungen übernommen", Detail: "Delivery-Freigabe erteilt."})
	}
	return trace, nil
}
func (s *Store) QueuedRuns(c context.Context) ([]domain.AgentRun, error) {
	if err := s.RefreshQueueState(c); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(c, "SELECT id,task_id,agent_id,COALESCE(rule_id::text,''),COALESCE(batch_id::text,''),status,prompt_snapshot,workspace_snapshot,COALESCE(target_project_id::text,''),summary,error_message,started_at,finished_at,created_at FROM agent_runs WHERE status='queued' ORDER BY created_at LIMIT 20")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.AgentRun])
}

// RefreshQueueState persists the current resource explanation and a bounded
// retry point. It is safe to call from every worker cycle and after restart.
func (s *Store) RefreshQueueState(c context.Context) error {
	_, err := s.DB.Exec(c, `WITH state AS (
		SELECT r.id,
			(EXISTS (SELECT 1 FROM agent_runs prior WHERE prior.workspace_snapshot=r.workspace_snapshot AND prior.status='queued' AND (prior.created_at<r.created_at OR (prior.created_at=r.created_at AND prior.id<r.id)))
			 OR (SELECT count(*) FROM agent_runs active WHERE active.workspace_snapshot=r.workspace_snapshot AND active.status='running') >= (SELECT max_parallel_runs FROM agents WHERE id=r.agent_id)) AS blocked,
			EXISTS (SELECT 1 FROM agent_runs prior WHERE prior.workspace_snapshot=r.workspace_snapshot AND prior.status='queued' AND (prior.created_at<r.created_at OR (prior.created_at=r.created_at AND prior.id<r.id))) AS has_prior
		FROM agent_runs r WHERE r.status='queued'
	)
	UPDATE agent_runs r SET
		queue_wait_started_at=CASE WHEN state.blocked THEN COALESCE(r.queue_wait_started_at,r.created_at) ELSE NULL END,
		queue_wait_reason=CASE WHEN NOT state.blocked THEN '' WHEN state.has_prior THEN 'Wartet auf vorherige Runs im Workspace' ELSE 'Workspace ist ausgelastet' END,
		queue_next_attempt_at=CASE WHEN state.blocked THEN CASE WHEN r.queue_wait_started_at IS NULL THEN now()+interval '3 seconds' WHEN r.queue_next_attempt_at > now() THEN r.queue_next_attempt_at ELSE now() END ELSE now() END
	FROM state WHERE r.id=state.id`)
	return err
}

// RecoverInterruptedRuns releases workspaces after a service restart. A running
// process belongs to the previous service instance and cannot safely be resumed.
// The affected runs are returned so the worker can finish them through the same
// notification, comment and workflow-outcome path as every other failure.
func (s *Store) RecoverInterruptedRuns(c context.Context) ([]domain.AgentRun, error) {
	rows, err := s.DB.Query(c, `UPDATE agent_runs
		SET status='failed',finished_at=now(),summary='Agent-Run durch Dienstneustart unterbrochen',error_message='Taskboard wurde während dieses Agent-Runs neu gestartet'
		WHERE status='running'
		RETURNING id,task_id,agent_id,COALESCE(rule_id::text,''),COALESCE(batch_id::text,''),status,prompt_snapshot,workspace_snapshot,COALESCE(target_project_id::text,''),summary,error_message,started_at,finished_at,created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.AgentRun])
}
func (s *Store) ClaimRun(c context.Context, id string) (bool, error) {
	tag, err := s.DB.Exec(c, `UPDATE agent_runs r SET status='running',started_at=now(),queue_wait_reason=''
		WHERE r.id=$1 AND r.status='queued'
		AND r.queue_next_attempt_at <= now()
		AND NOT EXISTS (SELECT 1 FROM agent_runs prior WHERE prior.workspace_snapshot=r.workspace_snapshot AND prior.status='queued' AND (prior.created_at<r.created_at OR (prior.created_at=r.created_at AND prior.id<r.id)))
		AND (SELECT count(*) FROM agent_runs active WHERE active.workspace_snapshot=r.workspace_snapshot AND active.status='running') < (SELECT max_parallel_runs FROM agents WHERE id=r.agent_id)`, id)
	return tag.RowsAffected() == 1, err
}
func (s *Store) CancelRun(c context.Context, id string) (bool, error) {
	tag, err := s.DB.Exec(c, "UPDATE agent_runs SET status='cancelled',finished_at=now(),summary='Run durch Nutzer abgebrochen',queue_wait_reason='' WHERE id=$1 AND status IN('queued','running')", id)
	if err == nil && tag.RowsAffected() == 1 {
		_ = s.RecordAudit(c, "", "agent_run.cancelled", "agent_run", id, map[string]string{"reason": "user requested"})
	}
	return tag.RowsAffected() == 1, err
}

// WakeWorkspace makes queued runs immediately eligible after a terminal run
// releases a repository. ClaimRun remains the single atomic start gate.
func (s *Store) WakeWorkspace(c context.Context, runID string) error {
	_, err := s.DB.Exec(c, `WITH released AS (SELECT workspace_snapshot FROM agent_runs WHERE id=$1)
		UPDATE agent_runs r SET queue_next_attempt_at=now(),queue_wait_reason=''
		FROM released WHERE r.status='queued' AND r.workspace_snapshot=released.workspace_snapshot`, runID)
	if err == nil {
		_ = s.RecordAudit(c, "", "agent_run.queue_wakeup", "agent_run", runID, map[string]string{"reason": "workspace released"})
	}
	return err
}
func (s *Store) CreateNotification(c context.Context, task, run, kind, message string) error {
	_, err := s.DB.Exec(c, "INSERT INTO notifications(task_id,agent_run_id,kind,message) VALUES(NULLIF($1,'')::uuid,NULLIF($2,'')::uuid,$3,$4)", task, run, kind, message)
	return err
}
func (s *Store) MarkNotificationRead(c context.Context, id string) error {
	_, err := s.DB.Exec(c, "UPDATE notifications SET read_at=now() WHERE id=$1", id)
	return err
}
func (s *Store) Webhooks(c context.Context) ([]domain.Webhook, error) {
	rows, err := s.DB.Query(c, `SELECT w.id,w.name,w.url,w.events,w.enabled,w.created_at,
		(SELECT count(*) FROM webhook_deliveries d WHERE d.webhook_id=w.id AND d.status IN ('queued','sending','retry')),
		(SELECT count(*) FROM webhook_deliveries d WHERE d.webhook_id=w.id AND d.status='dead')
		FROM webhook_subscriptions w ORDER BY w.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.Webhook])
}
func (s *Store) AddWebhook(c context.Context, name, url, events string) error {
	_, err := s.DB.Exec(c, "INSERT INTO webhook_subscriptions(name,url,events) VALUES($1,$2,$3)", strings.TrimSpace(name), strings.TrimSpace(url), strings.TrimSpace(events))
	return err
}
func (s *Store) DeleteWebhook(c context.Context, id string) error {
	_, err := s.DB.Exec(c, "DELETE FROM webhook_subscriptions WHERE id=$1", id)
	return err
}
func (s *Store) SetWebhookEnabled(c context.Context, id string, enabled bool) error {
	result, err := s.DB.Exec(c, "UPDATE webhook_subscriptions SET enabled=$2 WHERE id=$1", id, enabled)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return errors.New("webhook not found")
	}
	return nil
}
func (s *Store) QueueWebhookDelivery(c context.Context, webhookID, runID, event string, payload []byte) error {
	_, err := s.DB.Exec(c, `INSERT INTO webhook_deliveries(webhook_id,agent_run_id,event_name,payload)
		VALUES($1,$2,$3,$4::jsonb) ON CONFLICT(webhook_id,agent_run_id,event_name) DO NOTHING`, webhookID, runID, event, payload)
	return err
}

// ClaimWebhookDeliveries provides at-least-once delivery. A stable delivery ID
// is sent to receivers so they can make repeated HTTP attempts idempotent.
// Sending rows abandoned by a crashed process become eligible again after a
// minute, rather than disappearing permanently.
func (s *Store) ClaimWebhookDeliveries(c context.Context, limit int) ([]domain.WebhookDelivery, error) {
	if limit < 1 {
		limit = 1
	}
	rows, err := s.DB.Query(c, `WITH candidates AS (
		SELECT d.id FROM webhook_deliveries d JOIN webhook_subscriptions w ON w.id=d.webhook_id
		WHERE w.enabled AND ((d.status IN ('queued','retry') AND d.next_attempt_at <= now()) OR (d.status='sending' AND d.updated_at < now()-interval '1 minute'))
		ORDER BY d.next_attempt_at,d.created_at FOR UPDATE OF d SKIP LOCKED LIMIT $1
	), claimed AS (
		UPDATE webhook_deliveries d SET status='sending',attempt_count=attempt_count+1,updated_at=now()
		FROM candidates c WHERE d.id=c.id
		RETURNING d.id,d.webhook_id,d.event_name,d.payload::text,d.status,d.attempt_count,d.last_error
	) SELECT c.id,c.webhook_id,w.url,c.event_name,c.payload,c.status,c.last_error,c.attempt_count
	FROM claimed c JOIN webhook_subscriptions w ON w.id=c.webhook_id`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.WebhookDelivery])
}
func (s *Store) MarkWebhookDelivered(c context.Context, id string) error {
	_, err := s.DB.Exec(c, "UPDATE webhook_deliveries SET status='delivered',delivered_at=now(),last_error='',updated_at=now() WHERE id=$1 AND status='sending'", id)
	return err
}
func (s *Store) RetryWebhookDelivery(c context.Context, id, message string, wait time.Duration, terminal bool) error {
	status := "retry"
	if terminal {
		status = "dead"
	}
	_, err := s.DB.Exec(c, "UPDATE webhook_deliveries SET status=$2,next_attempt_at=now()+$3::interval,last_error=$4,updated_at=now() WHERE id=$1 AND status='sending'", id, status, wait.String(), strings.TrimSpace(message))
	return err
}
func (s *Store) Schedules(c context.Context) ([]domain.Schedule, error) {
	rows, err := s.DB.Query(c, `SELECT r.id,r.name,COALESCE(b.name,'Alle Boards'),COALESCE(a.name,'Kein Agent'),COALESCE(r.schedule_every_minutes,0),COALESCE(r.due_within_hours,0),r.enabled FROM automation_rules r LEFT JOIN boards b ON b.id=r.board_id LEFT JOIN agents a ON a.id=r.agent_id WHERE r.trigger_type='task.due_soon' ORDER BY r.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.Schedule])
}
func (s *Store) Providers(c context.Context) ([]domain.ProviderSetting, error) {
	rows, err := s.DB.Query(c, "SELECT id,provider,enabled,model,command,secret_env,base_url,options::text,discovery_source,discovery_error,discovery_at,updated_at FROM provider_settings ORDER BY provider")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.ProviderSetting])
}
func (s *Store) SaveProvider(c context.Context, provider, model, command, env, base, options string, enabled bool) error {
	options = strings.TrimSpace(options)
	if options == "" {
		options = "{}"
	}
	var object map[string]any
	if !json.Valid([]byte(options)) || json.Unmarshal([]byte(options), &object) != nil {
		return errors.New("Provider-Optionen müssen ein JSON-Objekt sein")
	}
	// model is retained only for migration/API compatibility. It is not
	// changed by provider saves and is never used to select a new run.
	_, err := s.DB.Exec(c, "UPDATE provider_settings SET enabled=$2,command=$3,secret_env=$4,base_url=$5,options=$6::jsonb,updated_at=now() WHERE provider=$1", provider, enabled, command, env, base, options)
	return err
}
func (s *Store) Provider(c context.Context, provider string) (domain.ProviderSetting, error) {
	var p domain.ProviderSetting
	err := s.DB.QueryRow(c, "SELECT id,provider,enabled,model,command,secret_env,base_url,options::text,discovery_source,discovery_error,discovery_at,updated_at FROM provider_settings WHERE provider=$1", provider).Scan(&p.ID, &p.Provider, &p.Enabled, &p.Model, &p.Command, &p.SecretEnv, &p.BaseURL, &p.Options, &p.DiscoverySource, &p.DiscoveryError, &p.DiscoveryAt, &p.UpdatedAt)
	return p, err
}
func (s *Store) PendingEvents(c context.Context) ([]domain.AutomationEvent, error) {
	r, e := s.DB.Query(c, "SELECT id,type,COALESCE(task_id::text,''),COALESCE(board_id::text,''),payload,occurred_at FROM automation_events WHERE processed_at IS NULL ORDER BY occurred_at LIMIT 20")
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.AutomationEvent])
}
func (s *Store) RulesForEvent(c context.Context, e domain.AutomationEvent) ([]domain.AutomationRule, error) {
	r, err := s.DB.Query(c, `SELECT `+automationRuleSelect+` 
		FROM automation_rules WHERE enabled AND trigger_type=$1 AND (board_id IS NULL OR board_id=$2)
		AND (target_column_id IS NULL OR target_column_id::text=(SELECT payload->>'target_column_id' FROM automation_events WHERE id=$3))
		AND (label_id IS NULL OR EXISTS(SELECT 1 FROM task_labels tl WHERE tl.task_id=$4 AND tl.label_id=automation_rules.label_id))
		AND ($1 <> 'task.due_soon' OR COALESCE((SELECT payload->>'schedule_rule_id' FROM automation_events WHERE id=$3),'')='' OR id::text=(SELECT payload->>'schedule_rule_id' FROM automation_events WHERE id=$3))`, e.Type, e.BoardID, e.ID, e.TaskID)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return pgx.CollectRows(r, pgx.RowToStructByPos[domain.AutomationRule])
}

// AutomationPreviewTasks is a read-only dry-run aid for the rule editor. It
// deliberately returns a bounded list: previewing a rule must never enqueue
// an event, claim capacity, or start an agent.
func (s *Store) AutomationPreviewTasks(c context.Context, boardID, columnID string, limit int) ([]domain.AutomationPreviewTask, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	rows, err := s.DB.Query(c, `SELECT t.id,t.title,c.name FROM tasks t JOIN workflow_columns c ON c.id=t.column_id
		WHERE t.board_id=$1 AND (NULLIF($2,'') IS NULL OR t.column_id=NULLIF($2,'')::uuid) AND t.completed_at IS NULL ORDER BY t.created_at DESC LIMIT $3`, boardID, columnID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.AutomationPreviewTask])
}
func (s *Store) CreateRunForEvent(c context.Context, event domain.AutomationEvent, rule domain.AutomationRule) (domain.AgentRun, error) {
	runs, err := s.CreateRunsForEvent(c, event, rule)
	if err != nil {
		return domain.AgentRun{}, err
	}
	return runs[0], nil
}
func (s *Store) CreateRunsForEvent(c context.Context, event domain.AutomationEvent, rule domain.AutomationRule) ([]domain.AgentRun, error) {
	gate, err := acquireWorkspaceRunGate()
	if err != nil {
		return nil, err
	}
	defer gate.Close()
	a, e := s.GetAgent(c, rule.AgentID)
	if e != nil {
		return nil, e
	}
	if !a.Enabled {
		return nil, errors.New("agent is disabled")
	}
	targets, e := s.runTargets(c, event.TaskID)
	if e != nil {
		return nil, e
	}
	if len(targets) == 0 {
		return nil, ErrTargetSelectionRequired
	}
	for _, target := range targets {
		if target.ProjectID != "" && target.RepositoryURL == "" {
			return nil, errors.New("target project has no repository")
		}
	}
	targets, e = runRepositoryTargets(targets)
	if e != nil {
		return nil, e
	}
	tx, e := s.DB.Begin(c)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(c)
	fingerprint, e := AutomationEventFingerprint(event, rule)
	if e != nil {
		return nil, e
	}
	var claimID string
	canonicalPayload := canonicalAutomationPayloadValue(event.Payload)
	returnGeneration := automationReturnGeneration(canonicalPayload)
	// Claims backfilled from pre-fingerprint batches use a namespaced legacy
	// fingerprint. Match them semantically before inserting the new SHA claim
	// so a changed transport ID cannot start a second run after migration.
	var existingStatus string
	legacyErr := tx.QueryRow(c, `SELECT status FROM automation_event_claims
		WHERE task_id=$1 AND rule_id=NULLIF($2,'')::uuid
		  AND target_column_id IS NOT DISTINCT FROM NULLIF($3,'')::uuid
		  AND return_generation=$4
		  AND canonical_automation_payload(payload)=canonical_automation_payload($5::jsonb)
		ORDER BY created_at LIMIT 1`, event.TaskID, rule.ID, rule.TargetColumnID, returnGeneration, event.Payload).Scan(&existingStatus)
	if legacyErr == nil {
		if existingStatus == "queued" || existingStatus == "running" {
			return nil, ErrAutomationActive
		}
		return nil, ErrNoRunCreated
	}
	if !errors.Is(legacyErr, pgx.ErrNoRows) {
		return nil, legacyErr
	}
	e = tx.QueryRow(c, `INSERT INTO automation_event_claims(fingerprint,event_id,task_id,rule_id,target_column_id,return_generation,payload)
		VALUES($1,NULLIF($2,'')::uuid,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,$7)
		ON CONFLICT (fingerprint) DO NOTHING RETURNING id`, fingerprint, event.ID, event.TaskID, rule.ID, rule.TargetColumnID,
		returnGeneration, canonicalPayload).Scan(&claimID)
	if errors.Is(e, pgx.ErrNoRows) {
		var claimStatus string
		if statusErr := tx.QueryRow(c, "SELECT status FROM automation_event_claims WHERE fingerprint=$1", fingerprint).Scan(&claimStatus); statusErr != nil {
			return nil, statusErr
		}
		if claimStatus == "queued" || claimStatus == "running" {
			return nil, ErrAutomationActive
		}
		return nil, ErrNoRunCreated
	}
	if e != nil {
		return nil, e
	}
	var cooldown int
	if e = tx.QueryRow(c, "SELECT cooldown_minutes FROM automation_rules WHERE id=$1", rule.ID).Scan(&cooldown); e != nil {
		return nil, e
	}
	if cooldown > 0 {
		var recent bool
		if e = tx.QueryRow(c, `SELECT EXISTS(SELECT 1 FROM agent_run_batches WHERE task_id=$1 AND rule_id=$2 AND created_at>now()-make_interval(mins=>$3))`, event.TaskID, rule.ID, cooldown).Scan(&recent); e != nil {
			return nil, e
		}
		if recent {
			return nil, ErrAutomationActive
		}
	}
	// Events can arrive in quick succession when a task is moved back and
	// forth. Serialize per task/rule and let one active batch own the work.
	if _, e = tx.Exec(c, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", event.TaskID+"|"+rule.ID); e != nil {
		return nil, e
	}
	var active bool
	if e = tx.QueryRow(c, `SELECT EXISTS(SELECT 1 FROM agent_run_batches WHERE task_id=$1 AND rule_id=$2 AND status IN('queued','running'))`, event.TaskID, rule.ID).Scan(&active); e != nil {
		return nil, e
	}
	if active {
		return nil, ErrAutomationActive
	}
	batch, e := createRunBatchTx(c, tx, event.TaskID, a.ID, rule.ID, event.ID, len(targets))
	if e != nil {
		return nil, e
	}
	runs := make([]domain.AgentRun, 0, len(targets))
	for _, target := range targets {
		workspace := target.LocalPath
		if e = lockWorkspaceTx(c, tx, workspace); e != nil {
			return nil, e
		}
		run, created, createErr := createRunTx(c, tx, event.TaskID, a, rule.AgentID, rule.ID, event.ID, batch.ID, workspace, target.ProjectID)
		if createErr != nil {
			return nil, createErr
		}
		if !created {
			continue
		}
		runs = append(runs, run)
	}
	if len(runs) == 0 {
		return nil, ErrNoRunCreated
	}
	if _, e = tx.Exec(c, "UPDATE automation_event_claims SET batch_id=$2,updated_at=now() WHERE id=$1", claimID, batch.ID); e != nil {
		return nil, e
	}
	if e = tx.Commit(c); e != nil {
		return nil, e
	}
	return runs, nil
}

func canonicalAutomationPayloadValue(raw json.RawMessage) any {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return map[string]any{}
	}
	return canonicalAutomationPayload(value)
}

func (s *Store) MarkEventProcessed(c context.Context, id string) error {
	_, e := s.DB.Exec(c, "UPDATE automation_events SET processed_at=now() WHERE id=$1", id)
	return e
}
func (s *Store) RecordEventFailure(c context.Context, id, message string) (int, error) {
	tx, err := s.DB.Begin(c)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(c)
	var attempts int
	err = tx.QueryRow(c, "UPDATE automation_events SET attempts=attempts+1,last_error=$2 WHERE id=$1 AND processed_at IS NULL RETURNING attempts", id, message).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(c, `UPDATE automation_event_claims
		SET status=CASE WHEN status IN ('queued','running') THEN 'failed' ELSE status END,
			attempts=attempts+1,last_error=$2,updated_at=now()
		WHERE event_id=$1 AND status NOT IN ('succeeded','blocked')`, id, message); err != nil {
		return 0, err
	}
	return attempts, tx.Commit(c)
}

// AbandonEvent terminates an unrecoverable event so a broken integration
// cannot starve the bounded pending-event queue forever. The original event
// remains inspectable through its attempts and last_error columns.
func (s *Store) AbandonEvent(c context.Context, event domain.AutomationEvent, message string) (bool, error) {
	tx, err := s.DB.Begin(c)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(c)
	tag, err := tx.Exec(c, "UPDATE automation_events SET processed_at=now(),last_error=$2 WHERE id=$1 AND processed_at IS NULL", event.ID, message)
	if err != nil || tag.RowsAffected() == 0 {
		return false, err
	}
	if _, err = tx.Exec(c, `UPDATE automation_event_claims SET status='blocked',attempts=GREATEST(attempts,(SELECT attempts FROM automation_events WHERE id=$1)),last_error=$2,updated_at=now()
		WHERE event_id=$1 AND status IN ('queued','running','failed')`, event.ID, message); err != nil {
		return false, err
	}
	if event.TaskID != "" {
		if _, err = tx.Exec(c, "INSERT INTO notifications(task_id,kind,message) VALUES($1,'automation_failed',$2)", event.TaskID, "Automation nach mehreren Versuchen beendet: "+message); err != nil {
			return false, err
		}
		if _, err = tx.Exec(c, "INSERT INTO task_comments(task_id,author,body) VALUES($1,'Taskboard',$2)", event.TaskID, "Automationsereignis beendet, damit die Queue nicht blockiert.\n\nUrsache: "+message+"\n\nBitte Task, Regel und Workspace prüfen; ein erneuter Spaltenwechsel löst die Regel erneut aus."); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(c)
}
func (s *Store) SetRunStatus(c context.Context, id, status, summary, errText string) error {
	_, e := s.DB.Exec(c, "UPDATE agent_runs SET status=$2,summary=$3,error_message=$4,started_at=CASE WHEN $2='running' THEN now() ELSE started_at END,finished_at=CASE WHEN $2 IN('succeeded','failed','cancelled') THEN now() ELSE finished_at END WHERE id=$1 AND (status <> 'cancelled' OR $2='cancelled')", id, status, summary, errText)
	return e
}
func (s *Store) SetRunWorktree(c context.Context, id, path string) error {
	_, e := s.DB.Exec(c, "UPDATE agent_runs SET worktree_path=$2 WHERE id=$1", id, path)
	return e
}
func (s *Store) AddRunLog(c context.Context, id, level, message string) error {
	// A run can receive output from the terminal stream, lifecycle handling and
	// a user action at nearly the same time. Serialise only the sequence number
	// for this run so the UNIQUE(agent_run_id, sequence) invariant never turns
	// a best-effort log append into a silently dropped line.
	_, e := s.DB.Exec(c, `WITH held AS (
		SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))
	)
	INSERT INTO agent_run_logs(agent_run_id,sequence,level,message)
	SELECT $1::uuid,COALESCE(max(l.sequence),0)+1,$2,$3
	FROM agent_run_logs l CROSS JOIN held
	WHERE l.agent_run_id=$1::uuid`, id, level, message)
	return e
}

func (s *Store) SetRunSelection(c context.Context, id string, sel domain.RunSelection) error {
	_, err := s.DB.Exec(c, `UPDATE agent_runs SET effective_model=$2,effective_effort=$3,escalation_stage=$4,policy_version=$5,discovery_source=$6,fallback=$7,budget_decision=$8,budget_limit_microusd=$9,selection_cost_microusd=$10 WHERE id=$1`,
		id, sel.Model, sel.Effort, sel.Stage, sel.PolicyVersion, sel.DiscoverySource, sel.Fallback, sel.BudgetDecision, sel.BudgetLimitMicrousd, sel.EstimatedCostMicrousd)
	return err
}

func (s *Store) RunSelection(c context.Context, id string) (domain.RunSelection, error) {
	var sel domain.RunSelection
	err := s.DB.QueryRow(c, `SELECT effective_model,effective_effort,escalation_stage,policy_version,discovery_source,fallback,budget_decision,budget_limit_microusd,selection_cost_microusd FROM agent_runs WHERE id=$1`, id).
		Scan(&sel.Model, &sel.Effort, &sel.Stage, &sel.PolicyVersion, &sel.DiscoverySource, &sel.Fallback, &sel.BudgetDecision, &sel.BudgetLimitMicrousd, &sel.EstimatedCostMicrousd)
	return sel, err
}

func (s *Store) ReworkPolicyState(c context.Context) (domain.ReworkPolicyState, error) {
	var state domain.ReworkPolicyState
	var policy []byte
	err := s.DB.QueryRow(c, `SELECT version,policy,human_escalation_after,budget_limit_microusd,updated_at FROM task_rework_policies WHERE id=TRUE`).
		Scan(&state.Version, &policy, &state.HumanEscalationAfter, &state.BudgetLimitMicrousd, &state.UpdatedAt)
	if err != nil {
		return state, err
	}
	state.Policy = string(policy)
	var parsed struct {
		EstimatedCostMicrousd map[string]int64 `json:"estimated_cost_microusd"`
	}
	if json.Unmarshal(policy, &parsed) == nil {
		state.EstimatedCostMicrousd = parsed.EstimatedCostMicrousd
	}
	return state, nil
}

func (s *Store) SaveReworkPolicyState(c context.Context, version, policyJSON string, humanAfter int, budget int64) error {
	if humanAfter < 0 || budget < 0 {
		return errors.New("rework policy budget and human threshold must be non-negative")
	}
	raw := defaultString(strings.TrimSpace(policyJSON), "{}")
	if !json.Valid([]byte(raw)) {
		return errors.New("rework policy must be valid JSON")
	}
	_, err := s.DB.Exec(c, `INSERT INTO task_rework_policies(id,version,policy,human_escalation_after,budget_limit_microusd,updated_at)
		VALUES(TRUE,$1,$2::jsonb,$3,$4,now())
		ON CONFLICT (id) DO UPDATE SET version=EXCLUDED.version, policy=EXCLUDED.policy, human_escalation_after=EXCLUDED.human_escalation_after, budget_limit_microusd=EXCLUDED.budget_limit_microusd, updated_at=now()`,
		defaultString(strings.TrimSpace(version), "rework-v1"), raw, humanAfter, budget)
	return err
}

// AcquirePublication holds a PostgreSQL transaction-scoped advisory lock for
// one deterministic release identity. It is intentionally exposed as a
// structural adapter for internal/release, so separate worker processes share
// the same idempotency boundary. The returned function rolls back the small
// lock-only transaction and therefore releases the lock without committing any
// application data.
func (s *Store) AcquirePublication(c context.Context, key string) (func(), error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("publication lock key is required")
	}
	tx, err := s.DB.Begin(c)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(c, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", key); err != nil {
		_ = tx.Rollback(c)
		return nil, err
	}
	return func() { _ = tx.Rollback(context.Background()) }, nil
}
