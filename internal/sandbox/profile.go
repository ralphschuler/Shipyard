package sandbox

import (
	"errors"
	"fmt"
	"path/filepath"
)

// Profile is the small, versioned policy vocabulary understood by the worker.
// It intentionally contains no raw bubblewrap flags.
type Profile struct {
	Name        string
	Description string
	Mounts      []string
	NetworkMode string
	WriteMode   string
	Active      bool
}

var profiles = map[string]Profile{
	"strict":         {Name: "strict", Description: "Worktree-only, no network", Mounts: []string{"worktree"}, NetworkMode: "none", WriteMode: "worktree", Active: true},
	"development":    {Name: "development", Description: "Worktree-only development", Mounts: []string{"worktree"}, NetworkMode: "none", WriteMode: "worktree", Active: true},
	"qa-readonly":    {Name: "qa-readonly", Description: "Read-only checks with explicit network policy", Mounts: []string{"worktree"}, NetworkMode: "none", WriteMode: "readonly", Active: true},
	"release-bridge": {Name: "release-bridge", Description: "Host-side release bridge only", Mounts: []string{"worktree"}, NetworkMode: "bridge-only", WriteMode: "readonly", Active: true},
}

func All() []Profile {
	result := make([]Profile, 0, len(profiles))
	for _, p := range profiles {
		result = append(result, p)
	}
	return result
}

func Get(name string) (Profile, error) {
	p, ok := profiles[name]
	if !ok || !p.Active {
		return Profile{}, fmt.Errorf("sandbox profile %q is unknown or inactive", name)
	}
	return p, nil
}

func Validate(name string) error { _, err := Get(name); return err }

// ValidateMounts rejects any caller-provided host mounts. Mounts are policy
// metadata and the worker derives the actual bind from the assigned worktree.
func ValidateMounts(mounts []string) error {
	for _, mount := range mounts {
		if mount != "worktree" {
			return errors.New("sandbox mounts must use the worktree allowlist")
		}
	}
	return nil
}

func Effective(name, worktree string) (Profile, error) {
	p, err := Get(name)
	if err != nil {
		return Profile{}, err
	}
	if !filepath.IsAbs(worktree) || worktree == "/" {
		return Profile{}, errors.New("sandbox worktree must be an absolute non-root path")
	}
	return p, nil
}
