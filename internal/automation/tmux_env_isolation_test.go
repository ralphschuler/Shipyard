package automation

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	reviewFirstKey   = "REVIEW_ASSIGNED_ONLY_TO_FIRST"
	reviewSecondKey  = "REVIEW_ASSIGNED_ONLY_TO_SECOND"
	reviewFirstValue = "sec05-first-assigned-token-aaaa"
	reviewSecondVal  = "sec05-second-assigned-token-bbbb"
	reviewOldValue   = "sec05-revoked-old-token-cccc"
	reviewNewValue   = "sec05-reassigned-token-dddd"
)

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is required for overlapping-run secret isolation tests")
	}
}

func isolatedTmuxWorker(t *testing.T) *Worker {
	t.Helper()
	requireTmux(t)
	socket := fmt.Sprintf("s05%x", time.Now().UnixNano())
	w := &Worker{tmuxServer: socket, runLogsDir: t.TempDir()}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	})
	return w
}

func reviewAgentEnv(assignments ...string) []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/bin:/bin"
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}
	env := []string{"PATH=" + path, "HOME=" + home, "TERM=dumb"}
	return append(env, assignments...)
}

func envDumpArgs(dumpDir string, sleepSeconds int) (string, []string) {
	script := `umask 077
printenv > "$1"
tr '\0' '\n' < /proc/self/cmdline > "$2"
tr '\0' '\n' < /proc/self/environ > "$3"
tr '\0' '\n' < /proc/$PPID/cmdline > "$4"
if [ -n "$5" ]; then
  exec sleep "$5"
fi
`
	sleep := ""
	if sleepSeconds > 0 {
		sleep = strconv.Itoa(sleepSeconds)
	}
	return "/bin/sh", []string{
		"-c", script, "env-dump",
		filepath.Join(dumpDir, "printenv"),
		filepath.Join(dumpDir, "argv"),
		filepath.Join(dumpDir, "environ"),
		filepath.Join(dumpDir, "parent-argv"),
		sleep,
	}
}

func waitFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return data
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for dump file")
	return nil
}

func parsePrintenv(data []byte) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

func assertAssignedOnly(t *testing.T, env map[string]string, assignedKey, assignedValue, otherKey string) {
	t.Helper()
	if got, ok := env[assignedKey]; !ok || got != assignedValue {
		t.Fatal("run did not receive only its assigned secret")
	}
	if _, ok := env[otherKey]; ok {
		t.Fatal("run inherited a secret that was not assigned to it")
	}
}

func assertNoSecretValues(t *testing.T, data []byte, values ...string) {
	t.Helper()
	for _, value := range values {
		if value != "" && bytes.Contains(data, []byte(value)) {
			t.Fatal("secret value appeared in process arguments or diagnostic output")
		}
	}
}

func startKeepAliveRun(t *testing.T, w *Worker, runID, workDir string, env []string) (dumpDir string, cancel context.CancelFunc) {
	t.Helper()
	dumpDir = filepath.Join(workDir, runID+"-dump")
	if err := os.MkdirAll(dumpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	command, args := envDumpArgs(dumpDir, 60)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := w.runInTmux(ctx, runID, workDir, command, args, "", "", env)
		errCh <- err
	}()
	waitFile(t, filepath.Join(dumpDir, "printenv"))
	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
		}
	})
	return dumpDir, cancel
}

func runToCompletion(t *testing.T, w *Worker, runID, workDir string, env []string) string {
	t.Helper()
	dumpDir := filepath.Join(workDir, runID+"-dump")
	if err := os.MkdirAll(dumpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	command, args := envDumpArgs(dumpDir, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := w.runInTmux(ctx, runID, workDir, command, args, "", "", env); err != nil {
		t.Fatalf("finishing run failed: %v", err)
	}
	waitFile(t, filepath.Join(dumpDir, "printenv"))
	return dumpDir
}

func secretValues() []string {
	return []string{reviewFirstValue, reviewSecondVal, reviewOldValue, reviewNewValue}
}

func assertDumpIsolated(t *testing.T, dumpDir string, assignedKey, assignedValue, otherKey string) {
	t.Helper()
	env := parsePrintenv(waitFile(t, filepath.Join(dumpDir, "printenv")))
	assertAssignedOnly(t, env, assignedKey, assignedValue, otherKey)
	for _, name := range []string{"argv", "parent-argv"} {
		assertNoSecretValues(t, waitFile(t, filepath.Join(dumpDir, name)), secretValues()...)
	}
}

func assertTmuxServerOmitsSecrets(t *testing.T, socket string) {
	t.Helper()
	out, err := exec.Command("tmux", "-L", socket, "list-sessions", "-F", "#{pid}").Output()
	if err != nil {
		t.Fatalf("tmux server pid: %v", err)
	}
	pid := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	if pid == "" {
		t.Fatal("tmux server pid was empty")
	}
	cmdline, err := os.ReadFile(filepath.Join("/proc", pid, "cmdline"))
	if err != nil {
		t.Fatal(err)
	}
	environ, err := os.ReadFile(filepath.Join("/proc", pid, "environ"))
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecretValues(t, cmdline, secretValues()...)
	assertNoSecretValues(t, environ, secretValues()...)
}

func TestTmuxClientEnvironmentOmitsServiceAndRunSecrets(t *testing.T) {
	t.Setenv(reviewFirstKey, reviewFirstValue)
	t.Setenv("OPENAI_API_KEY", reviewSecondVal)
	t.Setenv("TASKBOARD_MCP_TOKEN", reviewOldValue)
	env := strings.Join(tmuxClientEnvironment(), "\n")
	assertNoSecretValues(t, []byte(env), secretValues()...)
	if strings.Contains(env, reviewFirstKey) || strings.Contains(env, "OPENAI_API_KEY") || strings.Contains(env, "TASKBOARD_MCP_TOKEN") {
		t.Fatal("tmux client environment listed a secret variable name")
	}
}

func TestTmuxWindowCommandDoesNotEmbedSecretValues(t *testing.T) {
	command := tmuxWindowCommand("/bin/bash", "/tmp/runner", "/tmp/args", "/tmp/exit", "/tmp/stdin", "/tmp/out", "/tmp/env")
	assertNoSecretValues(t, []byte(command), secretValues()...)
	if strings.Contains(command, reviewFirstKey) {
		t.Fatal("window command embedded a secret variable name")
	}
}

func TestTmuxRunnerRebuildsEnvironmentWithoutInheritedSecrets(t *testing.T) {
	dir := t.TempDir()
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is required")
	}
	runnerPath := filepath.Join(dir, "runner")
	if err := os.WriteFile(runnerPath, []byte(tmuxRunnerScript(bashPath)), 0o700); err != nil {
		t.Fatal(err)
	}
	argsPath := filepath.Join(dir, "args")
	exitPath := filepath.Join(dir, "exit")
	envPath := filepath.Join(dir, "env")
	dumpPath := filepath.Join(dir, "printenv")
	if err := os.WriteFile(argsPath, nulTerminated([]string{"/bin/sh", "-c", "printenv > \"$1\"", "dump", dumpPath}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, nulTerminated(reviewAgentEnv(reviewSecondKey+"="+reviewSecondVal)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bashPath, runnerPath, argsPath, exitPath, "", "", envPath)
	cmd.Env = append(tmuxClientEnvironment(), reviewFirstKey+"="+reviewFirstValue)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("runner failed: %v %s", runErr, out)
	}
	env := parsePrintenv(waitFile(t, dumpPath))
	assertAssignedOnly(t, env, reviewSecondKey, reviewSecondVal, reviewFirstKey)
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatal("env file was left on disk after the runner loaded it")
	}
}

func TestProductionTmuxLaunchDoesNotUseGlobalSetEnvironment(t *testing.T) {
	src, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(src, []byte("set-environment")) {
		t.Fatal("secrets must not be distributed via tmux global environment updates")
	}
	if !bytes.Contains(src, []byte("start.Env = tmuxClientEnvironment()")) {
		t.Fatal("tmux clients must not receive the agent secret environment")
	}
	if !bytes.Contains(src, []byte("exec -c")) {
		t.Fatal("runner must drop the inherited tmux environment before loading the run env file")
	}
	if !bytes.Contains(src, []byte("nulTerminated(env)")) {
		t.Fatal("run environment must be transported through the per-run env file")
	}
}

func TestOverlappingTmuxRunsIsolateSecrets(t *testing.T) {
	cases := []struct {
		name                   string
		keepKey, keepValue     string
		finishKey, finishValue string
	}{
		{name: "A then B", keepKey: reviewFirstKey, keepValue: reviewFirstValue, finishKey: reviewSecondKey, finishValue: reviewSecondVal},
		{name: "B then A", keepKey: reviewSecondKey, keepValue: reviewSecondVal, finishKey: reviewFirstKey, finishValue: reviewFirstValue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := isolatedTmuxWorker(t)
			workDir := t.TempDir()
			keepDump, _ := startKeepAliveRun(t, w, "keep", workDir, reviewAgentEnv(tc.keepKey+"="+tc.keepValue))
			finishDump := runToCompletion(t, w, "finish", workDir, reviewAgentEnv(tc.finishKey+"="+tc.finishValue))
			assertDumpIsolated(t, keepDump, tc.keepKey, tc.keepValue, tc.finishKey)
			assertDumpIsolated(t, finishDump, tc.finishKey, tc.finishValue, tc.keepKey)
			assertTmuxServerOmitsSecrets(t, w.tmuxServer)
			logData, _ := os.ReadFile(filepath.Join(w.runLogsDir, "keep.log"))
			logData = append(logData, mustRead(t, filepath.Join(w.runLogsDir, "finish.log"))...)
			assertNoSecretValues(t, logData, secretValues()...)
		})
	}
}

func TestPreexistingTmuxServerDoesNotLeakOrBlockAssignedSecrets(t *testing.T) {
	w := isolatedTmuxWorker(t)
	workDir := t.TempDir()
	seed := exec.Command("tmux", "-L", w.tmuxServer, "new-session", "-d", "-s", "seed-preexisting", "--", "/bin/sleep", "60")
	seed.Env = append(tmuxClientEnvironment(), reviewFirstKey+"="+reviewFirstValue)
	if out, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("pre-existing tmux server: %s", strings.TrimSpace(string(out)))
	}
	dump := runToCompletion(t, w, "second", workDir, reviewAgentEnv(reviewSecondKey+"="+reviewSecondVal))
	assertDumpIsolated(t, dump, reviewSecondKey, reviewSecondVal, reviewFirstKey)
}

func TestSecretRevokeAndReassignmentApplyWithoutRestartingTmuxServer(t *testing.T) {
	w := isolatedTmuxWorker(t)
	workDir := t.TempDir()
	oldDump, _ := startKeepAliveRun(t, w, "old", workDir, reviewAgentEnv(reviewFirstKey+"="+reviewOldValue))
	assertDumpIsolated(t, oldDump, reviewFirstKey, reviewOldValue, reviewSecondKey)

	revokedDump := runToCompletion(t, w, "revoked", workDir, reviewAgentEnv())
	revokedEnv := parsePrintenv(waitFile(t, filepath.Join(revokedDump, "printenv")))
	if _, ok := revokedEnv[reviewFirstKey]; ok {
		t.Fatal("revoked secret was still present in the next run")
	}
	if _, ok := revokedEnv[reviewSecondKey]; ok {
		t.Fatal("unassigned secret was present after revoke")
	}

	reassignedDump := runToCompletion(t, w, "reassigned", workDir, reviewAgentEnv(reviewFirstKey+"="+reviewNewValue))
	reassignedEnv := parsePrintenv(waitFile(t, filepath.Join(reassignedDump, "printenv")))
	if got := reassignedEnv[reviewFirstKey]; got != reviewNewValue {
		t.Fatal("reassigned secret was not applied to the next run")
	}
	if got := reassignedEnv[reviewFirstKey]; got == reviewOldValue {
		t.Fatal("next run kept the previous assignment")
	}
	assertTmuxServerOmitsSecrets(t, w.tmuxServer)
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}
