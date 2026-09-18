package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"taskboard/internal/store"
	"time"
)

const (
	productionBinaryPath = "/home/agent/taskboard/taskboard"
	productionHealthURL  = "http://127.0.0.1:8080/healthz"
)

// productionAdapter is deliberately bound to the host's known deployment
// paths. It never accepts a command, binary path, or restart target from the
// release payload or the browser.
type productionAdapter struct {
	store        *store.Store
	binary       string
	previous     string
	backupScript string
	backupDir    string
	databaseURL  string
	healthURL    string
	buildInfoURL string
	writeMu      sync.Mutex
}

// NewProductionOrchestrator wires the explicit host adapter used by the
// running Shipyard service. The default web constructor remains nil-safe for
// tests and non-production embeddings; the production entry point calls this
// constructor explicitly.
func NewProductionOrchestrator(s *store.Store) *Orchestrator {
	root := strings.TrimSpace(os.Getenv("TASKBOARD_INSTALL_ROOT"))
	if root == "" {
		root = "/home/agent/taskboard"
	}
	p := &productionAdapter{
		store:        s,
		binary:       filepath.Join(root, "taskboard"),
		previous:     filepath.Join(root, "taskboard.previous-update"),
		backupScript: filepath.Join(root, "deploy", "backup-postgres.sh"),
		backupDir:    envOr("TASKBOARD_BACKUP_DIR", filepath.Join(root, "backups")),
		databaseURL:  envOr("DATABASE_URL", "postgres://taskboard:taskboard@localhost:5432/taskboard?sslmode=disable"),
		healthURL:    productionHealthURL,
		buildInfoURL: "http://127.0.0.1:8080/app/build-info.json",
	}
	return &Orchestrator{
		Backup:            p.backup,
		DownloadAndVerify: p.downloadAndVerify,
		VerifyArtifact:    p.verifyArtifact,
		Verify:            p.verify,
		Migrate:           p.migrate,
		Switch:            p.switchBinary,
		// Restart validates the newly installed executable. The supervisor
		// restart itself is deferred until after the HTTP response.
		Restart:      p.restart,
		Health:       p.health,
		Rollback:     p.rollback,
		AfterSuccess: p.afterSuccess,
	}
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func (p *productionAdapter) backup(ctx context.Context, _ Snapshot) error {
	if info, err := os.Stat(p.backupScript); err != nil || info.Mode()&0111 == 0 {
		return errors.New("backup adapter is unavailable")
	}
	cmd := exec.CommandContext(ctx, p.backupScript)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+p.databaseURL, "TASKBOARD_BACKUP_DIR="+p.backupDir)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("backup adapter failed: %w", err)
	}
	return nil
}

func (p *productionAdapter) downloadAndVerify(ctx context.Context, artifactURL, checksum string) ([]byte, error) {
	client := Client{HTTP: http.DefaultClient, BaseURL: os.Getenv("TASKBOARD_GITHUB_API_URL"), Token: os.Getenv("TASKBOARD_GITHUB_TOKEN")}
	return client.DownloadAndVerify(ctx, artifactURL, checksum)
}

func (p *productionAdapter) verifyArtifact(_ context.Context, snapshot Snapshot, artifact []byte) error {
	if !snapshot.Release.Verified || !snapshot.Release.Compatible || len(artifact) < 4 {
		return errors.New("release artifact is not verified")
	}
	if string(artifact[:4]) != "\x7fELF" {
		return errors.New("release artifact is not an executable")
	}
	name := strings.ToLower(snapshot.Release.ArtifactName)
	if !strings.Contains(name, strings.ToLower(runtime.GOOS)) || !strings.Contains(name, strings.ToLower(runtime.GOARCH)) {
		return errors.New("release artifact is incompatible with this host")
	}
	digest := sha256.Sum256(artifact)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), snapshot.Release.Checksum) {
		return errors.New("release artifact checksum changed")
	}
	return nil
}

func (p *productionAdapter) verify(ctx context.Context, _ Snapshot) error {
	return p.health(ctx, Snapshot{})
}

func (p *productionAdapter) migrate(ctx context.Context, _ Snapshot) error {
	if p.store == nil {
		return errors.New("database migration adapter is unavailable")
	}
	return p.store.Migrate(ctx)
}

func (p *productionAdapter) switchBinary(_ context.Context, _ Snapshot, artifact []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	current, err := os.ReadFile(p.binary)
	if err != nil {
		return fmt.Errorf("read current binary: %w", err)
	}
	mode := os.FileMode(0755)
	if info, statErr := os.Stat(p.binary); statErr == nil && info.Mode().Perm()&0111 != 0 {
		mode = info.Mode().Perm()
	}
	if err := atomicWrite(p.previous, current, mode); err != nil {
		return fmt.Errorf("save rollback binary: %w", err)
	}
	if err := atomicWrite(p.binary, artifact, mode); err != nil {
		return fmt.Errorf("install release binary: %w", err)
	}
	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".taskboard-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (p *productionAdapter) restart(ctx context.Context, snapshot Snapshot) error {
	// The current process still owns the HTTP socket, so stopping it here would
	// truncate the update response. Execute the newly installed binary in its
	// isolated validation mode instead; this verifies the exact bundle that the
	// supervisor will start, including its embedded app and build metadata.
	cmd := exec.CommandContext(ctx, p.binary, "--validate-embedded-app", snapshot.Release.Version, snapshot.Release.Commit)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return errors.New("new release failed embedded app validation")
	}
	// A detached monitor waits for the old process to relinquish the socket,
	// verifies the health and build identity of the process started by systemd,
	// and restores the complete previous binary bundle on failure. The monitor
	// must not inherit the request context: the HTTP handler cancels it after
	// writing the successful update response.
	healthURL := envOr(p.healthURL, productionHealthURL)
	buildInfoURL := envOr(p.buildInfoURL, "http://127.0.0.1:8080/app/build-info.json")
	monitor := exec.Command(p.binary, "--monitor-restart", healthURL, buildInfoURL, p.binary, p.previous, snapshot.Release.Version, snapshot.Release.Commit)
	monitor.Stdout = io.Discard
	monitor.Stderr = io.Discard
	if err := monitor.Start(); err != nil {
		return errors.New("new release restart monitor could not start")
	}
	return nil
}

func (p *productionAdapter) health(ctx context.Context, _ Snapshot) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, envOr(p.healthURL, productionHealthURL), nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned HTTP %d", res.StatusCode)
	}
	return nil
}

type restartMonitorConfig struct {
	healthURL    string
	buildInfoURL string
	binary       string
	previous     string
	version      string
	commit       string
	terminate    func(context.Context) error
	poll         time.Duration
	wait         time.Duration
	client       *http.Client
}

// RunRestartMonitor is the small supervisor hand-off used by the production
// binary. It returns an error so the CLI can exit with the service's
// Restart=on-failure status after restoring the previous bundle.
func RunRestartMonitor(healthURL, buildInfoURL, binary, previous, version, commit string) error {
	return monitorRestart(context.Background(), restartMonitorConfig{
		healthURL:    healthURL,
		buildInfoURL: buildInfoURL,
		binary:       binary,
		previous:     previous,
		version:      version,
		commit:       commit,
		terminate:    func(context.Context) error { return terminateRunningBundle(binary) },
		poll:         250 * time.Millisecond,
		wait:         30 * time.Second,
	})
}

func monitorRestart(ctx context.Context, cfg restartMonitorConfig) error {
	if cfg.poll <= 0 {
		cfg.poll = 250 * time.Millisecond
	}
	if cfg.wait <= 0 {
		cfg.wait = 30 * time.Second
	}
	if cfg.terminate == nil {
		cfg.terminate = func(context.Context) error { return terminateRunningBundle(cfg.binary) }
	}
	client := cfg.client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	deadline := time.NewTimer(cfg.wait)
	defer deadline.Stop()

	// First observe the old service going away. A healthy response here is
	// still the old process and must never count as update success.
	for {
		if err := ctx.Err(); err != nil {
			return rollbackAfterRestartError(cfg, err)
		}
		if !endpointHealthy(ctx, client, cfg.healthURL) {
			break
		}
		if !waitForMonitorTick(ctx, deadline, cfg.poll) {
			return rollbackAfterRestartError(cfg, errors.New("old service did not stop"))
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return rollbackAfterRestartError(cfg, err)
		}
		if endpointHealthy(ctx, client, cfg.healthURL) && buildInfoMatches(ctx, client, cfg.buildInfoURL, cfg.version, cfg.commit) {
			return nil
		}
		if !waitForMonitorTick(ctx, deadline, cfg.poll) {
			return rollbackAfterRestartError(cfg, errors.New("new service did not become healthy with matching build metadata"))
		}
	}
}

func waitForMonitorTick(ctx context.Context, deadline *time.Timer, poll time.Duration) bool {
	timer := time.NewTimer(poll)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-deadline.C:
		return false
	case <-timer.C:
		return true
	}
}

func endpointHealthy(ctx context.Context, client *http.Client, endpoint string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode == http.StatusOK
}

func buildInfoMatches(ctx context.Context, client *http.Client, endpoint, version, commit string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false
	}
	var info struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return false
	}
	return info.Version == version && info.Commit == commit
}

func rollbackAfterRestartError(cfg restartMonitorConfig, cause error) error {
	if err := cfg.terminate(context.Background()); err != nil {
		cause = fmt.Errorf("%w; terminate failed bundle: %v", cause, err)
	}
	previous, err := os.ReadFile(cfg.previous)
	if err != nil {
		return fmt.Errorf("%w; read rollback bundle: %v", cause, err)
	}
	if err := atomicWrite(cfg.binary, previous, 0755); err != nil {
		return fmt.Errorf("%w; restore rollback bundle: %v", cause, err)
	}
	return cause
}

// terminateRunningBundle stops the process that systemd started from the
// candidate binary. The monitor is exec'd from the same binary, so it skips
// itself. Stopping the failed process before restoring the file is essential:
// a running process keeps serving its in-memory embedded assets after the
// on-disk binary has been rolled back.
func terminateRunningBundle(binary string) error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return fmt.Errorf("read process table: %w", err)
	}
	var processes []*os.Process
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		executable, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		if err != nil || filepath.Clean(executable) != filepath.Clean(binary) {
			continue
		}
		process, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("stop process %d: %w", pid, err)
		}
		processes = append(processes, process)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(processes) > 0 && time.Now().Before(deadline) {
		remaining := processes[:0]
		for _, process := range processes {
			if err := process.Signal(syscall.Signal(0)); err == nil {
				remaining = append(remaining, process)
			}
		}
		processes = remaining
		if len(processes) > 0 {
			time.Sleep(25 * time.Millisecond)
		}
	}
	for _, process := range processes {
		_ = process.Signal(syscall.SIGKILL)
	}
	return nil
}

func (p *productionAdapter) rollback(_ context.Context, _ Snapshot) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	previous, err := os.ReadFile(p.previous)
	if err != nil {
		return fmt.Errorf("read rollback binary: %w", err)
	}
	if err := atomicWrite(p.binary, previous, 0755); err != nil {
		return fmt.Errorf("restore rollback binary: %w", err)
	}
	return nil
}

func (p *productionAdapter) afterSuccess() {
	// taskboard.service has NoNewPrivileges=true, so a process-local sudo or
	// systemd-run cannot elevate to restart itself. Restart=on-failure is already
	// part of the hardened unit; exit only after net/http has flushed the success
	// response and let systemd start the atomically installed binary.
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(75)
	}()
}
