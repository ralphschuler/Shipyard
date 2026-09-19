package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"taskboard/internal/domain"
)

type dashboardReaderStub struct {
	dashboard domain.Dashboard
	err       error
}

func (s dashboardReaderStub) DashboardFiltered(context.Context, *time.Time, *time.Time, string, string, string, string) (domain.Dashboard, error) {
	return s.dashboard, s.err
}
func (s dashboardReaderStub) DashboardAttention(context.Context) (domain.DashboardAttention, error) {
	return domain.DashboardAttention{}, s.err
}

func TestDashboardAPIUsesReaderAndPreservesBadRange(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/dashboard?range=invalid", nil)
	w := httptest.NewRecorder()
	dashboardAPI(dashboardReaderStub{}, w, r)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDashboardAPIMapsReaderErrors(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/dashboard", nil)
	w := httptest.NewRecorder()
	dashboardAPI(dashboardReaderStub{err: errors.New("database unavailable")}, w, r)
	if w.Code != 503 {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}
