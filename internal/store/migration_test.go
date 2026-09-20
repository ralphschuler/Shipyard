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

func TestPersistentRunQueueMigrationStoresWakeAndWaitState(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/047_persistent_run_queue.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"queue_wait_started_at", "queue_wait_reason", "queue_next_attempt_at", "agent_runs_queue_ready", "WHERE status = 'queued'"} {
		if !strings.Contains(text, required) {
			t.Fatalf("persistent queue migration is missing %q", required)
		}
	}
}

func TestSandboxEffectivePolicyMigrationBackfillsLegacyRuns(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/050_backfill_sandbox_effective_policy.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"UPDATE agent_runs",
		"sandbox_effective = jsonb_build_object",
		"sandbox_profiles",
		"sandbox_effective = '{}'::jsonb",
		"network_mode",
		"write_mode",
		"WHERE p.name = r.sandbox_profile",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("sandbox compatibility migration is missing %q", required)
		}
	}
}

func TestRunStartSHAMigrationPersistsDiffBase(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/051_run_start_sha.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"run_start_sha TEXT NOT NULL DEFAULT ''", "ADD COLUMN IF NOT EXISTS"} {
		if !strings.Contains(text, required) {
			t.Fatalf("run start SHA migration is missing %q", required)
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

func TestTaskTargetInheritanceMigrationIsIdempotent(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/048_task_target_inheritance.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "ADD COLUMN IF NOT EXISTS target_source") {
		t.Fatal("task target inheritance migration must tolerate a pre-existing target_source column")
	}
	if !strings.Contains(text, "CREATE INDEX IF NOT EXISTS task_repository_targets_inherited") {
		t.Fatal("task target inheritance migration must tolerate a pre-existing index")
	}
}

func TestReleasePublicationMigrationMakesFinalizationDurableAndUnique(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/049_release_publications.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"release_publications", "task_id", "project_id", "run_id", "UNIQUE(task_id, project_id, run_id)", "pr_url", "audit_recorded", "comment_recorded"} {
		if !strings.Contains(text, required) {
			t.Fatalf("release publication migration is missing %q", required)
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

func TestGitIntegrationQueueMigrationSupportsLeasedClaims(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/042_git_integration_queue.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"claimed_until TIMESTAMPTZ", "status IN ('queued','running','pushed','pr_open')", "repository_integration_queue_claim"} {
		if !strings.Contains(text, required) {
			t.Fatalf("git integration queue migration is missing %q", required)
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

func TestAgentWorkspaceMigrationRemovesOnlyProfileWorkspace(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/048_remove_agent_workspace.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "ALTER TABLE agents DROP COLUMN IF EXISTS workspace_path") {
		t.Fatal("agent migration must remove the profile workspace column")
	}
	if strings.Contains(text, "agent_runs") || strings.Contains(text, "workspace_snapshot") {
		t.Fatal("agent migration must preserve historical run snapshots")
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
		"CREATE TRIGGER memory_fact_versions_append_only BEFORE UPDATE OR DELETE ON memory_fact_versions",
		"current_version_id UUID", "high_impact BOOLEAN",
		"memory_fact_versions_current_validity", "memory_facts_retention",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("memory migration missing %q", required)
		}
	}
}

func TestMemoryHardeningAllowsOnlyExplicitRetentionDeletes(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/048_memory_hardening.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"shipyard.memory_delete", "TG_OP = 'DELETE'", "memory_fact_versions_append_only"} {
		if !strings.Contains(text, required) {
			t.Fatalf("memory hardening migration missing %q", required)
		}
	}
}

func TestMemoryRetentionMigrationAddsScopedAgeIndexes(t *testing.T) {
	body, err := migrationFiles.ReadFile("migrations/049_memory_retention_indexes.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{"memory_conversations_retention_scope", "memory_facts_retention_updated", "updated_at"} {
		if !strings.Contains(text, required) {
			t.Fatalf("memory retention migration missing %q", required)
		}
	}
}
