package govulngate

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestRequireGoToolchainScriptRejectsUnpatchedVersion(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("bash", "scripts/require-go-toolchain.sh", "--version", "go1.26.0")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("go1.26.0 must fail the patched-toolchain gate: %s", out)
	}
	cmd = exec.Command("bash", "scripts/require-go-toolchain.sh", "--version", "go1.26.8")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go1.26.8 must pass the patched-toolchain gate: %s", out)
	}
	cmd = exec.Command("bash", "scripts/go-toolchain-selftest.sh")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("toolchain self-test failed: %s", out)
	}
}
