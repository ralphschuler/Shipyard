package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"taskboard/internal/store"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Store is deliberately a narrow service façade. Callers cannot supply a
// partial WHERE clause; every query starts with the complete validated scope.
type Store struct{ db *store.Store }

func New(s *store.Store) *Store { return &Store{db: s} }
func scopeArgs(s Scope) ([]any, error) {
	if !s.Valid() {
		return nil, ErrInvalidScope
	}
	return s.Values(), nil
}

func (s *Store) AppendConversation(ctx context.Context, scope Scope, p Provenance, m ConversationMessage) (string, error) {
	if _, err := scopeArgs(scope); err != nil {
		return "", err
	}
	if !p.Valid(scope) {
		return "", ErrInvalidProvenance
	}
	if m.MessageID != p.MessageID || m.RunID != p.RunID || m.OccurredAt.IsZero() || !m.OccurredAt.Equal(p.OccurredAt) {
		return "", ErrInvalidProvenance
	}
	original := m.Content
	m.Content = Redact(m.Content)
	if m.Content == "" || m.ThreadID == "" || m.Role == "" {
		return "", errors.New("message is incomplete")
	}
	tx, err := s.db.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var id string
	if err = tx.QueryRow(ctx, `INSERT INTO memory_conversations(tenant_id,user_id,project_id,task_id,agent_id,thread_id,message_id,run_id,role,content,content_hash,occurred_at,expires_at,provenance_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`, scope.TenantID, scope.UserID, scope.ProjectID, scope.TaskID, scope.AgentID, m.ThreadID, m.MessageID, m.RunID, m.Role, m.Content, Hash(m.Content), m.OccurredAt, m.ExpiresAt, pJSON(p)).Scan(&id); err != nil {
		return "", err
	}
	if err = s.auditTx(ctx, tx, scope, "stored", id, "ok", map[string]any{"redacted": m.Content != original}); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s *Store) SearchConversation(ctx context.Context, scope Scope, query string, limit int, before time.Time, beforeID string) ([]ConversationMessage, error) {
	if _, err := scopeArgs(scope); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		limit = 25
	}
	rows, err := s.db.DB.Query(ctx, `SELECT id,thread_id,message_id,run_id,role,content,occurred_at,expires_at FROM memory_conversations WHERE tenant_id=$1 AND user_id=$2 AND project_id=$3 AND task_id=$4 AND agent_id=$5 AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at>now()) AND ($6='' OR search_vector @@ plainto_tsquery('simple',$6)) AND ($7::timestamptz IS NULL OR occurred_at<$7 OR (occurred_at=$7 AND id::text<$8)) ORDER BY occurred_at DESC,id DESC LIMIT $9`, scope.TenantID, scope.UserID, scope.ProjectID, scope.TaskID, scope.AgentID, strings.TrimSpace(query), nullableTime(before), beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConversationMessage{}
	for rows.Next() {
		var m ConversationMessage
		if err = rows.Scan(&m.ID, &m.ThreadID, &m.MessageID, &m.RunID, &m.Role, &m.Content, &m.OccurredAt, &m.ExpiresAt); err != nil {
			return nil, err
		}
		m.Content = Redact(m.Content)
		out = append(out, m)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = s.audit(ctx, scope, "searched", "", "ok", map[string]any{"query": query, "count": len(out)}); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) UpsertFact(ctx context.Context, scope Scope, in FactInput, actorID string) (string, error) {
	if _, err := scopeArgs(scope); err != nil {
		return "", err
	}
	if in.MessageID == "" || in.RunID == "" || in.ValidFrom.IsZero() {
		return "", ErrInvalidProvenance
	}
	object, err := NormalizeObject(in.Object)
	if err != nil {
		return "", err
	}
	if in.Confidence < 0 || in.Confidence > 1 {
		return "", errors.New("confidence must be between 0 and 1")
	}
	if actorID == "" {
		return "", errors.New("actor is required")
	}
	object = RedactObject(object)
	tx, err := s.db.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	key := DedupeKey(in.Subject, in.Predicate, object)
	var factID, oldVersion string
	var existingObject []byte
	var version int
	err = tx.QueryRow(ctx, `INSERT INTO memory_facts(tenant_id,user_id,project_id,task_id,agent_id,subject,predicate,object_json,dedupe_key,confidence,high_impact) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(tenant_id,user_id,project_id,task_id,agent_id,dedupe_key) DO NOTHING RETURNING id`, scope.TenantID, scope.UserID, scope.ProjectID, scope.TaskID, scope.AgentID, in.Subject, in.Predicate, object, key, in.Confidence, in.HighImpact).Scan(&factID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id,COALESCE(current_version_id::text,''),COALESCE((SELECT version_no FROM memory_fact_versions v WHERE v.id=current_version_id),0),object_json FROM memory_facts WHERE tenant_id=$1 AND user_id=$2 AND project_id=$3 AND task_id=$4 AND agent_id=$5 AND dedupe_key=$6 FOR UPDATE`, scope.TenantID, scope.UserID, scope.ProjectID, scope.TaskID, scope.AgentID, key).Scan(&factID, &oldVersion, &version, &existingObject)
	} else if err == nil {
		oldVersion = ""
		version = 0
	}
	if err != nil {
		return "", err
	}
	if oldVersion != "" && string(existingObject) == string(object) {
		if err = s.auditTx(ctx, tx, scope, "deduplicated", factID, "ok", map[string]any{"version": version}); err != nil {
			return "", err
		}
		return factID, tx.Commit(ctx)
	}
	if oldVersion != "" {
		if _, err = tx.Exec(ctx, `UPDATE memory_fact_versions SET active=false WHERE id=$1`, oldVersion); err != nil {
			return "", err
		}
		if _, err = tx.Exec(ctx, `UPDATE memory_facts SET status='pending',object_json=$1,confidence=$2,high_impact=$3,updated_at=now() WHERE id=$4`, object, in.Confidence, in.HighImpact, factID); err != nil {
			return "", err
		}
	}
	var vid string
	err = tx.QueryRow(ctx, `INSERT INTO memory_fact_versions(fact_id,version_no,supersedes_version_id,value_json,valid_from,valid_until,confidence,change_reason,message_id,run_id,created_by,provenance_json) VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, factID, version+1, oldVersion, object, in.ValidFrom, in.ValidUntil, in.Confidence, in.ChangeReason, in.MessageID, in.RunID, actorID, pJSON(Provenance{MessageID: in.MessageID, TaskID: scope.TaskID, RunID: in.RunID, AgentID: scope.AgentID, OccurredAt: in.ValidFrom})).Scan(&vid)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE memory_facts SET current_version_id=$1,updated_at=now() WHERE id=$2`, vid, factID); err != nil {
		return "", err
	}
	role, err := s.actorRole(ctx, actorID)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO memory_audit_events(tenant_id,user_id,project_id,task_id,agent_id,action,target_id,actor_id,actor_role,result,metadata_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'ok',$10)`, scope.TenantID, scope.UserID, scope.ProjectID, scope.TaskID, scope.AgentID, func() string {
		if oldVersion != "" {
			return "superseded"
		}
		return "stored"
	}(), factID, actorID, role, `{}`); err != nil {
		return "", err
	}
	return factID, tx.Commit(ctx)
}

func (s *Store) SetFactStatus(ctx context.Context, scope Scope, factID, actorID, status string) error {
	if _, err := scopeArgs(scope); err != nil {
		return err
	}
	if status != "confirmed" && status != "revoked" {
		return errors.New("invalid fact status")
	}
	role, err := s.actorRole(ctx, actorID)
	if err != nil {
		return err
	}
	if role == "viewer" || role == "service" {
		return errors.New("actor is not allowed to change fact status")
	}
	tx, err := s.db.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var highImpact bool
	if err = tx.QueryRow(ctx, `SELECT high_impact FROM memory_facts WHERE tenant_id=$1 AND user_id=$2 AND project_id=$3 AND task_id=$4 AND agent_id=$5 AND id=$6 FOR UPDATE`, scope.TenantID, scope.UserID, scope.ProjectID, scope.TaskID, scope.AgentID, factID).Scan(&highImpact); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `UPDATE memory_facts SET status=$1,updated_at=now() WHERE id=$2 RETURNING id`, status, factID).Scan(&factID); err != nil {
		return err
	}
	if err = s.auditTx(ctx, tx, scope, status, factID, "ok", map[string]any{"actor": actorID, "actor_role": role}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Retain(ctx context.Context, scope Scope) (int64, error) {
	return s.RetainWithPolicy(ctx, scope, RetentionPolicy{})
}

func (s *Store) RetainWithPolicy(ctx context.Context, scope Scope, policy RetentionPolicy) (int64, error) {
	if _, err := scopeArgs(scope); err != nil {
		return 0, err
	}
	policy = policy.normalized()
	tx, err := s.db.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	r, err := tx.Exec(ctx, `DELETE FROM memory_conversations WHERE tenant_id=$1 AND user_id=$2 AND project_id=$3 AND task_id=$4 AND agent_id=$5 AND (expires_at<now() OR occurred_at<$6)`, scope.TenantID, scope.UserID, scope.ProjectID, scope.TaskID, scope.AgentID, time.Now().Add(-policy.ConversationAge))
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM memory_facts WHERE tenant_id=$1 AND user_id=$2 AND project_id=$3 AND task_id=$4 AND agent_id=$5 AND (expires_at<now() OR updated_at<$6)`, scope.TenantID, scope.UserID, scope.ProjectID, scope.TaskID, scope.AgentID, time.Now().Add(-policy.FactAge)); err != nil {
		return 0, err
	}
	if err = s.auditTx(ctx, tx, scope, "retained", "", "ok", map[string]any{"conversation_rows": r.RowsAffected()}); err != nil {
		return 0, err
	}
	return r.RowsAffected(), tx.Commit(ctx)
}

// RetrieveContextPack applies scope predicates in SQL and enforces the hard
// budget after deterministic ordering. Pending high-impact facts never enter.
func (s *Store) RetrieveContextPack(ctx context.Context, scope Scope, query string, budget int) (ContextPack, error) {
	if _, err := scopeArgs(scope); err != nil {
		return ContextPack{}, err
	}
	items := []RetrievalItem{}
	rows, err := s.db.DB.Query(ctx, `SELECT id,content,occurred_at,message_id,run_id FROM memory_conversations WHERE tenant_id=$1 AND user_id=$2 AND project_id=$3 AND task_id=$4 AND agent_id=$5 AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at>now()) AND ($6='' OR search_vector @@ plainto_tsquery('simple',$6)) ORDER BY occurred_at DESC,id DESC LIMIT 100`, scope.TenantID, scope.UserID, scope.ProjectID, scope.TaskID, scope.AgentID, strings.TrimSpace(query))
	if err != nil {
		return ContextPack{}, err
	}
	for rows.Next() {
		var id, text, message, run string
		var at time.Time
		if err = rows.Scan(&id, &text, &at, &message, &run); err != nil {
			rows.Close()
			return ContextPack{}, err
		}
		items = append(items, RetrievalItem{Kind: "conversation", ID: id, Text: text, Source: Provenance{MessageID: message, TaskID: scope.TaskID, RunID: run, AgentID: scope.AgentID, OccurredAt: at}})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return ContextPack{}, err
	}
	rows, err = s.db.DB.Query(ctx, `SELECT f.id,f.subject,f.predicate,f.object_json,f.confidence,v.message_id,v.run_id,v.created_at FROM memory_facts f JOIN memory_fact_versions v ON v.id=f.current_version_id WHERE f.tenant_id=$1 AND f.user_id=$2 AND f.project_id=$3 AND f.task_id=$4 AND f.agent_id=$5 AND f.status IN ('confirmed','pending') AND (NOT f.high_impact OR f.status='confirmed') AND (f.expires_at IS NULL OR f.expires_at>now()) ORDER BY f.confidence DESC,f.updated_at DESC,f.id LIMIT 100`, scope.TenantID, scope.UserID, scope.ProjectID, scope.TaskID, scope.AgentID)
	if err != nil {
		return ContextPack{}, err
	}
	for rows.Next() {
		var id, subject, predicate, message, run string
		var object []byte
		var confidence float64
		var at time.Time
		if err = rows.Scan(&id, &subject, &predicate, &object, &confidence, &message, &run, &at); err != nil {
			rows.Close()
			return ContextPack{}, err
		}
		items = append(items, RetrievalItem{Kind: "fact", ID: id, Text: subject + " " + predicate + " " + string(object), Confidence: confidence, Source: Provenance{MessageID: message, TaskID: scope.TaskID, RunID: run, AgentID: scope.AgentID, OccurredAt: at}})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return ContextPack{}, err
	}
	pack := Fit(items, budget)
	pack.ID = Hash(scope.TenantID + scope.UserID + scope.ProjectID + scope.TaskID + scope.AgentID + query + fmt.Sprint(budget))
	if err = s.audit(ctx, scope, "read", "", "ok", map[string]any{"retrieval_id": pack.ID, "items": len(pack.Items), "budget": budget}); err != nil {
		return ContextPack{}, err
	}
	return pack, nil
}

func (s *Store) DeleteMemory(ctx context.Context, scope Scope) (int64, error) {
	if _, err := scopeArgs(scope); err != nil {
		return 0, err
	}
	tx, err := s.db.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var n int64
	if err = tx.QueryRow(ctx, `WITH deleted AS (DELETE FROM memory_conversations WHERE tenant_id=$1 AND user_id=$2 AND project_id=$3 AND task_id=$4 AND agent_id=$5 RETURNING 1) SELECT count(*) FROM deleted`, scope.TenantID, scope.UserID, scope.ProjectID, scope.TaskID, scope.AgentID).Scan(&n); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM memory_facts WHERE tenant_id=$1 AND user_id=$2 AND project_id=$3 AND task_id=$4 AND agent_id=$5`, scope.TenantID, scope.UserID, scope.ProjectID, scope.TaskID, scope.AgentID); err != nil {
		return 0, err
	}
	if err = s.auditTx(ctx, tx, scope, "deleted", "", "ok", map[string]any{"conversation_rows": n}); err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

func (s *Store) audit(ctx context.Context, sc Scope, action, target, result string, meta map[string]any) error {
	return s.auditTx(ctx, s.db.DB, sc, action, target, result, meta)
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Store) actorRole(ctx context.Context, actorID string) (string, error) {
	return actorRole(ctx, s.db.DB, actorID)
}

func actorRole(ctx context.Context, q queryRower, actorID string) (string, error) {
	if strings.TrimSpace(actorID) == "" {
		return "", errors.New("actor is required")
	}
	var role string
	err := q.QueryRow(ctx, `SELECT role FROM users WHERE id=$1 AND active`, actorID).Scan(&role)
	if err == nil {
		return role, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agents WHERE id=$1 AND retired_at IS NULL)`, actorID).Scan(&exists); err != nil {
			return "", err
		}
		if exists {
			return "service", nil
		}
	}
	return "", err
}

func (s *Store) auditTx(ctx context.Context, q queryRower, sc Scope, action, target, result string, meta map[string]any) error {
	b, _ := json.Marshal(meta)
	role, err := actorRole(ctx, q, sc.UserID)
	if err != nil {
		return err
	}
	qExec, ok := q.(interface {
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	})
	if !ok {
		return errors.New("audit executor is unavailable")
	}
	_, err = qExec.Exec(ctx, `INSERT INTO memory_audit_events(tenant_id,user_id,project_id,task_id,agent_id,action,target_id,actor_id,actor_role,result,metadata_json) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,$2,$8,$9,$10)`, sc.TenantID, sc.UserID, sc.ProjectID, sc.TaskID, sc.AgentID, action, target, role, result, b)
	return err
}
func pJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
