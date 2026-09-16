package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"taskboard/internal/domain"
	"time"
)

const maxOpenAIToolRounds = 24

// bubblewrapPreflight starts the same namespace shape used for tool commands
// without touching the worktree or contacting a provider. Keeping this check
// before the API request makes host/systemd regressions fail cheaply.
func bubblewrapPreflight(ctx context.Context) error {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return errors.New("OpenAI-Agenten benötigen bubblewrap (bwrap); installiere das Paket bubblewrap auf dem Server und starte taskboard.service neu")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{"--die-with-parent", "--unshare-all", "--new-session"}
	for _, directory := range []string{"/usr", "/bin", "/lib", "/lib64"} {
		if _, statErr := os.Stat(directory); statErr == nil {
			args = append(args, "--ro-bind", directory, directory)
		}
	}
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--setenv", "PATH", "/usr/bin:/bin", "/bin/sh", "-c", "test ! -s /proc/net/route && printf sandbox-ok")
	out, err := exec.CommandContext(checkCtx, bwrap, args...).CombinedOutput()
	if err == nil && strings.TrimSpace(string(out)) == "sandbox-ok" {
		return nil
	}
	detail := strings.TrimSpace(string(out))
	if strings.Contains(detail, "NETLINK_ROUTE") || strings.Contains(detail, "loopback") {
		return errors.New("Bubblewrap-Sandbox konnte keinen NETLINK_ROUTE-Socket anlegen; erlaube AF_NETLINK ausschließlich in taskboard.service (RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK), führe daemon-reload aus und starte den Dienst neu")
	}
	if checkCtx.Err() != nil {
		return fmt.Errorf("Bubblewrap-Sandbox-Preflight ist abgelaufen; prüfe bwrap und die Systemd-Sandbox: %w", checkCtx.Err())
	}
	if detail == "" {
		if err != nil {
			detail = err.Error()
		} else {
			detail = "unbekannter Fehler"
		}
	}
	return fmt.Errorf("Bubblewrap-Sandbox-Preflight fehlgeschlagen: %s; prüfe installierte bwrap-Version, User-/Mount-Namespaces und taskboard.service", detail)
}

type responseRequest struct {
	Model              string   `json:"model"`
	Instructions       string   `json:"instructions,omitempty"`
	Input              any      `json:"input"`
	PreviousResponseID string   `json:"previous_response_id,omitempty"`
	Tools              any      `json:"tools,omitempty"`
	Reasoning          any      `json:"reasoning,omitempty"`
	Text               any      `json:"text,omitempty"`
	MaxOutputTokens    int      `json:"max_output_tokens,omitempty"`
	Temperature        *float64 `json:"temperature,omitempty"`
	ServiceTier        string   `json:"service_tier,omitempty"`
	Store              bool     `json:"store"`
}
type responseUsage struct {
	InputTokens       int    `json:"input_tokens"`
	OutputTokens      int    `json:"output_tokens"`
	TotalTokens       int    `json:"total_tokens"`
	CachedInputTokens int    `json:"cached_input_tokens"`
	CacheWriteTokens  int    `json:"cache_write_tokens"`
	ReasoningTokens   int    `json:"reasoning_tokens"`
	CostMicrousd      *int64 `json:"cost_microusd"`
}
type openAIUsage struct {
	InputTokens, OutputTokens, CachedInputTokens, CacheWriteTokens, ReasoningTokens, TotalTokens int
	EstimatedCostMicrousd                                                                        int64
	NativeCostMicrousd                                                                           *int64
	APICalls                                                                                     int
	ServiceTier                                                                                  string
}
type responseOutput struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}
type responseResult struct {
	ID         string           `json:"id"`
	Status     string           `json:"status"`
	OutputText string           `json:"output_text"`
	Output     []responseOutput `json:"output"`
	Usage      responseUsage    `json:"usage"`
	Error      *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func responsesURL(base string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return "https://api.openai.com/v1/responses", nil
	}
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("OpenAI Base URL muss eine HTTPS-Adresse sein")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/v1") {
		u.Path += "/v1"
	}
	u.Path += "/responses"
	return u.String(), nil
}

func outputText(result responseResult) string {
	if strings.TrimSpace(result.OutputText) != "" {
		return result.OutputText
	}
	var parts []string
	for _, item := range result.Output {
		for _, content := range item.Content {
			if content.Type == "output_text" && content.Text != "" {
				parts = append(parts, content.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func callResponses(ctx context.Context, client *http.Client, endpoint, apiKey string, request responseRequest) (responseResult, error) {
	var result responseResult
	body, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := client.Do(httpRequest)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 2<<20)
	if err := json.NewDecoder(limited).Decode(&result); err != nil {
		return result, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if result.Error != nil && result.Error.Message != "" {
			return result, fmt.Errorf("OpenAI API: %s", result.Error.Message)
		}
		return result, fmt.Errorf("OpenAI API: HTTP %d", response.StatusCode)
	}
	if result.Error != nil && result.Error.Message != "" {
		return result, fmt.Errorf("OpenAI API: %s", result.Error.Message)
	}
	return result, nil
}

func toolDefinitions() []map[string]any {
	return []map[string]any{{
		"type":        "function",
		"name":        "run_command",
		"description": "Run a non-interactive shell command in the assigned Git worktree. Inspect files, edit code, and run focused tests. Do not use network, push, merge, deploy, or start persistent processes.",
		"parameters": map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"command": map[string]string{"type": "string", "description": "Shell command executed in the worktree"}},
			"required":             []string{"command"},
			"additionalProperties": false,
		},
	}}
}

func openAISandboxArgs(worktree, command string) ([]string, error) {
	abs, err := filepath.Abs(worktree)
	if err != nil {
		return nil, err
	}
	args := []string{"--die-with-parent", "--unshare-all", "--new-session"}
	for _, directory := range []string{"/usr", "/bin", "/lib", "/lib64"} {
		if _, statErr := os.Stat(directory); statErr == nil {
			args = append(args, "--ro-bind", directory, directory)
		}
	}
	return append(args,
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
		"--bind", abs, "/workspace", "--chdir", "/workspace",
		"--setenv", "HOME", "/workspace", "--setenv", "PATH", "/usr/bin:/bin",
		"--setenv", "LANG", "C", "/bin/sh", "-lc", command,
	), nil
}

func runToolCommand(ctx context.Context, worktree, command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return "error: empty command"
	}
	// This protects against accidental parent-directory execution while the
	// service-level sandbox remains the outer security boundary.
	args, err := openAISandboxArgs(worktree, command)
	if err != nil {
		return "error: invalid worktree"
	}
	// A network namespace alone is not isolation: without a separate mount
	// namespace a remote model could still walk from its worktree into host
	// directories. Bubblewrap supplies both boundaries. The API request stays
	// outside this sandbox; only model-issued shell commands are contained.
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return "error: command sandbox unavailable: OpenAI-Agenten benötigen bubblewrap (bwrap); installiere das Paket bubblewrap auf dem Server und starte taskboard.service neu"
	}
	cmd := exec.CommandContext(ctx, bwrap, args...)
	cmd.Env = agentEnvironment("")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if len(text) > 12000 {
		text = text[len(text)-12000:]
	}
	if err != nil {
		return "exit error: " + err.Error() + "\n" + text
	}
	return text
}

func runOpenAIResponses(ctx context.Context, provider domain.ProviderSetting, prompt, worktree string) (string, openAIUsage, error) {
	if strings.TrimSpace(provider.Model) == "" {
		return "", openAIUsage{}, errors.New("OpenAI-Modell fehlt in den Provider-Einstellungen")
	}
	if strings.TrimSpace(provider.SecretEnv) == "" {
		return "", openAIUsage{}, errors.New("OpenAI Secret-Umgebungsvariable fehlt")
	}
	if os.Getenv("TASKBOARD_BWRAP_PREFLIGHT") != "0" {
		if err := bubblewrapPreflight(ctx); err != nil {
			return "", openAIUsage{}, err
		}
	} else if _, err := exec.LookPath("bwrap"); err != nil {
		return "", openAIUsage{}, errors.New("OpenAI-Agenten benötigen bubblewrap (bwrap); installiere das Paket bubblewrap auf dem Server und starte taskboard.service neu")
	}
	apiKey := os.Getenv(provider.SecretEnv)
	if apiKey == "" {
		return "", openAIUsage{}, errors.New("OpenAI Secret-Umgebungsvariable ist nicht gesetzt")
	}
	endpoint, err := responsesURL(provider.BaseURL)
	if err != nil {
		return "", openAIUsage{}, err
	}
	var options struct {
		ReasoningEffort      string   `json:"reasoning_effort"`
		Verbosity            string   `json:"verbosity"`
		MaxOutputTokens      int      `json:"max_output_tokens"`
		Temperature          *float64 `json:"temperature"`
		ServiceTier          string   `json:"service_tier"`
		InputCostPerMillion  float64  `json:"input_cost_per_million"`
		OutputCostPerMillion float64  `json:"output_cost_per_million"`
	}
	if provider.Options != "" && provider.Options != "{}" {
		if err := json.Unmarshal([]byte(provider.Options), &options); err != nil {
			return "", openAIUsage{}, errors.New("OpenAI-Optionen sind ungültig")
		}
	}
	if options.MaxOutputTokens < 0 || options.InputCostPerMillion < 0 || options.OutputCostPerMillion < 0 || (options.Temperature != nil && (*options.Temperature < 0 || *options.Temperature > 2)) {
		return "", openAIUsage{}, errors.New("OpenAI-Optionen enthalten ungültige Werte")
	}
	client := &http.Client{Timeout: 19 * time.Minute}
	usageServiceTier := options.ServiceTier
	request := responseRequest{
		Model:           provider.Model,
		Instructions:    "Du bist ein Coding-Agent. Arbeite ausschließlich im zugewiesenen Git-Worktree über run_command. Keine Netzwerkanfragen, keine Pushes, Merges, Releases, Deployments oder dauerhaften Prozesse. Prüfe die Änderung und antworte mit einer kurzen Zusammenfassung.",
		Input:           prompt,
		Tools:           toolDefinitions(),
		MaxOutputTokens: options.MaxOutputTokens,
		Temperature:     options.Temperature,
		ServiceTier:     options.ServiceTier,
		Store:           false,
	}
	if options.ReasoningEffort != "" {
		request.Reasoning = map[string]string{"effort": options.ReasoningEffort}
	}
	if options.Verbosity != "" {
		request.Text = map[string]string{"verbosity": options.Verbosity}
	}
	var transcript []string
	usage := openAIUsage{}
	usage.ServiceTier = usageServiceTier
	for round := 0; round < maxOpenAIToolRounds; round++ {
		result, err := callResponses(ctx, client, endpoint, apiKey, request)
		if err != nil {
			return strings.Join(transcript, "\n"), usage, err
		}
		usage.InputTokens += result.Usage.InputTokens
		usage.APICalls++
		usage.OutputTokens += result.Usage.OutputTokens
		usage.CachedInputTokens += result.Usage.CachedInputTokens
		usage.CacheWriteTokens += result.Usage.CacheWriteTokens
		usage.ReasoningTokens += result.Usage.ReasoningTokens
		usage.TotalTokens += result.Usage.TotalTokens
		if result.Usage.CostMicrousd != nil {
			if usage.NativeCostMicrousd == nil {
				usage.NativeCostMicrousd = new(int64)
			}
			*usage.NativeCostMicrousd += *result.Usage.CostMicrousd
		}
		var outputs []map[string]string
		for _, item := range result.Output {
			if item.Type != "function_call" || item.Name != "run_command" {
				continue
			}
			var args struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal([]byte(item.Arguments), &args); err != nil {
				outputs = append(outputs, map[string]string{"type": "function_call_output", "call_id": item.CallID, "output": "error: invalid command arguments"})
				continue
			}
			output := runToolCommand(ctx, worktree, args.Command)
			transcript = append(transcript, "$ "+args.Command+"\n"+output)
			outputs = append(outputs, map[string]string{"type": "function_call_output", "call_id": item.CallID, "output": output})
		}
		if len(outputs) == 0 {
			text := strings.TrimSpace(outputText(result))
			if text != "" {
				transcript = append(transcript, text)
			}
			usage.EstimatedCostMicrousd = int64(math.Round((float64(usage.InputTokens)*options.InputCostPerMillion + float64(usage.OutputTokens)*options.OutputCostPerMillion) / 1_000_000 * 1_000_000))
			return strings.Join(transcript, "\n\n"), usage, nil
		}
		request = responseRequest{Model: provider.Model, Input: outputs, PreviousResponseID: result.ID, Tools: toolDefinitions(), Reasoning: request.Reasoning, Text: request.Text, MaxOutputTokens: request.MaxOutputTokens, Temperature: request.Temperature, ServiceTier: request.ServiceTier, Store: false}
	}
	return strings.Join(transcript, "\n\n"), usage, errors.New("OpenAI-Agent hat das Werkzeuglimit erreicht")
}
