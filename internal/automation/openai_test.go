package automation

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestResponsesURL(t *testing.T) {
	value, err := responsesURL("https://gateway.example")
	if err != nil || value != "https://gateway.example/v1/responses" {
		t.Fatalf("unexpected endpoint %q, %v", value, err)
	}
	value, err = responsesURL("https://gateway.example/v1")
	if err != nil || value != "https://gateway.example/v1/responses" {
		t.Fatalf("unexpected v1 endpoint %q, %v", value, err)
	}
	if _, err = responsesURL("http://gateway.example"); err == nil {
		t.Fatal("insecure endpoint was accepted")
	}
}

func TestCallResponsesAndOutputText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing authorization")
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}],"usage":{"total_tokens":42}}`))
	}))
	defer server.Close()
	result, err := callResponses(context.Background(), server.Client(), server.URL, "test-key", responseRequest{Model: "test", Input: "hello"})
	if err != nil || outputText(result) != "done" || result.Usage.TotalTokens != 42 {
		t.Fatalf("unexpected response %#v, %v", result, err)
	}
}

func TestRunToolCommandUsesWorktree(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap is required for the real sandbox integration test")
	}
	dir := t.TempDir()
	output := runToolCommand(context.Background(), dir, "pwd")
	abs, _ := filepath.Abs(dir)
	if !strings.Contains(output, abs) {
		t.Fatalf("command did not execute in worktree: %q", output)
	}
	if err := os.WriteFile(filepath.Join(dir, "check.txt"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := runToolCommand(context.Background(), dir, "cat check.txt"); result != "ok" {
		t.Fatalf("unexpected command output %q", result)
	}
	if result := runToolCommand(context.Background(), dir, "if test -s /proc/net/route; then echo route; else echo no-route; fi"); result != "no-route" {
		t.Fatalf("tool command unexpectedly has a routed network namespace: %q", result)
	}
}

func TestOpenAIToolSandboxArgumentsContainOnlyTheWorktreeAsWritableHostPath(t *testing.T) {
	dir := t.TempDir()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	args, err := openAISandboxArgs(dir, "printf ok")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(args, " "); !strings.Contains(got, "--unshare-all") || !strings.Contains(got, "--bind "+abs+" /workspace") || strings.Contains(got, "--bind /home/agent") {
		t.Fatalf("sandbox contract unexpectedly changed: %s", got)
	}
}
