package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// devContainerDefinition is intentionally a small, permissive view of the
// upstream schema. The devcontainer CLI remains the source of truth for
// features, Compose, mounts and lifecycle semantics; this type is only used
// for discovery, validation and audit logging.
type devContainerDefinition struct {
	Image             string          `json:"image"`
	Dockerfile        string          `json:"dockerFile"`
	DockerComposeFile json.RawMessage `json:"dockerComposeFile"`
	Service           string          `json:"service"`
	WorkspaceFolder   string          `json:"workspaceFolder"`
	WorkspaceMount    string          `json:"workspaceMount"`
	Features          json.RawMessage `json:"features"`
}

type devContainerConfig struct {
	Root            string
	Path            string
	Hash            string
	Image           string
	WorkspaceFolder string
	WorkspaceMount  string
	Dockerfile      string
	ComposeFiles    []string
	HasFeatures     bool
}

func discoverDevContainer(root string) (devContainerConfig, bool, error) {
	config := filepath.Join(root, ".devcontainer", "devcontainer.json")
	data, err := os.ReadFile(config)
	if errors.Is(err, os.ErrNotExist) {
		return devContainerConfig{}, false, nil
	}
	if err != nil {
		return devContainerConfig{}, true, fmt.Errorf("Dev-Container-Definition konnte nicht gelesen werden: %w", err)
	}
	var definition devContainerDefinition
	if err := json.Unmarshal(data, &definition); err != nil {
		return devContainerConfig{}, true, fmt.Errorf("devcontainer.json ist ungültig: %w", err)
	}
	if strings.TrimSpace(definition.Image) == "" && strings.TrimSpace(definition.Dockerfile) == "" && len(definition.DockerComposeFile) == 0 {
		return devContainerConfig{}, true, errors.New("devcontainer.json enthält weder image, dockerFile noch dockerComposeFile")
	}
	features := strings.TrimSpace(string(definition.Features))
	result := devContainerConfig{Root: root, Path: config, Image: definition.Image, WorkspaceFolder: definition.WorkspaceFolder, WorkspaceMount: definition.WorkspaceMount, HasFeatures: features != "" && features != "null" && features != "{}"}
	if definition.Dockerfile != "" {
		result.Dockerfile = filepath.Join(filepath.Dir(config), filepath.Clean(definition.Dockerfile))
		if !pathWithin(root, result.Dockerfile) {
			return devContainerConfig{}, true, errors.New("dockerFile liegt außerhalb des Projekt-Workspaces")
		}
		if _, err := os.Stat(result.Dockerfile); err != nil {
			return devContainerConfig{}, true, fmt.Errorf("dockerFile nicht gefunden: %w", err)
		}
	}
	if len(definition.DockerComposeFile) > 0 {
		var files []string
		composeValue := strings.TrimSpace(string(definition.DockerComposeFile))
		if strings.HasPrefix(composeValue, "[") {
			if err := json.Unmarshal(definition.DockerComposeFile, &files); err != nil {
				return devContainerConfig{}, true, fmt.Errorf("dockerComposeFile ist ungültig: %w", err)
			}
		} else if err := json.Unmarshal(definition.DockerComposeFile, &files); err != nil {
			var file string
			if string(definition.DockerComposeFile) == "" || json.Unmarshal(definition.DockerComposeFile, &file) != nil {
				return devContainerConfig{}, true, errors.New("dockerComposeFile ist ungültig")
			}
			files = []string{file}
		}
		for _, file := range files {
			resolved := filepath.Join(filepath.Dir(config), filepath.Clean(file))
			if !pathWithin(root, resolved) {
				return devContainerConfig{}, true, errors.New("dockerComposeFile liegt außerhalb des Projekt-Workspaces")
			}
			if _, err := os.Stat(resolved); err != nil {
				return devContainerConfig{}, true, fmt.Errorf("dockerComposeFile nicht gefunden: %w", err)
			}
			result.ComposeFiles = append(result.ComposeFiles, resolved)
		}
	}
	hash := sha256.New()
	hash.Write(data)
	for _, file := range append([]string{result.Dockerfile}, result.ComposeFiles...) {
		if fileData, readErr := os.ReadFile(file); readErr == nil {
			hash.Write([]byte(file))
			hash.Write(fileData)
		}
	}
	result.Hash = hex.EncodeToString(hash.Sum(nil))
	return result, true, nil
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func devContainerRuntime(ctx context.Context) (string, error) {
	runtime, err := exec.LookPath("devcontainer")
	if err != nil {
		return "", errors.New("keine nutzbare Dev-Container-Runtime gefunden (devcontainer CLI fehlt)")
	}
	if out, err := exec.CommandContext(ctx, runtime, "version").CombinedOutput(); err != nil {
		return "", fmt.Errorf("Dev-Container-Runtime ist nicht nutzbar: %s", strings.TrimSpace(string(out)))
	}
	return runtime, nil
}

func startDevContainer(ctx context.Context, runtime string, definition devContainerConfig, runID string) error {
	args := []string{"up", "--workspace-folder", definition.Root, "--id-label", "shipyard.run=" + runID}
	if out, err := exec.CommandContext(ctx, runtime, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("Dev-Container konnte nicht gestartet werden: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func stopDevContainer(ctx context.Context, runID string) error {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return errors.New("Dev-Container-Cleanup nicht möglich (docker CLI fehlt)")
	}
	label := "shipyard.run=" + runID
	out, err := exec.CommandContext(ctx, docker, "ps", "--all", "--quiet", "--filter", "label="+label).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Dev-Container-Cleanup konnte nicht geprüft werden: %s", strings.TrimSpace(string(out)))
	}
	containerIDs := strings.Fields(string(out))
	if len(containerIDs) == 0 {
		return nil
	}
	if out, err := exec.CommandContext(ctx, docker, append([]string{"rm", "--force"}, containerIDs...)...).CombinedOutput(); err != nil {
		return fmt.Errorf("Dev-Container konnte nicht beendet werden: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func devContainerExecArgs(runtime string, definition devContainerConfig, command string, args []string) (string, []string) {
	result := []string{"exec", "--workspace-folder", definition.Root, command}
	return runtime, append(result, args...)
}

func devContainerWorkspaceFolder(definition devContainerConfig, hostRoot string) string {
	if strings.TrimSpace(definition.WorkspaceFolder) != "" {
		return strings.TrimRight(definition.WorkspaceFolder, "/")
	}
	for _, part := range strings.Split(definition.WorkspaceMount, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if found && (key == "target" || key == "destination") && strings.TrimSpace(value) != "" {
			return strings.TrimRight(strings.TrimSpace(value), "/")
		}
	}
	return "/workspaces/" + filepath.Base(hostRoot)
}
