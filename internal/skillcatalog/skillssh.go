package skillcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"taskboard/internal/domain"
	"taskboard/internal/store"
	"time"
)

// Variable rather than a constant so the response contract can be exercised
// against an isolated HTTP test server.
var skillsSHSearchURL = "https://skills.sh/api/search"

var skillSourcePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var skillSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

type skillsSHResponse struct {
	Skills []struct {
		ID, SkillID, Name, Source string
		Installs                  int
	} `json:"skills"`
}

// SearchSkillsSH uses the public search endpoint that powers `skills find`.
// Search instead of a full import is essential: the catalog is intentionally
// global and changes too quickly to turn into local, stale database rows.
func SearchSkillsSH(ctx context.Context, query string) ([]domain.CatalogSkill, error) {
	query = strings.TrimSpace(query)
	if len([]rune(query)) < 2 {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, skillsSHSearchURL+"?q="+url.QueryEscape(query)+"&limit=24", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 8 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skills.sh search returned %s", response.Status)
	}
	var body skillsSHResponse
	if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&body); err != nil {
		return nil, err
	}
	result := make([]domain.CatalogSkill, 0, len(body.Skills))
	for _, skill := range body.Skills {
		if !skillSourcePattern.MatchString(skill.Source) || !skillSlugPattern.MatchString(skill.SkillID) {
			continue
		}
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			name = skill.SkillID
		}
		result = append(result, domain.CatalogSkill{Source: skill.Source, Slug: skill.SkillID, Name: name, Installs: skill.Installs, URL: "https://skills.sh/" + skill.Source + "/" + skill.SkillID})
	}
	return result, nil
}

// InstallSkillsSH delegates the source acquisition to skills.sh's own CLI.
// It installs into a throw-away project directory first, then copies only the
// selected skill into Shipyard's dedicated agent library. The CLI also keeps
// skills.sh's source and security metadata in its own install protocol.
func InstallSkillsSH(ctx context.Context, st *store.Store, source, slug string) error {
	source, slug = strings.TrimSpace(source), strings.TrimSpace(slug)
	if !skillSourcePattern.MatchString(source) || !skillSlugPattern.MatchString(slug) {
		return errors.New("ungültige skills.sh-Quelle oder Skill-ID")
	}
	// Hidden form fields are not authority. Confirm that this exact publisher
	// and skill are currently returned by skills.sh before letting its CLI
	// acquire source code. That keeps the integration catalog-backed instead
	// of turning it into a generic Git repository installer.
	catalog, err := SearchSkillsSH(ctx, slug)
	if err != nil {
		return errors.New("skills.sh-Katalog konnte vor der Installation nicht geprüft werden")
	}
	confirmed := false
	for _, candidate := range catalog {
		if candidate.Source == source && candidate.Slug == slug {
			confirmed = true
			break
		}
	}
	if !confirmed {
		return errors.New("dieser Skill ist nicht mehr im skills.sh-Katalog verfügbar")
	}
	temporary, err := os.MkdirTemp("", "taskboard-skills-sh-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	cmd := exec.CommandContext(ctx, "npx", "--yes", "skills", "add", source, "--skill", slug, "--agent", "codex", "--copy", "-y", "--json")
	cmd.Dir = temporary
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmtErr(string(out), err)
	}
	from := filepath.Join(temporary, ".agents", "skills", slug)
	if info, statErr := os.Stat(from); statErr != nil || !info.IsDir() {
		return errors.New("skills.sh hat keinen installierbaren Skill geliefert")
	}
	// A source-qualified directory prevents different publishers' equally named
	// skills from overwriting each other.
	target := filepath.Join("/home/agent/.taskboard-skills", strings.ReplaceAll(source, "/", "__"), slug)
	if err = os.RemoveAll(target); err != nil {
		return err
	}
	if err = copyTree(from, target); err != nil {
		return err
	}
	hash, err := treeHash(target)
	if err != nil {
		return err
	}
	canonical, err := st.EnsureSkillsSHSource(ctx)
	if err != nil {
		return err
	}
	skill, err := st.UpsertSkill(ctx, canonical.ID, slug, "skills.sh · "+source, source+"@"+slug)
	if err != nil {
		return err
	}
	_, err = st.InstallSkill(ctx, skill.ID, target, hash)
	return err
}

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(from, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return errors.New("ungültiger Skill-Pfad")
		}
		destination := filepath.Join(to, rel)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symbolische Links in skills.sh-Skills sind nicht erlaubt")
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func treeHash(root string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, err = io.WriteString(hash, rel+"\x00"); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(hash, file)
		return err
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
