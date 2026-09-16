package skillcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchSkillsSHMapsOnlySafeCatalogEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "postgres database" {
			t.Fatalf("query = %q", got)
		}
		_, _ = w.Write([]byte(`{"skills":[
			{"skillId":"postgres-setup","name":"Postgres setup","source":"acme/skills","installs":42},
			{"skillId":"../../escape","name":"bad","source":"acme/skills","installs":1},
			{"skillId":"valid","name":"bad source","source":"../../etc","installs":1}
		]}`))
	}))
	defer server.Close()

	previous := skillsSHSearchURL
	skillsSHSearchURL = server.URL
	defer func() { skillsSHSearchURL = previous }()

	got, err := SearchSkillsSH(context.Background(), "postgres database")
	if err != nil {
		t.Fatalf("SearchSkillsSH: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("result length = %d, want 1: %#v", len(got), got)
	}
	if got[0].Source != "acme/skills" || got[0].Slug != "postgres-setup" || got[0].URL != "https://skills.sh/acme/skills/postgres-setup" {
		t.Fatalf("unexpected mapped skill: %#v", got[0])
	}
}

func TestSkillsSHRejectsUnsafeInstallIdentifiersBeforeRunningCLI(t *testing.T) {
	if err := InstallSkillsSH(context.Background(), nil, "../../etc", "bad"); err == nil {
		t.Fatal("unsafe source must be rejected")
	}
	if err := InstallSkillsSH(context.Background(), nil, "acme/skills", "../bad"); err == nil {
		t.Fatal("unsafe slug must be rejected")
	}
}

func TestInstallSkillsSHRejectsCatalogMismatchBeforeRunningCLI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"skills":[{"skillId":"allowed","name":"Allowed","source":"acme/skills"}]}`))
	}))
	defer server.Close()
	previous := skillsSHSearchURL
	skillsSHSearchURL = server.URL
	defer func() { skillsSHSearchURL = previous }()

	if err := InstallSkillsSH(context.Background(), nil, "other/skills", "allowed"); err == nil {
		t.Fatal("catalog mismatch must be rejected before the CLI is run")
	}
}

func TestSearchSkillsSHNeedsUsefulQuery(t *testing.T) {
	got, err := SearchSkillsSH(context.Background(), " ")
	if err != nil {
		t.Fatalf("short query: %v", err)
	}
	if got != nil {
		t.Fatalf("short query = %#v, want nil", got)
	}
}
