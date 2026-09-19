package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// RunGate is a shared lease for run creation/start. Migration has a separate
// lock so normal independent runs do not look like an active migration.
type RunGate struct{ file *os.File }

func (g *RunGate) Close() error {
	if g == nil || g.file == nil {
		return nil
	}
	err := syscall.Flock(int(g.file.Fd()), syscall.LOCK_UN)
	closeErr := g.file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func acquireGate(root string, exclusive bool) (*RunGate, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("Workspace-Root ist nicht verfügbar")
	}
	// Lock the existing root directory itself. In particular, do not create a
	// lock file: doing so at a missing NFS mountpoint would silently prepare a
	// local fallback and defeat the fail-closed preflight.
	f, err := os.Open(root)
	if err != nil {
		return nil, errors.New("Workspace-Sperre konnte nicht geöffnet werden")
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(f.Fd()), mode); err != nil {
		_ = f.Close()
		return nil, errors.New("Workspace-Sperre konnte nicht übernommen werden")
	}
	return &RunGate{file: f}, nil
}

func acquireMigrationGate(root string) (*RunGate, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("Workspace-Root ist nicht verfügbar")
	}
	f, err := os.OpenFile(filepath.Join(root, ".shipyard-migration.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errors.New("Migrations-Sperre konnte nicht geöffnet werden")
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, errors.New("Migrations-Sperre konnte nicht übernommen werden")
	}
	return &RunGate{file: f}, nil
}

// AcquireRunGate is used around queue insertion and run claiming.
func AcquireRunGate() (*RunGate, error) { return acquireGate(configuredRoot(), false) }

type MigrationItem struct{ Kind, Name, Status string }
type MigrationState struct {
	Source, Target string
	Items          []MigrationItem
}

// Migrate copies managed checkouts and worktrees into targetRoot. It is
// restartable: completed valid Git directories are retained, and each item is
// published with rename after its copy validates. Active run paths are left in
// place; callers must pass their durable run worktree paths.
func Migrate(sourceRoot, targetRoot string, activeRunPaths map[string]bool) (MigrationState, error) {
	sourceRoot = filepath.Clean(strings.TrimSpace(sourceRoot))
	targetRoot = filepath.Clean(strings.TrimSpace(targetRoot))
	if sourceRoot == "" || targetRoot == "" || !filepath.IsAbs(sourceRoot) || !filepath.IsAbs(targetRoot) || sourceRoot == "/" || targetRoot == "/" {
		return MigrationState{}, errors.New("Quell- und Ziel-Workspace müssen absolute Nicht-Root-Pfade sein")
	}
	if sourceRoot == targetRoot {
		return MigrationState{}, errors.New("Quell- und Ziel-Workspace müssen verschieden sein")
	}
	sourceGate, err := acquireMigrationGate(sourceRoot)
	if err != nil {
		return MigrationState{}, err
	}
	defer sourceGate.Close()
	gate, err := acquireMigrationGate(targetRoot)
	if err != nil {
		return MigrationState{}, err
	}
	defer gate.Close()
	// Validate only after taking the target lock. This closes the TOCTOU window
	// in which an NFS mount could disappear after preflight and a local path
	// could be created or reused before copying starts.
	if err := validateMigrationTarget(targetRoot); err != nil {
		return MigrationState{}, err
	}
	state := MigrationState{Source: sourceRoot, Target: targetRoot}
	statePath := filepath.Join(targetRoot, ".shipyard-migration.json")
	for _, kind := range []string{"projects", "runs", "integrations"} {
		src := filepath.Join(sourceRoot, kind)
		entries, readErr := os.ReadDir(src)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return state, fmt.Errorf("%s konnten nicht gelesen werden", kind)
		}
		for _, entry := range entries {
			name := entry.Name()
			srcPath := filepath.Join(src, name)
			dstPath := filepath.Join(targetRoot, kind, name)
			if kind == "runs" && activeRunPaths[srcPath] {
				state.Items = append(state.Items, MigrationItem{kind, name, "zurückgestellt: laufender Run"})
				continue
			}
			if validGitCheckout(dstPath) {
				state.Items = append(state.Items, MigrationItem{kind, name, "bewahrt"})
				continue
			}
			if err := copyPublished(srcPath, dstPath); err != nil {
				return state, fmt.Errorf("%s konnten nicht migriert werden", kind)
			}
			if !validGitCheckout(dstPath) && kind != "runs" && kind != "integrations" {
				return state, errors.New("migrierter Projekt-Checkout ist kein gültiges Git-Repository")
			}
			state.Items = append(state.Items, MigrationItem{kind, name, "migriert"})
			if err := writeMigrationState(statePath, state); err != nil {
				return state, err
			}
		}
	}
	_ = os.Remove(statePath)
	return state, nil
}

func writeMigrationState(path string, state MigrationState) error {
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return errors.New("Migrationsstatus konnte nicht gespeichert werden")
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return errors.New("Migrationsstatus konnte nicht atomar umgeschaltet werden")
	}
	return nil
}

func copyPublished(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	tmp := dst + ".shipyard-copying"
	_ = os.RemoveAll(tmp)
	if err := copyTree(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		if recovery, err := atomicReplaceDirectory(tmp, dst); err != nil {
			// After a successful exchange, prepared contains the old
			// destination and is the only recovery copy. Never delete it.
			if recovery == "" {
				_ = os.RemoveAll(tmp)
			}
			return err
		}
		return nil
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return nil
}

// atomicReplaceDirectory exchanges two directories in one namespace
// operation. Unlike rename(old, rollback) followed by rename(tmp, dst), the
// destination never disappears, so a crash cannot expose a half-switched
// workspace. The old destination is then retained as a recovery directory;
// if that second rename fails, the new destination is still live and the old
// tree remains at prepared.
func atomicReplaceDirectory(prepared, destination string) (string, error) {
	rollback := fmt.Sprintf("%s.shipyard-rollback-%d", destination, time.Now().UnixNano())
	if err := unix.Renameat2(unix.AT_FDCWD, prepared, unix.AT_FDCWD, destination, unix.RENAME_EXCHANGE); err != nil {
		return "", errors.New("migrierter Workspace konnte nicht atomar veröffentlicht werden")
	}
	if err := os.Rename(prepared, rollback); err != nil {
		return prepared, fmt.Errorf("neuer Workspace ist veröffentlicht, alter Stand liegt zur Wiederaufnahme unter %s", prepared)
	}
	return rollback, nil
}

// validateMigrationTarget requires an operator-created marker before any
// directory is created. This is the migration equivalent of Validate's
// fail-closed behavior and prevents a missing NFS mount becoming local data.
func validateMigrationTarget(root string) error {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return errors.New("Ziel-Workspace ist nicht eingehängt oder nicht verfügbar")
	}
	marker, err := os.Lstat(filepath.Join(root, markerName))
	if err != nil || !marker.Mode().IsRegular() {
		return errors.New("Ziel-Workspace ist nicht als Shipyard-Speicher markiert")
	}
	markerContent, err := os.ReadFile(filepath.Join(root, markerName))
	if err != nil {
		return errors.New("Shipyard-Speichermarker konnte nicht gelesen werden")
	}
	if strings.Contains(strings.ToLower(string(markerContent)), "storage=nfs") {
		fs := new(syscall.Statfs_t)
		if err := syscall.Statfs(root, fs); err != nil || uint64(fs.Type) != 0x6969 {
			return errors.New("Ziel-Workspace ist nicht als NFS eingehängt")
		}
	}
	tmp, err := os.CreateTemp(root, ".shipyard-migration-preflight-*")
	if err != nil {
		return errors.New("Ziel-Workspace ist nicht beschreibbar")
	}
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return errors.New("Ziel-Workspace ist nicht beschreibbar")
	}
	_ = os.Remove(name)
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("Git ist auf dem Server nicht verfügbar")
	}
	return nil
}

func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("symlinks sind in verwalteten Workspaces nicht erlaubt")
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func validGitCheckout(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	cmd := exec.Command("git", "-C", path, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}
