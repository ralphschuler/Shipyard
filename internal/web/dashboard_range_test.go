package web

import (
	"net/http"
	"testing"
	"time"
)

func TestDashboardRangeCustomIsHalfOpenAndDateBased(t *testing.T) {
	r, err := http.NewRequest(http.MethodGet, "/?range=custom&from=2026-09-01&to=2026-09-03", nil)
	if err != nil {
		t.Fatal(err)
	}
	from, to, err := dashboardRange(r)
	if err != nil {
		t.Fatalf("custom range rejected: %v", err)
	}
	if got, want := from.Format("2006-01-02"), "2026-09-01"; got != want {
		t.Fatalf("from = %s, want %s", got, want)
	}
	if got, want := to.Format("2006-01-02"), "2026-09-04"; got != want {
		t.Fatalf("to = %s, want exclusive next day %s", got, want)
	}
	if !to.After(*from) || to.Sub(*from) != 72*time.Hour {
		t.Fatalf("custom range is not a three-day half-open interval: %v - %v", *from, *to)
	}
}

func TestDashboardRangeRejectsInvalidCustomRange(t *testing.T) {
	for _, query := range []string{
		"range=custom&from=2026-09-03&to=2026-09-03",
		"range=custom&from=2026-09-04&to=2026-09-03",
		"range=custom&from=not-a-date&to=2026-09-03",
	} {
		r, err := http.NewRequest(http.MethodGet, "/?"+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := dashboardRange(r); err == nil {
			t.Fatalf("invalid query accepted: %s", query)
		}
	}
}
