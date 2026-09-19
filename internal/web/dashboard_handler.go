package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"taskboard/internal/domain"
)

// DashboardReader is the narrow persistence contract used by dashboard HTTP
// handlers. Keeping the store behind this interface makes the handlers
// independently testable and prevents route code from depending on storage
// details.
type DashboardReader interface {
	DashboardFiltered(context.Context, *time.Time, *time.Time, string, string, string, string) (domain.Dashboard, error)
	DashboardAttention(context.Context) (domain.DashboardAttention, error)
}

func dashboardAPI(reader DashboardReader, w http.ResponseWriter, r *http.Request) {
	from, to, err := dashboardRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dashboard, err := reader.DashboardFiltered(r.Context(), from, to, r.URL.Query().Get("provider"), r.URL.Query().Get("model"), r.URL.Query().Get("agent"), r.URL.Query().Get("board"))
	if err != nil {
		http.Error(w, "Dashboard-Daten sind momentan nicht verfügbar.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dashboard)
}

func dashboardAttentionAPI(reader DashboardReader, w http.ResponseWriter, r *http.Request) {
	attention, err := reader.DashboardAttention(r.Context())
	if err != nil {
		http.Error(w, "Dashboard-Daten sind momentan nicht verfügbar.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(attention)
}
