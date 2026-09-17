package web

import (
	"net/url"
	"taskboard/internal/domain"
	"testing"
)

func TestFilterBoardTasksCombinesSearchAndFiltersWithoutCrossBoardMatches(t *testing.T) {
	tasks := []domain.Task{
		{ID: "matching", BoardID: "board-a", ColumnID: "todo", Title: "Login reparieren", Description: "Mobile Formularprüfung", Priority: "high", Labels: []domain.Label{{ID: "bug"}}, TargetProjects: []domain.Project{{ID: "website"}}},
		{ID: "wrong-board", BoardID: "board-b", ColumnID: "todo", Title: "Login reparieren", Description: "Mobile Formularprüfung", Priority: "high", Labels: []domain.Label{{ID: "bug"}}, TargetProjects: []domain.Project{{ID: "website"}}},
		{ID: "wrong-filter", BoardID: "board-a", ColumnID: "done", Title: "Login reparieren", Description: "Mobile Formularprüfung", Priority: "normal", Labels: []domain.Label{{ID: "ux"}}, TargetProjects: []domain.Project{{ID: "website"}}},
	}
	query := url.Values{}
	query.Set("search", "MOBILE FORM")
	query.Set("column", "todo")
	query.Set("priority", "high")
	query.Set("label", "bug")
	query.Set("project", "website")
	filtered := filterBoardTasks(tasks, "board-a", query)
	if len(filtered) != 1 || filtered[0].ID != "matching" {
		t.Fatalf("filtered tasks = %#v, want only matching task", filtered)
	}
}

func TestFilterBoardTasksSupportsPartialAndEmptyQueries(t *testing.T) {
	tasks := []domain.Task{
		{ID: "one", Title: "Deploy vorbereiten", Description: "Release notes prüfen"},
		{ID: "two", Title: "Dokumentation", Description: "Release notes ergänzen"},
	}
	query := url.Values{"search": []string{"deploy"}}
	if got := filterBoardTasks(tasks, "", query); len(got) != 1 || got[0].ID != "one" {
		t.Fatalf("partial search result = %#v", got)
	}
	if got := filterBoardTasks(tasks, "", url.Values{}); len(got) != len(tasks) {
		t.Fatalf("empty query result length = %d, want %d", len(got), len(tasks))
	}
}

func TestFilterBoardTasksMatchesExactTermsAndReturnsEmptyResults(t *testing.T) {
	tasks := []domain.Task{
		{ID: "exact", BoardID: "board-a", Title: "Release vorbereiten", Description: "Notizen prüfen"},
		{ID: "partial", BoardID: "board-a", Title: "Release planen", Description: "Zeitplan abstimmen"},
	}

	query := url.Values{"search": []string{"Release vorbereiten"}}
	got := filterBoardTasks(tasks, "board-a", query)
	if len(got) != 1 || got[0].ID != "exact" {
		t.Fatalf("exact search result = %#v, want exact task only", got)
	}

	query.Set("search", "does-not-exist")
	if got := filterBoardTasks(tasks, "board-a", query); len(got) != 0 {
		t.Fatalf("empty search result = %#v, want no tasks", got)
	}
}
