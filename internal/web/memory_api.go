package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"taskboard/internal/memory"
	"time"
)

func (a *App) memoryScope(r *http.Request) (memory.Scope, error) {
	u, ok := currentUser(r.Context())
	if !ok || !u.Active {
		return memory.Scope{}, errors.New("authentication required")
	}
	scope := memory.Scope{
		TenantID:  r.URL.Query().Get("tenant_id"),
		UserID:    u.ID,
		ProjectID: r.URL.Query().Get("project_id"),
		TaskID:    r.URL.Query().Get("task_id"),
		AgentID:   r.URL.Query().Get("agent_id"),
	}
	if !scope.Valid() {
		return memory.Scope{}, errors.New("tenant_id, project_id, task_id and agent_id are required")
	}
	return scope, nil
}

func (a *App) memoryAPI(w http.ResponseWriter, r *http.Request) {
	scope, err := a.memoryScope(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 25
	}
	before, _ := time.Parse(time.RFC3339Nano, r.URL.Query().Get("before"))
	items, err := a.memory.SearchConversation(r.Context(), scope, r.URL.Query().Get("q"), limit, before, r.URL.Query().Get("before_id"))
	if err != nil {
		http.Error(w, "memory search unavailable", http.StatusServiceUnavailable)
		return
	}
	budget, _ := strconv.Atoi(r.URL.Query().Get("budget"))
	if budget < 1 || budget > 10000 {
		budget = 2000
	}
	pack, err := a.memory.RetrieveContextPack(r.Context(), scope, r.URL.Query().Get("q"), budget)
	if err != nil {
		http.Error(w, "memory retrieval unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var nextBefore string
	var nextBeforeID string
	if len(items) == limit {
		last := items[len(items)-1]
		nextBefore = last.OccurredAt.Format(time.RFC3339Nano)
		nextBeforeID = last.ID
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"messages": items, "context": pack, "next_before": nextBefore, "next_before_id": nextBeforeID})
}

func (a *App) createMemoryFactAPI(w http.ResponseWriter, r *http.Request) {
	scope, err := a.memoryScope(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u, _ := currentUser(r.Context())
	var input memory.FactInput
	if err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		http.Error(w, "invalid fact JSON", http.StatusBadRequest)
		return
	}
	id, err := a.memory.UpsertFact(r.Context(), scope, input, u.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
}

func (a *App) setMemoryFactStatusAPI(w http.ResponseWriter, r *http.Request) {
	scope, err := a.memoryScope(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u, _ := currentUser(r.Context())
	var body struct {
		Status string `json:"status"`
	}
	if err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid status JSON", http.StatusBadRequest)
		return
	}
	if err = a.memory.SetFactStatus(r.Context(), scope, r.PathValue("id"), u.ID, body.Status); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) deleteMemoryAPI(w http.ResponseWriter, r *http.Request) {
	scope, err := a.memoryScope(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	n, err := a.memory.DeleteMemory(r.Context(), scope)
	if err != nil {
		http.Error(w, "memory deletion unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int64{"deleted_messages": n})
}

func (a *App) retainMemoryAPI(w http.ResponseWriter, r *http.Request) {
	scope, err := a.memoryScope(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := a.memory.RetainWithPolicyStats(r.Context(), scope, memory.RetentionPolicy{})
	if err != nil {
		http.Error(w, "memory retention unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
