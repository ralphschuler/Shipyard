package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateIsIdempotentAndLeavesActiveRunInPlace(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(target, markerName), []byte("shipyard workspace\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(source, "projects", "project-1")
	run := filepath.Join(source, "runs", "run-1")
	if err := os.MkdirAll(project, 0o750); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", project, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("project\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(run, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "run.txt"), []byte("keep-running\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := Migrate(source, target, map[string]bool{run: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "projects", "project-1", "README.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "runs", "run-1")); !os.IsNotExist(err) {
		t.Fatalf("active run was copied: %v", err)
	}
	if len(state.Items) != 2 {
		t.Fatalf("migration items = %d, want project plus deferred run", len(state.Items))
	}
	if _, err := Migrate(source, target, map[string]bool{run: true}); err != nil {
		t.Fatal(err)
	}
}

func TestSharedServiceLockDoesNotLookLikeMigration(t *testing.T) {
	markedWorkspace(t)
	gate, err := AcquireRunGate()
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	status, err := Validate()
	if err != nil || !status.Ready() || status.Migration {
		t.Fatalf("Validate() = %#v, %v; shared run/service lock must not look like a migration", status, err)
	}
	second, err := AcquireRunGate()
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Close()
}

func TestExclusiveMigrationLockBlocksPreflightAndNewRuns(t *testing.T) {
	root := markedWorkspace(t)
	gate, err := acquireGate(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	if _, err := AcquireRunGate(); err == nil || !strings.Contains(err.Error(), "Workspace-Migration") {
		t.Fatalf("AcquireRunGate() error = %v, want migration block", err)
	}
	status, err := Validate()
	if err == nil || !status.Migration || status.Ready() || !strings.Contains(status.Error, "Workspace-Migration läuft") {
		t.Fatalf("Validate() = %#v, %v; want migration preflight block", status, err)
	}
}

func TestPreflightClearsAfterMigrationLockReleased(t *testing.T) {
	root := markedWorkspace(t)
	gate, err := acquireGate(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := Validate(); err == nil || !status.Migration {
		t.Fatalf("Validate() = %#v, %v; want block while exclusive lock is held", status, err)
	}
	if err := gate.Close(); err != nil {
		t.Fatal(err)
	}
	status, err := Validate()
	if err != nil || !status.Ready() || status.Migration {
		t.Fatalf("Validate() = %#v, %v; want ready after migration lock is released", status, err)
	}
	runGate, err := AcquireRunGate()
	if err != nil {
		t.Fatal(err)
	}
	_ = runGate.Close()
}

func TestIndependentRunGatesCanBeHeldInParallel(t *testing.T) {
	markedWorkspace(t)
	start := make(chan struct{})
	type result struct {
		gate *RunGate
		err  error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			gate, err := AcquireRunGate()
			results <- result{gate, err}
		}()
	}
	close(start)
	var held []*RunGate
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil {
			t.Fatalf("AcquireRunGate() parallel error = %v", got.err)
		}
		held = append(held, got.gate)
	}
	defer func() {
		for _, gate := range held {
			_ = gate.Close()
		}
	}()
	status, err := Validate()
	if err != nil || !status.Ready() || status.Migration {
		t.Fatalf("Validate() = %#v, %v; parallel shared run gates must leave preflight ready", status, err)
	}
}

func markedWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, markerName), []byte("shipyard workspace\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TASKBOARD_WORKSPACE_ROOT", root)
	return root
}

func TestMigrateRejectsUnmarkedTargetWithoutCreatingLocalFallback(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "projects"), 0o750); err != nil {
		t.Fatal(err)
	}

	_, err := Migrate(source, target, nil)
	if err == nil || !strings.Contains(err.Error(), "markiert") {
		t.Fatalf("Migrate() error = %v, want actionable marker error", err)
	}
	if _, statErr := os.Stat(filepath.Join(target, markerName)); !os.IsNotExist(statErr) {
		t.Fatalf("migration created an operator marker: %v", statErr)
	}
}

func TestMigrateRejectsMissingTargetWithoutCreatingLocalFallback(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "missing-target")
	if err := os.MkdirAll(filepath.Join(source, "projects"), 0o750); err != nil {
		t.Fatal(err)
	}
	_, err := Migrate(source, target, nil)
	if err == nil || !strings.Contains(err.Error(), "verfügbar") {
		t.Fatalf("Migrate() error = %v, want unavailable target diagnostic", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("migration created missing target: %v", statErr)
	}
}

func TestMigrateRejectsNFSMarkerOnNonNFSStorage(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "projects"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, markerName), []byte("shipyard workspace\nstorage=nfs\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	_, err := Migrate(source, target, nil)
	if err == nil || !strings.Contains(err.Error(), "NFS") {
		t.Fatalf("Migrate() error = %v, want NFS storage diagnostic", err)
	}
}

func TestCopyPublishedPreservesExistingDestinationForRollback(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	destination := filepath.Join(parent, "destination")
	if err := os.MkdirAll(source, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "value"), []byte("new\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "value"), []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := copyPublished(source, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "value"))
	if err != nil || string(got) != "new\n" {
		t.Fatalf("published content = %q, %v; want new content", got, err)
	}
	rollback, err := filepath.Glob(destination + ".shipyard-rollback-*")
	if err != nil || len(rollback) != 1 {
		t.Fatalf("rollback copy count = %d, %v; want one preserved rollback", len(rollback), err)
	}
	old, err := os.ReadFile(filepath.Join(rollback[0], "value"))
	if err != nil || string(old) != "old\n" {
		t.Fatalf("rollback content = %q, %v; want old content", old, err)
	}
}

func TestAtomicReplaceDirectoryExchangesExistingDestinationWithoutGap(t *testing.T) {
	parent := t.TempDir()
	oldPath := filepath.Join(parent, "workspace")
	newPath := filepath.Join(parent, "workspace.new")
	if err := os.MkdirAll(oldPath, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newPath, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldPath, "value"), []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newPath, "value"), []byte("new\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	rollback, err := atomicReplaceDirectory(newPath, oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if rollback == "" {
		t.Fatal("atomicReplaceDirectory() returned no recovery path")
	}
	got, err := os.ReadFile(filepath.Join(oldPath, "value"))
	if err != nil || string(got) != "new\n" {
		t.Fatalf("published content = %q, %v; want new content", got, err)
	}
	old, err := os.ReadFile(filepath.Join(rollback, "value"))
	if err != nil || string(old) != "old\n" {
		t.Fatalf("recovery content = %q, %v; want old content", old, err)
	}
}
