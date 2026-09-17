package store

import (
	"context"
	"taskboard/internal/domain"
	"testing"
	"time"
)

// These opt-in integration checks deliberately use an invalid actor UUID. The
// audit insert must fail, and the surrounding transaction must leave the
// catalog mutation untouched.
func TestUsagePriceCreateWithAuditRollsBackOnAuditFailure(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	p := testUsagePrice("atomic-create")
	if err := s.SaveUsagePriceWithAudit(ctx, p, "not-a-uuid"); err == nil {
		t.Fatal("expected audit failure")
	}
	prices, err := s.UsagePrices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range prices {
		if candidate.Version == p.Version {
			t.Fatal("price mutation survived failed audit")
		}
	}
}

func TestUsagePriceUpdateWithAuditRollsBackOnAuditFailure(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	p := testUsagePrice("atomic-update")
	if err := s.SaveUsagePrice(ctx, p); err != nil {
		t.Fatal(err)
	}
	p = storedTestPrice(t, s, ctx, p.Version)
	t.Cleanup(func() { _ = s.DeleteUsagePrice(ctx, p.ID) })
	updated := p
	updated.Version = "atomic-update-new"
	if err := s.UpdateUsagePriceWithAudit(ctx, updated, "not-a-uuid"); err == nil {
		t.Fatal("expected audit failure")
	}
	prices, err := s.UsagePrices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range prices {
		if candidate.ID == p.ID && candidate.Version != p.Version {
			t.Fatal("price update survived failed audit")
		}
	}
}

func TestUsagePriceDeleteWithAuditRollsBackOnAuditFailure(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	p := testUsagePrice("atomic-delete")
	if err := s.SaveUsagePrice(ctx, p); err != nil {
		t.Fatal(err)
	}
	p = storedTestPrice(t, s, ctx, p.Version)
	t.Cleanup(func() { _ = s.DeleteUsagePrice(ctx, p.ID) })
	if err := s.DeleteUsagePriceWithAudit(ctx, p.ID, "not-a-uuid"); err == nil {
		t.Fatal("expected audit failure")
	}
	prices, err := s.UsagePrices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range prices {
		if candidate.ID == p.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("price deletion survived failed audit")
	}
}

func testUsagePrice(version string) domain.UsagePrice {
	rate := int64(1)
	return domain.UsagePrice{
		Provider: "test", Model: "test-model", ServiceTier: "standard",
		Version: version + "-" + time.Now().UTC().Format("20060102150405.000000000"), ValidFrom: time.Now().UTC().Add(-time.Minute),
		Input: &rate,
	}
}

func storedTestPrice(t *testing.T, s *Store, ctx context.Context, version string) domain.UsagePrice {
	t.Helper()
	prices, err := s.UsagePrices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range prices {
		if p.Version == version {
			return p
		}
	}
	t.Fatalf("stored test price %q not found", version)
	return domain.UsagePrice{}
}
