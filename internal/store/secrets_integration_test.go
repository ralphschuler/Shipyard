package store

import (
	"context"
	"testing"
	"time"
)

// TestSecretAssignmentSerializesWithReplacement exercises the lock ordering
// used by SetSecretAgents and ReplaceSecret. It is opt-in because it needs a
// PostgreSQL database (see integrationStore).
func TestSecretAssignmentSerializesWithReplacement(t *testing.T) {
	s := integrationStore(t)
	t.Setenv("SHIPYARD_SECRET_KEY", "integration-secret-key")
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405000000000")
	agent, err := s.CreateAgent(ctx, "Secret lock agent "+suffix, "integration", "", "", "", t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteAgent(ctx, agent.ID) })

	active, err := s.CreateSecret(ctx, "", "active-"+suffix, "", "LOCK_TEST_TOKEN", "active-value")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteSecret(ctx, "", active.ID) })
	if err = s.SetSecretAgents(ctx, "", active.ID, []string{agent.ID}); err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateSecret(ctx, "", "target-"+suffix, "", "TARGET_TEST_TOKEN", "target-value")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteSecret(ctx, "", target.ID) })
	if err = s.RevokeSecret(ctx, "", target.ID); err != nil {
		t.Fatal(err)
	}

	lockTx, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback(ctx)
	var lockedID string
	if err = lockTx.QueryRow(ctx, "SELECT id::text FROM secrets WHERE id=$1 FOR UPDATE", target.ID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- s.SetSecretAgents(ctx, "", target.ID, []string{agent.ID})
	}()
	select {
	case err = <-result:
		t.Fatalf("assignment bypassed the replacement lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = lockTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}

	if err = s.ReplaceSecret(ctx, "", target.ID, "target-replacement"); err != nil {
		t.Fatalf("replacement failed after serialized assignment: %v", err)
	}
	values, err := s.SecretValuesForAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].ID != active.ID || values[0].Value != "active-value" || values[1].ID != target.ID || values[1].Value != "target-replacement" {
		t.Fatalf("concurrent assignment/reactivation exposed an invalid active set: %#v", values)
	}
	if lockedID != target.ID {
		t.Fatalf("target secret was not locked: %q", lockedID)
	}

	// Run the two mutators concurrently as well. Exactly one operation may win
	// the lock/order race; the losing operation must observe the active
	// env_name conflict, so the agent can never end up with two active values.
	if err = s.RevokeSecret(ctx, "", target.ID); err != nil {
		t.Fatal(err)
	}
	concurrent, err := s.CreateSecret(ctx, "", "concurrent-"+suffix, "", "TARGET_TEST_TOKEN", "concurrent-value")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteSecret(ctx, "", concurrent.ID) })
	if err = s.RevokeSecret(ctx, "", concurrent.ID); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		results <- s.SetSecretAgents(ctx, "", concurrent.ID, []string{agent.ID})
	}()
	go func() {
		<-start
		results <- s.ReplaceSecret(ctx, "", concurrent.ID, "concurrent-replacement")
	}()
	close(start)
	first, second := <-results, <-results
	if first == nil && second == nil {
		t.Fatal("concurrent assignment and replacement both succeeded")
	}
	values, err = s.SecretValuesForAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	activeNames := make(map[string]bool)
	for _, value := range values {
		activeNames[value.EnvName] = true
	}
	if len(values) != 2 || !activeNames["LOCK_TEST_TOKEN"] || !activeNames["TARGET_TEST_TOKEN"] {
		t.Fatalf("concurrent mutation produced an invalid active set: %#v (errors: %v, %v)", values, first, second)
	}
}

func TestSecretReplacementRollsBackWhenAuditFails(t *testing.T) {
	s := integrationStore(t)
	t.Setenv("SHIPYARD_SECRET_KEY", "integration-secret-key")
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405000000000")
	secret, err := s.CreateSecret(ctx, "", "rollback-"+suffix, "", "ROLLBACK_TOKEN", "original-value")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteSecret(ctx, "", secret.ID) })

	if err = s.ReplaceSecret(ctx, "not-a-uuid", secret.ID, "replacement-value"); err == nil {
		t.Fatal("expected replacement audit failure")
	}
	values, err := s.SecretValuesForAgent(ctx, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 {
		t.Fatalf("unassigned secret unexpectedly returned: %#v", values)
	}
	// The encrypted payload is intentionally checked through the authorized
	// path only; assigning it after the failed mutation must reveal the original.
	if err = s.SetSecretAgents(ctx, "", secret.ID, []string{"00000000-0000-0000-0000-000000000000"}); err != nil {
		t.Fatal(err)
	}
	values, err = s.SecretValuesForAgent(ctx, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Value != "original-value" {
		t.Fatalf("failed-audit replacement was not rolled back: %#v", values)
	}
}
