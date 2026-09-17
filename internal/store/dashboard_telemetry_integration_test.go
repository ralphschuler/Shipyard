package store

import (
	"context"
	"testing"
)

func TestDashboardFilteredUnboundedIncludesTelemetrySeries(t *testing.T) {
	s := integrationStore(t)

	dashboard, err := s.DashboardFiltered(context.Background(), nil, nil, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(dashboard.TelemetrySeries) == 0 {
		t.Fatal("unbounded dashboard omitted telemetry series")
	}
}
