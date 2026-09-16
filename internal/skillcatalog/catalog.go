package skillcatalog

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"taskboard/internal/domain"
	"taskboard/internal/store"
)

func checkout(ctx context.Context, s domain.SkillSource) (string, error) {
	dir, e := os.MkdirTemp("", "taskboard-skills-")
	if e != nil {
		return "", e
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--branch", s.Branch, s.RepositoryURL, dir)
	if out, e := cmd.CombinedOutput(); e != nil {
		os.RemoveAll(dir)
		return "", fmtErr(string(out), e)
	}
	return dir, nil
}
func fmtErr(out string, e error) error { return &catalogError{out, e} }

type catalogError struct {
	out string
	err error
}

func (e *catalogError) Error() string { return e.err.Error() + ": " + e.out }
func Scan(ctx context.Context, st *store.Store, id string) error {
	s, e := st.GetSkillSource(ctx, id)
	if e != nil {
		return e
	}
	dir, e := checkout(ctx, s)
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, e error) error {
		if e != nil || d.IsDir() || d.Name() != "SKILL.md" {
			return e
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		name := strings.TrimSuffix(filepath.Base(filepath.Dir(path)), "/")
		desc := ""
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "description:") {
				desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
				break
			}
		}
		rel, _ := filepath.Rel(dir, filepath.Dir(path))
		_, e = st.UpsertSkill(ctx, s.ID, name, desc, rel)
		return e
	})
}
func Install(ctx context.Context, st *store.Store, id string) error {
	skill, e := st.GetSkill(ctx, id)
	if e != nil {
		return e
	}
	source, e := st.GetSkillSource(ctx, skill.SourceID)
	if e != nil {
		return e
	}
	dir, e := checkout(ctx, source)
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	target := filepath.Join("/home/agent/.taskboard-skills", skill.Name)
	if e = os.RemoveAll(target); e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
		return e
	}
	if out, copyErr := exec.CommandContext(ctx, "cp", "-a", filepath.Join(dir, skill.RepositoryPath), target).CombinedOutput(); copyErr != nil {
		return fmtErr(string(out), copyErr)
	}
	out, e := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	if e != nil {
		return e
	}
	_, e = st.InstallSkill(ctx, skill.ID, target, strings.TrimSpace(string(out)))
	return e
}
