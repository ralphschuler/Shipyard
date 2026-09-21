package automation

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"taskboard/internal/domain"
	"taskboard/internal/sandbox"
	"testing"
	"time"
)

func requireBubblewrap(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap is required for the real sandbox integration test")
	}
	if err := bubblewrapPreflight(context.Background()); err != nil {
		t.Skip(err.Error())
	}
}

func builtinPolicy(t *testing.T, name string) sandbox.Profile {
	t.Helper()
	policy, err := sandbox.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func runCLISandbox(t *testing.T, worktree string, policy sandbox.Profile, shell string) (string, error) {
	t.Helper()
	session, err := startCLISandbox(context.Background(), cliSandboxRequest{
		Worktree: worktree,
		Policy:   policy,
		Command:  "sh",
		Args:     []string{"-c", shell},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if session.Command != "bwrap" {
		t.Fatalf("production CLI sandbox did not wrap with bwrap: %q", session.Command)
	}
	if !strings.Contains(session.IsolationLog, policy.Name) || !strings.Contains(session.IsolationLog, policy.NetworkMode) || !strings.Contains(session.IsolationLog, policy.WriteMode) {
		t.Fatalf("logged policy does not match effective profile: %q vs %#v", session.IsolationLog, policy)
	}
	cmd := exec.Command(session.Command, session.Args...)
	out, runErr := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), runErr
}

func TestReviewCLIInvocationAppliesSandboxProfileOnProductionPath(t *testing.T) {
	workerSrc, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatal(err)
	}
	execSrc, err := os.ReadFile("sandbox_exec.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(execSrc), "func startCLISandbox(") {
		t.Fatal("startCLISandbox must be defined as the production CLI execution boundary")
	}
	if strings.Count(string(workerSrc), "startCLISandbox(") < 1 {
		t.Fatal("production execute path must invoke startCLISandbox")
	}
	if !strings.Contains(string(workerSrc), "session.IsolationLog") {
		t.Fatal("production execute path must log the effective sandbox isolation")
	}
	if !strings.Contains(string(workerSrc), "session.HostAuthLog") {
		t.Fatal("production execute path must log when host CLI auth is mounted")
	}
	if !strings.Contains(string(workerSrc), "hostCLIAuthMissingWarning") {
		t.Fatal("production execute path must warn when neither host CLI login nor assigned secret is available")
	}
	if !strings.Contains(string(workerSrc), "env = append(env, secretEnv...)") {
		t.Fatal("production execute path must still inject assigned secrets after sandbox wrap")
	}
	if !strings.Contains(string(workerSrc), "Dev-Container-Läufe können das gewählte Sandbox-Profil nicht technisch erzwingen") {
		t.Fatal("unsupported devcontainer CLI combinations must fail closed")
	}
}

func TestCLISandboxInvocationUsesProfileIsolation(t *testing.T) {
	command, args, err := cliSandboxInvocation("/workspace/run", "development", "sh", []string{"-c", "printf ok"})
	if err != nil || command != "bwrap" {
		t.Fatalf("sandbox wrapper missing: %q %v", command, err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--unshare-all", "--ro-bind", "--bind", "/workspace/run"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("sandbox args lack %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "--share-net") {
		t.Fatalf("development profile unexpectedly shared the network: %s", joined)
	}
	if strings.Contains(joined, "danger-full-access") {
		t.Fatal("unsafe sandbox flag leaked")
	}
}

func TestCLISandboxReadonlyArgsBindWorktreeReadOnly(t *testing.T) {
	args, err := cliSandboxArgs("/workspace/run", builtinPolicy(t, "qa-readonly"), "sh", []string{"-c", "true"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--ro-bind /workspace/run /workspace/run") {
		t.Fatalf("qa-readonly did not ro-bind the worktree: %s", joined)
	}
	if strings.Contains(joined, "--bind /workspace/run /workspace/run") {
		t.Fatalf("qa-readonly left the worktree writable: %s", joined)
	}
	if strings.Contains(joined, "--share-net") {
		t.Fatalf("qa-readonly unexpectedly shared the network: %s", joined)
	}
}

func TestCLISandboxBlocksReleaseBridgeDirectExecution(t *testing.T) {
	if _, _, err := cliSandboxInvocation("/workspace/run", "release-bridge", "sh", nil); err == nil || !strings.Contains(err.Error(), "hostseitigen Release-Bridge") {
		t.Fatalf("release bridge was not blocked: %v", err)
	}
	if _, err := startCLISandbox(context.Background(), cliSandboxRequest{
		Worktree: t.TempDir(),
		Policy:   builtinPolicy(t, "release-bridge"),
		Command:  "sh",
	}); err == nil || !strings.Contains(err.Error(), "hostseitigen Release-Bridge") {
		t.Fatalf("release-bridge did not fail closed before agent start: %v", err)
	}
}

func TestStartCLISandboxFailsClosedWithoutBubblewrap(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	t.Setenv("TASKBOARD_BWRAP_PREFLIGHT", "0")
	_, err := startCLISandbox(context.Background(), cliSandboxRequest{
		Worktree: t.TempDir(),
		Policy:   builtinPolicy(t, "strict"),
		Command:  "sh",
	})
	if err == nil || !strings.Contains(err.Error(), "bubblewrap") {
		t.Fatalf("missing bubblewrap was not fail-closed: %v", err)
	}
}

func TestStartCLISandboxFailsClosedForUnsupportedNetworkMode(t *testing.T) {
	policy := sandbox.Profile{Name: "custom-open", Mounts: []string{"worktree"}, NetworkMode: "host", WriteMode: "worktree", Active: true}
	_, err := startCLISandbox(context.Background(), cliSandboxRequest{
		Worktree: t.TempDir(),
		Policy:   policy,
		Command:  "sh",
	})
	if err == nil {
		t.Fatal("unsupported network mode was accepted")
	}
}

func TestCLISandboxQaReadonlyPreventsWorktreeModifications(t *testing.T) {
	requireBubblewrap(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runCLISandbox(t, dir, builtinPolicy(t, "qa-readonly"), "echo pwned > owned.txt || true; cat keep.txt")
	if err != nil {
		t.Fatalf("readonly probe failed: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "owned.txt")); !os.IsNotExist(statErr) {
		t.Fatal("qa-readonly allowed a worktree modification")
	}
	contents, readErr := os.ReadFile(filepath.Join(dir, "keep.txt"))
	if readErr != nil || string(contents) != "keep\n" {
		t.Fatalf("existing worktree file was altered: %q %v", contents, readErr)
	}
}

func TestCLISandboxDevelopmentAllowsWorktreeWrites(t *testing.T) {
	requireBubblewrap(t)
	dir := t.TempDir()
	out, err := runCLISandbox(t, dir, builtinPolicy(t, "development"), "echo ok > owned.txt && cat owned.txt")
	if err != nil {
		t.Fatalf("development write probe failed: %v %s", err, out)
	}
	contents, readErr := os.ReadFile(filepath.Join(dir, "owned.txt"))
	if readErr != nil || strings.TrimSpace(string(contents)) != "ok" {
		t.Fatalf("development profile did not persist a worktree write: %q %v", contents, readErr)
	}
}

func TestCLISandboxStrictBlocksHostDirsAndToolNetwork(t *testing.T) {
	requireBubblewrap(t)
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	if err := os.Mkdir(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secret, []byte("classified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runCLISandbox(t, worktree, builtinPolicy(t, "strict"), `
		if test -s /proc/net/route; then echo route; else echo no-route; fi
		if cat `+secret+` >/tmp/leaked 2>/dev/null; then echo leaked; else echo blocked-host; fi
		pwd
	`)
	if err != nil {
		t.Fatalf("strict probe failed: %v %s", err, out)
	}
	if !strings.Contains(out, "no-route") {
		t.Fatalf("strict profile did not isolate tool network: %q", out)
	}
	if strings.Contains(out, "leaked") || strings.Contains(out, "classified") {
		t.Fatalf("strict profile allowed an unassigned host file: %q", out)
	}
	if !strings.Contains(out, "blocked-host") {
		t.Fatalf("strict profile did not report blocked host access: %q", out)
	}
}

func TestCLISandboxDevelopmentBlocksHostDirsAndToolNetwork(t *testing.T) {
	requireBubblewrap(t)
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	if err := os.Mkdir(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secret, []byte("classified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runCLISandbox(t, worktree, builtinPolicy(t, "development"), `
		if test -s /proc/net/route; then echo route; else echo no-route; fi
		if cat `+secret+` >/tmp/leaked 2>/dev/null; then echo leaked; else echo blocked-host; fi
	`)
	if err != nil {
		t.Fatalf("development probe failed: %v %s", err, out)
	}
	if !strings.Contains(out, "no-route") || strings.Contains(out, "leaked") {
		t.Fatalf("development profile did not enforce host/network isolation: %q", out)
	}
}

func TestStartCLISandboxNoneUsesModelAPIProxyWithoutSharingNet(t *testing.T) {
	requireBubblewrap(t)
	session, err := startCLISandbox(context.Background(), cliSandboxRequest{
		Worktree: t.TempDir(),
		Policy:   builtinPolicy(t, "strict"),
		Command:  "sh",
		Args:     []string{"-c", "true"},
		Provider: domain.ProviderSetting{Provider: "codex", BaseURL: "https://api.openai.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	joined := strings.Join(session.Args, " ")
	if !session.ModelAPIProxy || strings.Contains(joined, "--share-net") {
		t.Fatalf("strict CLI sandbox did not separate model-API proxy from tool network: proxy=%t args=%s", session.ModelAPIProxy, joined)
	}
	if !strings.Contains(joined, "model-api-relay.py") || !strings.Contains(joined, "--unshare-all") {
		t.Fatalf("strict CLI sandbox missing relay/unshare boundary: %s", joined)
	}
}

func TestStartCLISandboxQANetworkSharesNetWithoutProxy(t *testing.T) {
	requireBubblewrap(t)
	policy := sandbox.Profile{Name: "qa-network", Mounts: []string{"worktree"}, NetworkMode: "qa-network", WriteMode: "readonly", Active: true}
	session, err := startCLISandbox(context.Background(), cliSandboxRequest{
		Worktree: t.TempDir(),
		Policy:   policy,
		Command:  "sh",
		Args:     []string{"-c", "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	joined := strings.Join(session.Args, " ")
	if session.ModelAPIProxy || !strings.Contains(joined, "--share-net") {
		t.Fatalf("qa-network CLI sandbox should share net without a model-API proxy: proxy=%t args=%s", session.ModelAPIProxy, joined)
	}
}

func TestModelAPIAllowlistIncludesProviderBaseURLAndLocalMCP(t *testing.T) {
	t.Setenv("TASKBOARD_ADDR", "127.0.0.1:8080")
	items := modelAPIAllowlist(domain.ProviderSetting{Provider: "codex", BaseURL: "https://gateway.example:8443/v1"})
	set := allowlistSet(items)
	for _, want := range []string{"gateway.example:8443", "api.openai.com:443", "api.x.ai:443", "127.0.0.1:8080"} {
		if _, ok := set[want]; !ok {
			t.Fatalf("allowlist missing %s: %#v", want, set)
		}
	}
}

func TestModelAPIProxyAllowsOnlyListedCONNECTTargets(t *testing.T) {
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "allowed")
	}))
	t.Cleanup(allowed.Close)
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("denied target was reached")
	}))
	t.Cleanup(denied.Close)
	allowHost, allowPort, err := net.SplitHostPort(strings.TrimPrefix(allowed.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	denyHost, denyPort, err := net.SplitHostPort(strings.TrimPrefix(denied.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := startModelAPIProxy([]allowedEndpoint{{host: allowHost, port: allowPort}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	if status := connectViaUnixProxy(t, proxy.path, net.JoinHostPort(allowHost, allowPort)); status != "HTTP/1.1 200 Connection Established" {
		t.Fatalf("allowed CONNECT status = %q", status)
	}
	if status := connectViaUnixProxy(t, proxy.path, net.JoinHostPort(denyHost, denyPort)); status != "HTTP/1.1 403 Forbidden" {
		t.Fatalf("denied CONNECT status = %q", status)
	}
}

func connectViaUnixProxy(t *testing.T, socket, target string) string {
	t.Helper()
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(conn, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(status)
}

func isolateHostCLIAuth(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("GROK_HOME", "")
}

func hasArgSeq(args []string, seq ...string) bool {
	if len(seq) == 0 || len(seq) > len(args) {
		return false
	}
	for i := 0; i+len(seq) <= len(args); i++ {
		match := true
		for j := range seq {
			if args[i+j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func writeHostCLIAuthFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestCLISandboxMountsHostCodexAuthReadOnly(t *testing.T) {
	isolateHostCLIAuth(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	auth := writeHostCLIAuthFile(t, codexHome, "auth.json", `{"tokens":{"access_token":"redacted"}}`)
	config := writeHostCLIAuthFile(t, codexHome, "config.toml", "model = \"gpt-test\"\n")
	cache := writeHostCLIAuthFile(t, codexHome, "cache.bin", "not-auth")
	if err := os.Mkdir(filepath.Join(codexHome, "log"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "log", "codex.log"), []byte("noise"), 0o600); err != nil {
		t.Fatal(err)
	}

	args, err := buildSandboxArgs(sandboxExec{
		Worktree: t.TempDir(),
		Policy:   builtinPolicy(t, "strict"),
		Layout:   sandboxLayoutCLI,
		Provider: "codex",
		Command:  "sh",
		Args:     []string{"-c", "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgSeq(args, "--ro-bind", auth, filepath.Join(sandboxCLIHome, ".codex", "auth.json")) {
		t.Fatalf("Codex auth.json was not ro-bound into sandbox HOME: %s", strings.Join(args, " "))
	}
	if !hasArgSeq(args, "--ro-bind", config, filepath.Join(sandboxCLIHome, ".codex", "config.toml")) {
		t.Fatalf("Codex config.toml was not ro-bound: %s", strings.Join(args, " "))
	}
	if !hasArgSeq(args, "--dir", filepath.Join(sandboxCLIHome, ".codex")) {
		t.Fatalf("sandbox HOME layout missing writable .codex dir: %s", strings.Join(args, " "))
	}
	if !hasArgSeq(args, "--setenv", "CODEX_HOME", filepath.Join(sandboxCLIHome, ".codex")) {
		t.Fatalf("CODEX_HOME was not set to the sandbox auth home: %s", strings.Join(args, " "))
	}
	if !hasArgSeq(args, "--setenv", "HOME", sandboxCLIHome) {
		t.Fatalf("HOME was not the CLI sandbox home: %s", strings.Join(args, " "))
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, cache) || strings.Contains(joined, filepath.Join(codexHome, "log")) {
		t.Fatalf("Codex cache/log tree was mounted: %s", joined)
	}
}

func TestCLISandboxHonorsHostCODEXHome(t *testing.T) {
	isolateHostCLIAuth(t)
	ignored := t.TempDir()
	if err := os.Mkdir(filepath.Join(ignored, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeHostCLIAuthFile(t, filepath.Join(ignored, ".codex"), "auth.json", `{"from":"home"}`)
	t.Setenv("HOME", ignored)

	custom := t.TempDir()
	auth := writeHostCLIAuthFile(t, custom, "auth.json", `{"from":"CODEX_HOME"}`)
	t.Setenv("CODEX_HOME", custom)

	args, err := cliSandboxArgs(t.TempDir(), builtinPolicy(t, "strict"), "sh", []string{"-c", "true"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgSeq(args, "--ro-bind", auth, filepath.Join(sandboxCLIHome, ".codex", "auth.json")) {
		t.Fatalf("CODEX_HOME auth was not used: %s", strings.Join(args, " "))
	}
	if strings.Contains(strings.Join(args, " "), filepath.Join(ignored, ".codex", "auth.json")) {
		t.Fatal("default ~/.codex was mounted despite CODEX_HOME")
	}
}

func TestCLISandboxMountsHostGrokAuthReadOnly(t *testing.T) {
	isolateHostCLIAuth(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	grokHome := filepath.Join(home, ".grok")
	auth := writeHostCLIAuthFile(t, grokHome, "auth.json", `{"https://auth.x.ai":{"key":"redacted"}}`)
	writeHostCLIAuthFile(t, grokHome, "session.log", "not-auth")

	args, err := buildSandboxArgs(sandboxExec{
		Worktree: t.TempDir(),
		Policy:   builtinPolicy(t, "strict"),
		Layout:   sandboxLayoutCLI,
		Provider: "grokbot",
		Command:  "sh",
		Args:     []string{"-c", "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgSeq(args, "--ro-bind", auth, filepath.Join(sandboxCLIHome, ".grok", "auth.json")) {
		t.Fatalf("Grok auth.json was not ro-bound into sandbox HOME: %s", strings.Join(args, " "))
	}
	if !hasArgSeq(args, "--setenv", "GROK_HOME", filepath.Join(sandboxCLIHome, ".grok")) {
		t.Fatalf("GROK_HOME was not set to the sandbox auth home: %s", strings.Join(args, " "))
	}
	if strings.Contains(strings.Join(args, " "), filepath.Join(grokHome, "session.log")) {
		t.Fatal("Grok non-auth files were mounted")
	}
}

func TestCLISandboxIgnoresMissingHostCLIAuth(t *testing.T) {
	isolateHostCLIAuth(t)
	args, err := buildSandboxArgs(sandboxExec{
		Worktree: t.TempDir(),
		Policy:   builtinPolicy(t, "strict"),
		Layout:   sandboxLayoutCLI,
		Provider: "codex",
		Command:  "sh",
		Args:     []string{"-c", "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, filepath.Join(sandboxCLIHome, ".codex")) || strings.Contains(joined, filepath.Join(sandboxCLIHome, ".grok")) {
		t.Fatalf("missing host login still produced auth mounts: %s", joined)
	}
	if strings.Contains(joined, "CODEX_HOME") || strings.Contains(joined, "GROK_HOME") {
		t.Fatalf("missing host login still set CLI home env: %s", joined)
	}
}

func TestCLISandboxDoesNotMountOtherProviderAuth(t *testing.T) {
	isolateHostCLIAuth(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	auth := writeHostCLIAuthFile(t, codexHome, "auth.json", `{"tokens":{}}`)

	args, err := buildSandboxArgs(sandboxExec{
		Worktree: t.TempDir(),
		Policy:   builtinPolicy(t, "strict"),
		Layout:   sandboxLayoutCLI,
		Provider: "grokbot",
		Command:  "sh",
		Args:     []string{"-c", "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), auth) {
		t.Fatal("Codex auth was mounted into a Grokbot sandbox")
	}
}

func TestStartCLISandboxLogsHostCodexAuthWithoutSecrets(t *testing.T) {
	requireBubblewrap(t)
	isolateHostCLIAuth(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	writeHostCLIAuthFile(t, codexHome, "auth.json", `{"tokens":{}}`)

	session, err := startCLISandbox(context.Background(), cliSandboxRequest{
		Worktree: t.TempDir(),
		Policy:   builtinPolicy(t, "strict"),
		Command:  "sh",
		Args:     []string{"-c", "true"},
		Provider: domain.ProviderSetting{Provider: "codex"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if session.HostAuthLog != hostCLIAuthMountedLog+"Codex" {
		t.Fatalf("host auth log = %q", session.HostAuthLog)
	}
	if strings.Join(session.HostAuthMounted, ",") != "Codex" {
		t.Fatalf("mounted = %#v", session.HostAuthMounted)
	}
	if strings.Contains(session.HostAuthLog, "tokens") || strings.Contains(session.HostAuthLog, "auth.json") {
		t.Fatalf("auth log leaked auth material names beyond the provider: %q", session.HostAuthLog)
	}
}

func TestCLISandboxHostCodexAuthVisibleWithoutCacheTree(t *testing.T) {
	requireBubblewrap(t)
	isolateHostCLIAuth(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	writeHostCLIAuthFile(t, codexHome, "auth.json", "login-ok\n")
	writeHostCLIAuthFile(t, codexHome, "cache.bin", "cache-secret\n")

	out, err := runCLISandbox(t, t.TempDir(), builtinPolicy(t, "strict"), `
		echo "HOME=$HOME"
		echo "CODEX_HOME=$CODEX_HOME"
		if test -r "$HOME/.codex/auth.json"; then echo auth-visible; cat "$HOME/.codex/auth.json"; else echo auth-missing; fi
		if test -e "$HOME/.codex/cache.bin" || test -e "$CODEX_HOME/cache.bin"; then echo cache-leaked; else echo cache-hidden; fi
		if echo session > "$CODEX_HOME/session-write" && test -s "$CODEX_HOME/session-write"; then echo home-writable; else echo home-readonly; fi
	`)
	if err != nil {
		t.Fatalf("sandbox probe failed: %v %s", err, out)
	}
	if !strings.Contains(out, "HOME="+sandboxCLIHome) || !strings.Contains(out, "CODEX_HOME="+filepath.Join(sandboxCLIHome, ".codex")) {
		t.Fatalf("sandbox HOME layout missing Codex auth home: %q", out)
	}
	if !strings.Contains(out, "auth-visible") || !strings.Contains(out, "login-ok") {
		t.Fatalf("host Codex login was not visible in the sandbox: %q", out)
	}
	if strings.Contains(out, "cache-leaked") || strings.Contains(out, "cache-secret") {
		t.Fatalf("Codex cache leaked into the sandbox: %q", out)
	}
	if !strings.Contains(out, "home-writable") {
		t.Fatalf("sandbox Codex home was not writable for session files: %q", out)
	}
}

func TestAgentEnvironmentDoesNotInheritHostProviderKeys(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "host-key")
	t.Setenv("XAI_API_KEY", "host-xai")
	inherited := strings.Join(agentEnvironment(""), "\n")
	if strings.Contains(inherited, "OPENAI_API_KEY=") || strings.Contains(inherited, "XAI_API_KEY=") {
		t.Fatal("unassigned host provider keys leaked into the CLI environment")
	}
	injected := append(agentEnvironment(""), "OPENAI_API_KEY=assigned-secret")
	if !strings.Contains(strings.Join(injected, "\n"), "OPENAI_API_KEY=assigned-secret") {
		t.Fatal("assigned secrets were not present in the CLI environment")
	}
}
