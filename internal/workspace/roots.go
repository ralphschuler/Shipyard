// Package workspace centralizes the on-disk roots used for managed Git
// checkouts and isolated agent worktrees. The legacy locations remain the
// default so existing deployments upgrade without moving data.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	legacyProjectsRoot = "/home/agent/.taskboard-projects"
	legacyRunsRoot     = "/home/agent/.taskboard-runs"
	// legacyRuntimeRoot sits beside the legacy run directory, on the real host
	// filesystem. It must not be /tmp or /var/tmp: the taskboard unit sets
	// PrivateTmp=true, so those paths are invisible to rootless dockerd.
	legacyRuntimeRoot = "/home/agent/.taskboard-runtime"
)

// Root returns an explicitly configured persistent workspace root. It is
// intentionally server-side only: callers never accept a browser supplied
// path. An empty value keeps the legacy locations for backwards compatibility.
func Root() string {
	return strings.TrimSpace(os.Getenv("TASKBOARD_WORKSPACE_ROOT"))
}

type Status struct {
	Root         string
	Projects     string
	Runs         string
	Integrations string
	Runtime      string
	Storage      string
	Marker       bool
	Migration    bool
	Writable     bool
	Git          bool
	Error        string
}

const markerName = ".shipyard-workspace"

func MarkerPath() string { return filepath.Join(configuredRoot(), markerName) }

func configuredRoot() string {
	if root := Root(); root != "" {
		return filepath.Clean(root)
	}
	return filepath.Dir(legacyProjectsRoot)
}

// Validate performs the same fail-closed check at startup and immediately
// before runs. The root must already exist and carry the operator-created
// marker; this prevents an absent NFS mount from being silently replaced by a
// local directory. Managed subdirectories may be created after that check.
func Validate() (Status, error) {
	root := configuredRoot()
	if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
		return invalidStatus(root, "TASKBOARD_WORKSPACE_ROOT muss ein absoluter Nicht-Root-Pfad sein")
	}
	root = filepath.Clean(root)
	status := Status{Root: root, Projects: ProjectsRoot(), Runs: RunsRoot(), Integrations: IntegrationsRoot(), Runtime: RuntimeRoot()}
	if status.Integrations == "" {
		status.Integrations = filepath.Join(root, "integrations")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		status.Error = "Workspace-Root ist nicht eingehängt oder nicht verfügbar"
		return status, errors.New(status.Error)
	}
	if fs := new(syscall.Statfs_t); syscall.Statfs(root, fs) == nil {
		const nfsSuperMagic = 0x6969
		if uint64(fs.Type) == nfsSuperMagic {
			status.Storage = "NFS"
		} else {
			status.Storage = "lokal"
		}
	} else {
		status.Storage = "unbekannt"
	}
	if Root() != "" {
		markerPath := filepath.Join(root, markerName)
		markerInfo, err := os.Lstat(markerPath)
		if err != nil || !markerInfo.Mode().IsRegular() {
			status.Error = "Workspace-Root ist nicht als Shipyard-Speicher markiert"
			return status, errors.New(status.Error)
		}
		markerContent, readErr := os.ReadFile(markerPath)
		if readErr != nil {
			status.Error = "Shipyard-Speichermarker konnte nicht gelesen werden"
			return status, errors.New(status.Error)
		}
		if strings.Contains(strings.ToLower(string(markerContent)), "storage=nfs") && status.Storage != "NFS" {
			status.Error = "Workspace-Speicher ist als NFS markiert, aber kein NFS-Mount"
			return status, errors.New(status.Error)
		}
		status.Marker = true
	}
	status.Migration = migrationInProgress(root)
	if status.Migration {
		status.Error = "Workspace-Migration läuft; neue Runs sind pausiert"
		return status, errors.New(status.Error)
	}
	if err := os.MkdirAll(status.Projects, 0o750); err != nil {
		status.Error = "Workspace-Root ist nicht verfügbar: " + err.Error()
		return status, errors.New(status.Error)
	}
	if err := os.MkdirAll(status.Runs, 0o700); err != nil {
		status.Error = "Run-Workspace ist nicht verfügbar: " + err.Error()
		return status, errors.New(status.Error)
	}
	if err := os.MkdirAll(status.Integrations, 0o750); err != nil {
		status.Error = "Integrations-Workspace ist nicht verfügbar: " + err.Error()
		return status, errors.New(status.Error)
	}
	if err := os.MkdirAll(status.Runtime, 0o700); err != nil {
		status.Error = "Runtime-Verzeichnis ist nicht verfügbar: " + err.Error()
		return status, errors.New(status.Error)
	}
	if err := os.Chmod(status.Runtime, 0o700); err != nil {
		status.Error = "Runtime-Verzeichnis ist nicht verfügbar: " + err.Error()
		return status, errors.New(status.Error)
	}
	tmp, err := os.CreateTemp(root, ".shipyard-preflight-*")
	if err != nil {
		status.Error = "Workspace-Root ist nicht beschreibbar"
		return status, errors.New(status.Error)
	}
	name := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(name)
	status.Writable = true
	_, err = exec.LookPath("git")
	status.Git = err == nil
	if !status.Git {
		status.Error = "Git ist auf dem Server nicht verfügbar"
		return status, errors.New(status.Error)
	}
	return status, nil
}

func invalidStatus(root, message string) (Status, error) {
	return Status{Root: root, Error: message}, errors.New(message)
}

func migrationInProgress(root string) bool {
	// Never create a lock path during validation. A missing mount must remain a
	// hard failure instead of becoming a local directory with a new lock file.
	f, err := os.Open(root)
	if err != nil {
		return true
	}
	defer f.Close()
	// Probe with a non-blocking shared lock. An exclusive migration lock
	// conflicts; the process's own shared run/service lock does not. An
	// exclusive probe would misclassify that shared lease as a migration
	// and pause every new run for the lifetime of the daemon.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

func (s Status) Ready() bool {
	return s.Writable && s.Git && !s.Migration && s.Error == ""
}

func (s Status) Summary() string {
	if s.Ready() {
		return fmt.Sprintf("bereit (%s)", s.Storage)
	}
	return s.Error
}

func ProjectsRoot() string {
	if root := Root(); root != "" {
		return filepath.Join(filepath.Clean(root), "projects")
	}
	return legacyProjectsRoot
}

func ProjectPath(id string) string { return filepath.Join(ProjectsRoot(), filepath.Base(id)) }

func RunsRoot() string {
	if root := Root(); root != "" {
		return filepath.Join(filepath.Clean(root), "runs")
	}
	return legacyRunsRoot
}

func IntegrationsRoot() string {
	if root := Root(); root != "" {
		return filepath.Join(filepath.Clean(root), "integrations")
	}
	return ""
}

// RuntimeRoot is the host directory for files the container runtime must be
// able to see. Configured workspaces use <root>/runtime, next to projects and
// runs. The legacy default is /home/agent/.taskboard-runtime. Callers must not
// put container bind sources in /tmp or /var/tmp: systemd PrivateTmp=true
// hides those directories from rootless dockerd.
func RuntimeRoot() string {
	if root := Root(); root != "" {
		return filepath.Join(filepath.Clean(root), "runtime")
	}
	return legacyRuntimeRoot
}
