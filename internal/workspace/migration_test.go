package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMigrateIsIdempotentAndLeavesActiveRunInPlace(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
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

func TestMigrationGateBlocksRunValidation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, markerName), []byte("shipyard workspace\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TASKBOARD_WORKSPACE_ROOT", root)
	gate, err := acquireGate(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	status, err := Validate()
	if err == nil || !status.Migration || status.Ready() {
		t.Fatalf("Validate() = %#v, %v; want migration block", status, err)
	}
}
