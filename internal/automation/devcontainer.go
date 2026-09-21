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
	"sort"
	"strings"
)

// devContainerDefinition is the trusted-boundary view of a repository
// Dev Container. Features, Compose and image metadata are part of the same
// security decision as host hooks, mounts and extra Docker rights. The CLI
// remains the runtime, but it is only invoked after this policy passes.
type devContainerDefinition struct {
	Image             string          `json:"image"`
	Dockerfile        string          `json:"dockerFile"`
	DockerComposeFile json.RawMessage `json:"dockerComposeFile"`
	Service           string          `json:"service"`
	WorkspaceFolder   string          `json:"workspaceFolder"`
	WorkspaceMount    json.RawMessage `json:"workspaceMount"`
	Features          json.RawMessage `json:"features"`
	InitializeCommand json.RawMessage `json:"initializeCommand"`
	Privileged        bool            `json:"privileged"`
	Mounts            json.RawMessage `json:"mounts"`
	RunArgs           json.RawMessage `json:"runArgs"`
	CapAdd            json.RawMessage `json:"capAdd"`
	SecurityOpt       json.RawMessage `json:"securityOpt"`
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
	FeatureIDs      []string
	definition      devContainerDefinition
}

// devContainerApproval is an explicit, reviewed grant for one definition hash.
// A logged hash is an audit identifier and is never treated as approval.
type devContainerApproval struct {
	Hash             string
	AllowHostHooks   bool
	AllowPrivileged  bool
	AllowHostMounts  bool
	AllowExtraDocker bool
}

func (a devContainerApproval) grants(hash, capability string) bool {
	if strings.TrimSpace(a.Hash) == "" || hash == "" || a.Hash != hash {
		return false
	}
	switch capability {
	case "hostHooks":
		return a.AllowHostHooks
	case "privileged":
		return a.AllowPrivileged
	case "hostMounts":
		return a.AllowHostMounts
	case "extraDocker":
		return a.AllowExtraDocker
	default:
		return false
	}
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
	featureIDs, err := parseFeatureIDs(definition.Features)
	if err != nil {
		return devContainerConfig{}, true, err
	}
	result := devContainerConfig{
		Root:            root,
		Path:            config,
		Image:           definition.Image,
		WorkspaceFolder: definition.WorkspaceFolder,
		HasFeatures:     len(featureIDs) > 0,
		FeatureIDs:      featureIDs,
		definition:      definition,
	}
	if mount, ok := jsonString(definition.WorkspaceMount); ok {
		result.WorkspaceMount = mount
	}
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

func parseFeatureIDs(raw json.RawMessage) ([]string, error) {
	if !jsonFieldPresent(raw) {
		return nil, nil
	}
	var features map[string]json.RawMessage
	if err := json.Unmarshal(raw, &features); err != nil {
		return nil, errors.New("features ist ungültig")
	}
	ids := make([]string, 0, len(features))
	for id := range features {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func jsonFieldPresent(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != "[]" && s != "{}" && s != `""`
}

func jsonString(raw json.RawMessage) (string, bool) {
	if !jsonFieldPresent(raw) {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func reviewDevContainerPolicy(config devContainerConfig, approval devContainerApproval) error {
	def := config.definition
	if jsonFieldPresent(def.InitializeCommand) && !approval.grants(config.Hash, "hostHooks") {
		return errors.New("Dev-Container-Definition enthält nicht genehmigte Host-Hooks (initializeCommand)")
	}
	if def.Privileged && !approval.grants(config.Hash, "privileged") {
		return errors.New("Dev-Container-Definition verlangt privilegierte Container-Rechte")
	}
	if jsonFieldPresent(def.RunArgs) || jsonFieldPresent(def.CapAdd) || jsonFieldPresent(def.SecurityOpt) {
		if !approval.grants(config.Hash, "extraDocker") {
			return errors.New("Dev-Container-Definition verlangt zusätzliche Docker-Rechte")
		}
	}
	if err := reviewDevContainerMounts(config, approval); err != nil {
		return err
	}
	for _, id := range config.FeatureIDs {
		if featureRequiresHostAccess(id) && !approval.grants(config.Hash, "extraDocker") && !approval.grants(config.Hash, "privileged") && !approval.grants(config.Hash, "hostMounts") {
			return fmt.Errorf("Dev-Container-Feature %q verlangt Host- oder Docker-Rechte", id)
		}
	}
	for _, file := range config.ComposeFiles {
		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("dockerComposeFile nicht gefunden: %w", err)
		}
		if err := inspectComposeFile(config.Root, data, approval, config.Hash); err != nil {
			return err
		}
	}
	return nil
}

func reviewDevContainerMounts(config devContainerConfig, approval devContainerApproval) error {
	if approval.grants(config.Hash, "hostMounts") {
		return nil
	}
	mounts, err := mountSpecs(config.definition.Mounts)
	if err != nil {
		return err
	}
	for _, spec := range mounts {
		if hostBindEscapes(config.Root, mountSource(spec)) {
			return errors.New("Dev-Container-Definition mountet Host-Pfade außerhalb des Workspaces")
		}
	}
	if jsonFieldPresent(config.definition.WorkspaceMount) {
		source := mountSource(config.WorkspaceMount)
		if source == "" {
			if parsed, ok := jsonString(config.definition.WorkspaceMount); ok {
				source = mountSource(parsed)
			} else {
				source = mountSource(strings.TrimSpace(string(config.definition.WorkspaceMount)))
			}
		}
		if hostBindEscapes(config.Root, source) {
			return errors.New("Dev-Container-Definition mountet Host-Pfade außerhalb des Workspaces")
		}
	}
	return nil
}

func featureRequiresHostAccess(id string) bool {
	lower := strings.ToLower(id)
	for _, needle := range []string{
		"docker-in-docker",
		"docker-outside-of-docker",
		"docker-from-docker",
		"nvidia-cuda",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func mountSpecs(raw json.RawMessage) ([]string, error) {
	if !jsonFieldPresent(raw) {
		return nil, nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		specs := make([]string, 0, len(arr))
		for _, item := range arr {
			spec, err := mountSpecString(item)
			if err != nil {
				return nil, err
			}
			specs = append(specs, spec)
		}
		return specs, nil
	}
	spec, err := mountSpecString(raw)
	if err != nil {
		return nil, err
	}
	return []string{spec}, nil
}

func mountSpecString(raw json.RawMessage) (string, error) {
	var spec string
	if json.Unmarshal(raw, &spec) == nil {
		return spec, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", errors.New("mounts ist ungültig")
	}
	if src, ok := obj["source"].(string); ok {
		return "source=" + src, nil
	}
	if src, ok := obj["src"].(string); ok {
		return "source=" + src, nil
	}
	return strings.TrimSpace(string(raw)), nil
}

func mountSource(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	for _, part := range strings.Split(spec, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if found && (key == "source" || key == "src") {
			return strings.TrimSpace(value)
		}
	}
	if strings.HasPrefix(spec, "/") || strings.HasPrefix(spec, "${") {
		host, _, _ := strings.Cut(spec, ":")
		return host
	}
	return ""
}

func hostBindEscapes(root, source string) bool {
	source = strings.TrimSpace(source)
	if source == "" {
		return false
	}
	if strings.Contains(source, "${localEnv:") || (strings.Contains(source, "${") && !strings.HasPrefix(source, "${localWorkspaceFolder}")) {
		return true
	}
	expanded := expandWorkspaceSource(root, source)
	if expanded == "${localWorkspaceFolder}" {
		return false
	}
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(root, expanded)
	}
	if strings.Contains(filepath.Clean(expanded), "docker.sock") {
		return true
	}
	return !pathWithin(root, expanded)
}

func expandWorkspaceSource(root, source string) string {
	if source == "${localWorkspaceFolder}" {
		return root
	}
	if rest, ok := strings.CutPrefix(source, "${localWorkspaceFolder}/"); ok {
		return filepath.Join(root, rest)
	}
	return source
}

func inspectComposeFile(root string, data []byte, approval devContainerApproval, hash string) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	var parsed any
	if json.Unmarshal([]byte(trimmed), &parsed) == nil {
		return walkComposeValue(root, parsed, approval, hash)
	}
	return inspectComposeYAML(trimmed, approval, hash)
}

func inspectComposeYAML(text string, approval devContainerApproval, hash string) error {
	if strings.Contains(text, "<<:") || strings.Contains(text, "<< :") {
		return errors.New("Dev-Container-Compose verwendet YAML-Merges und kann nicht sicher geprüft werden")
	}
	lower := strings.ToLower(text)
	privileged := strings.Contains(lower, "privileged:")
	hostNS := strings.Contains(lower, "network_mode: host") || strings.Contains(lower, "network_mode:host") ||
		strings.Contains(lower, "pid: host") || strings.Contains(lower, "ipc: host")
	extra := strings.Contains(lower, "cap_add:") || strings.Contains(lower, "security_opt:") || strings.Contains(lower, "devices:")
	hostMount := strings.Contains(lower, "/var/run/docker.sock") || strings.Contains(lower, "- /:") ||
		strings.Contains(lower, "- '/:") || strings.Contains(lower, `- "/:`) ||
		strings.Contains(lower, "source: /") || strings.Contains(lower, "source:/")
	if privileged && !approval.grants(hash, "privileged") {
		return errors.New("Dev-Container-Compose verlangt privilegierte Container-Rechte")
	}
	if (hostNS || extra) && !approval.grants(hash, "extraDocker") {
		return errors.New("Dev-Container-Compose verlangt zusätzliche Docker-Rechte")
	}
	if hostMount && !approval.grants(hash, "hostMounts") {
		return errors.New("Dev-Container-Compose mountet Host-Pfade außerhalb des Workspaces")
	}
	return nil
}

func walkComposeValue(root string, value any, approval devContainerApproval, hash string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "privileged":
				if isTruthy(child) && !approval.grants(hash, "privileged") {
					return errors.New("Dev-Container-Compose verlangt privilegierte Container-Rechte")
				}
			case "network_mode", "pid", "ipc", "uts", "cgroup", "cgroupns":
				if strings.EqualFold(fmt.Sprint(child), "host") && !approval.grants(hash, "extraDocker") {
					return errors.New("Dev-Container-Compose verlangt zusätzliche Docker-Rechte")
				}
			case "cap_add", "devices", "security_opt":
				if !isEmptyComposeValue(child) && !approval.grants(hash, "extraDocker") {
					return errors.New("Dev-Container-Compose verlangt zusätzliche Docker-Rechte")
				}
			case "volumes":
				if err := inspectComposeVolumes(root, child, approval, hash); err != nil {
					return err
				}
			}
			if err := walkComposeValue(root, child, approval, hash); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := walkComposeValue(root, child, approval, hash); err != nil {
				return err
			}
		}
	}
	return nil
}

func inspectComposeVolumes(root string, value any, approval devContainerApproval, hash string) error {
	if approval.grants(hash, "hostMounts") {
		return nil
	}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			switch spec := item.(type) {
			case string:
				if hostBindEscapes(root, mountSource(spec)) || hostBindEscapes(root, composeBindHost(spec)) {
					return errors.New("Dev-Container-Compose mountet Host-Pfade außerhalb des Workspaces")
				}
			case map[string]any:
				source, _ := spec["source"].(string)
				typ, _ := spec["type"].(string)
				if typ == "" || strings.EqualFold(typ, "bind") {
					if hostBindEscapes(root, source) {
						return errors.New("Dev-Container-Compose mountet Host-Pfade außerhalb des Workspaces")
					}
				}
			}
		}
	case map[string]any:
		for _, item := range typed {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if opts, ok := m["driver_opts"].(map[string]any); ok {
				device, _ := opts["device"].(string)
				if hostBindEscapes(root, device) {
					return errors.New("Dev-Container-Compose mountet Host-Pfade außerhalb des Workspaces")
				}
			}
		}
	}
	return nil
}

func composeBindHost(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.HasPrefix(spec, "${") {
		return spec
	}
	host, _, found := strings.Cut(spec, ":")
	if !found {
		return ""
	}
	return host
}

func isTruthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "yes", "on":
			return true
		}
	case float64:
		return typed != 0
	}
	return false
}

func isEmptyComposeValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func runnerEnvironment() []string {
	// Lifecycle and runtime processes receive a deliberately narrow environment.
	// DATABASE_URL, the secret master key, proxy credentials and MCP/service
	// tokens are omitted so initializeCommand and Compose interpolation cannot
	// read the Shipyard service account's secrets via localEnv.
	keys := []string{"HOME", "PATH", "LANG", "LC_ALL", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "TMPDIR", "USER", "DOCKER_HOST"}
	env := make([]string, 0, len(keys)+2)
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			env = append(env, key+"="+value)
		}
	}
	return append(env, "NO_COLOR=1", "TERM=dumb")
}

func restrictedCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = runnerEnvironment()
	return cmd
}

func devContainerRuntime(ctx context.Context) (string, error) {
	runtime, err := exec.LookPath("devcontainer")
	if err != nil {
		return "", errors.New("keine nutzbare Dev-Container-Runtime gefunden (devcontainer CLI fehlt)")
	}
	if out, err := restrictedCommandContext(ctx, runtime, "version").CombinedOutput(); err != nil {
		return "", fmt.Errorf("Dev-Container-Runtime ist nicht nutzbar: %s", strings.TrimSpace(string(out)))
	}
	return runtime, nil
}

func startDevContainerAfterSandbox(ctx context.Context, runtime string, definition devContainerConfig, runID string, sandboxErr error, approval devContainerApproval) error {
	if sandboxErr != nil {
		return errors.New("Sandbox-Profil des Runs ist ungültig oder nicht verfügbar")
	}
	return startDevContainer(ctx, runtime, definition, runID, approval)
}

func startDevContainer(ctx context.Context, runtime string, definition devContainerConfig, runID string, approval devContainerApproval) error {
	if err := reviewDevContainerPolicy(definition, approval); err != nil {
		return err
	}
	args := []string{"up", "--workspace-folder", definition.Root, "--id-label", "shipyard.run=" + runID}
	if out, err := restrictedCommandContext(ctx, runtime, args...).CombinedOutput(); err != nil {
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
	out, err := restrictedCommandContext(ctx, docker, "ps", "--all", "--quiet", "--filter", "label="+label).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Dev-Container-Cleanup konnte nicht geprüft werden: %s", strings.TrimSpace(string(out)))
	}
	containerIDs := strings.Fields(string(out))
	if len(containerIDs) == 0 {
		return nil
	}
	if out, err := restrictedCommandContext(ctx, docker, append([]string{"rm", "--force"}, containerIDs...)...).CombinedOutput(); err != nil {
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
