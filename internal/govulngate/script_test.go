package govulngate

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

func TestTaskboardDoesNotImportOpenPGP(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("go", "list", "-deps", "-e", "-f", "{{.ImportPath}}", "./cmd/taskboard")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list deps: %v\n%s", err, out)
	}
	var argon2 bool
	for _, path := range strings.Split(string(out), "\n") {
		path = strings.TrimSpace(path)
		if path == "golang.org/x/crypto/argon2" {
			argon2 = true
		}
		if strings.Contains(path, "openpgp") {
			t.Fatalf("taskboard must not import OpenPGP packages; found %s", path)
		}
	}
	if !argon2 {
		t.Fatal("expected golang.org/x/crypto/argon2 to remain the password-hashing dependency")
	}
}
