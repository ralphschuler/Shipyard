package store

import (
	"strings"
	"testing"
)

func TestActiveBatchMigrationsProtectManualAndAutomationRuns(t *testing.T) {
	for _, name := range []string{"migrations/024_active_automation_batch.sql", "migrations/025_active_manual_batch.sql"} {
		body, err := migrationFiles.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(body)
		if !strings.Contains(text, "CREATE UNIQUE INDEX") || !strings.Contains(text, "status IN ('queued','running')") {
			t.Fatalf("%s does not protect active batches: %s", name, text)
		}
	}
}

func TestAgentTransitionSourceMigrationAdmitsWorkerSources(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/034_agent_transition_sources.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"agent_review", "agent_interaction"} {
		if !strings.Contains(string(body), source) {
			t.Fatalf("migration must admit %q", source)
		}
	}
}

func TestWorkflowColumnTypesMigrationKeepsSpecialRolesUnique(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/028_workflow_column_types.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"column_type IN ('standard', 'inbox', 'done', 'needs_action')", "one_special_workflow_column_type_per_board", "WHERE column_type <> 'standard'"} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing %q in column type migration", required)
		}
	}
}

func TestNeedsActionBackfillUsesSemanticType(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/029_backfill_needs_action_column_type.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "column_type = 'needs_action'") {
		t.Fatal("needs-action backfill is missing")
	}
}

func TestLegacyInteractionMigrationClosesDuplicatesWithoutDeletingHistory(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/031_deduplicate_legacy_agent_interactions.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"row_number() OVER", "status = 'cancelled'", "task_decisions", "UPDATE agent_interactions"} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing %q", required)
		}
	}
}

func TestSessionHygieneMigrationSupportsBoundedCleanup(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/035_session_hygiene.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"user_sessions_expires_at_idx", "user_sessions_user_created_idx", "created_at DESC"} {
		if !strings.Contains(string(body), required) {
			t.Fatalf("session hygiene migration is missing %q", required)
		}
	}
}

func TestAcceptedDeliveryCommitMigrationStoresGitObjectIdentity(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/041_accepted_delivery_commits.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"accepted_commit_sha TEXT", "source_workspace, accepted_commit_sha", "user-controlled"} {
		if !strings.Contains(text, required) {
			t.Fatalf("accepted delivery migration is missing %q", required)
		}
	}
}

func TestAutomationFingerprintMigrationHasAtomicDurableClaim(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/042_automation_event_fingerprints.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"automation_event_claims", "fingerprint TEXT NOT NULL UNIQUE", "status TEXT NOT NULL", "attempts INTEGER", "batch_id UUID", "canonical_automation_payload", "legacy:' || b.id::text", "legacy:run:' || r.id::text", "agent_runs", "ON CONFLICT (fingerprint) DO NOTHING"} {
		if !strings.Contains(text, required) {
			t.Fatalf("fingerprint migration is missing %q", required)
		}
	}
}

func TestAutomationPayloadRepairMigrationRecreatesCanonicalFunction(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/044_repair_automation_payload_function.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"CREATE OR REPLACE FUNCTION canonical_automation_payload", "jsonb_object_agg", "jsonb_array_elements", "transport_id"} {
		if !strings.Contains(text, required) {
			t.Fatalf("repair migration is missing %q", required)
		}
	}
}

func TestRetireAgentsMigrationPreservesHistoricalIdentities(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/045_retire_agents.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"retired_at TIMESTAMPTZ", "DROP CONSTRAINT IF EXISTS agents_name_key", "agents_active_name_key", "WHERE retired_at IS NULL"} {
		if !strings.Contains(text, required) {
			t.Fatalf("retired-agent migration is missing %q", required)
		}
	}
}

func TestAgentMemoryMigrationEnforcesScopeHistoryAndActiveVersion(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/046_agent_memory.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"memory_conversations", "memory_audit_events", "provenance_json", "search_vector",
		"UNIQUE(tenant_id,user_id,project_id,task_id,agent_id,dedupe_key)",
		"CREATE UNIQUE INDEX memory_one_active_version ON memory_fact_versions(fact_id) WHERE active",
		"current_version_id UUID", "high_impact BOOLEAN",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("memory migration missing %q", required)
		}
	}
}
