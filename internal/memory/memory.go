package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

const Redacted = "[REDACTED]"

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bearer\s+|api[_-]?key\s*[:=]\s*|token\s*[:=]\s*|password\s*[:=]\s*)[^\s,;]+`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`\b(sk|pk)_(live|test)_[A-Za-z0-9_-]{12,}\b`),
}

func Redact(s string) string {
	for _, p := range secretPatterns {
		s = p.ReplaceAllString(s, Redacted)
	}
	return s
}

func Hash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

type Scope struct{ TenantID, UserID, ProjectID, TaskID, AgentID string }

func (s Scope) Valid() bool {
	return s.TenantID != "" && s.UserID != "" && s.ProjectID != "" && s.TaskID != "" && s.AgentID != ""
}
func (s Scope) Values() []any { return []any{s.TenantID, s.UserID, s.ProjectID, s.TaskID, s.AgentID} }

type Provenance struct {
	MessageID, TaskID, RunID, AgentID string
	OccurredAt                        time.Time `json:"occurred_at"`
}

func (p Provenance) Valid(scope Scope) bool {
	return p.MessageID != "" && p.RunID != "" && p.TaskID == scope.TaskID && p.AgentID == scope.AgentID && !p.OccurredAt.IsZero()
}

type ConversationMessage struct {
	ID, ThreadID, MessageID, RunID, Role, Content string
	OccurredAt                                    time.Time
	ExpiresAt                                     *time.Time
}
type Fact struct {
	ID, Subject, Predicate, Status string
	Object                         json.RawMessage
	Confidence                     float64
	HighImpact                     bool
	Version                        int
	CurrentVersionID               string
	ExpiresAt                      *time.Time
}
type FactInput struct {
	Subject, Predicate string
	Object             json.RawMessage
	Confidence         float64
	HighImpact         bool
	MessageID, RunID   string
	ValidFrom          time.Time
	ValidUntil         *time.Time
	ChangeReason       string
}
type RetrievalItem struct {
	Kind, ID, Text string
	Source         Provenance
	Confidence     float64
}
type ContextPack struct {
	ID                      string          `json:"retrieval_id"`
	Items                   []RetrievalItem `json:"items"`
	UsedTokens, TokenBudget int
	Truncated               bool `json:"truncated"`
}

func NormalizeObject(raw json.RawMessage) (json.RawMessage, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	return b, err
}
func RedactObject(raw json.RawMessage) json.RawMessage {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return []byte(`"` + Redacted + `"`)
	}
	var walk func(any) any
	walk = func(x any) any {
		switch y := x.(type) {
		case string:
			return Redact(y)
		case []any:
			for i := range y {
				y[i] = walk(y[i])
			}
		case map[string]any:
			for k, v := range y {
				y[k] = walk(v)
			}
		}
		return x
	}
	b, _ := json.Marshal(walk(v))
	return b
}
func DedupeKey(subject, predicate string, object json.RawMessage) string {
	return Hash(strings.ToLower(strings.TrimSpace(subject)) + "\x00" + strings.ToLower(strings.TrimSpace(predicate)) + "\x00" + string(object))
}
func Tokens(s string) int { return len(strings.Fields(s)) }
func Fit(items []RetrievalItem, budget int) ContextPack {
	if budget < 1 {
		budget = 1
	}
	p := ContextPack{TokenBudget: budget}
	for _, it := range items {
		n := Tokens(it.Text)
		if n == 0 || p.UsedTokens+n > budget {
			p.Truncated = true
			continue
		}
		it.Text = Redact(it.Text)
		p.Items = append(p.Items, it)
		p.UsedTokens += n
	}
	return p
}

var ErrInvalidScope = errors.New("memory scope is incomplete")
var ErrInvalidProvenance = errors.New("memory provenance is incomplete or mismatched")
