package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"taskboard/internal/domain"
	"taskboard/internal/store"
	"testing"
)

func TestBoardAPIAppliesCombinedFiltersAndIsolatesBoard(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("SHIPYARD_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("SHIPYARD_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	board, err := db.CreateBoardWithTemplate(ctx, "API filter board", "software")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.DeleteBoard(ctx, board.ID) })
	foreignBoard, err := db.CreateBoardWithTemplate(ctx, "API filter foreign board", "software")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.DeleteBoard(ctx, foreignBoard.ID) })
	project, err := db.CreateProject(ctx, "API filter project", "", "main", "", []string{board.ID})
	if err != nil {
		t.Fatal(err)
	}
	label, err := db.CreateLabel(ctx, board.ID, "API filter label", "#123456")
	if err != nil {
		t.Fatal(err)
	}
	matching, err := db.CreateTask(ctx, board.ID, "API Filter Treffer", "Beschreibung mit Suchbegriff", "high", "", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetLabels(ctx, matching.ID, []string{label.ID}); err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateTask(ctx, board.ID, "API Filter anderer Status", "Beschreibung mit Suchbegriff", "high", "", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	columns, err := db.Columns(ctx, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.MoveTaskToColumnID(ctx, other.ID, columns[1].ID, "test"); err != nil {
		t.Fatal(err)
	}
	foreign, err := db.CreateTask(ctx, foreignBoard.ID, "API Filter Treffer", "Beschreibung mit Suchbegriff", "high", "", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = foreign

	query := url.Values{}
	query.Set("search", "SUCHBEGRIFF")
	query.Set("column", matching.ColumnID)
	query.Set("priority", "high")
	query.Set("label", label.ID)
	query.Set("project", project.ID)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/boards/"+board.ID+"?"+query.Encode(), nil)
	res := httptest.NewRecorder()
	mux := http.NewServeMux()
	(&App{store: db}).Register(mux)
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	var payload struct {
		Board domain.Board
		Tasks []domain.Task
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Board.ID != board.ID {
		t.Fatalf("board = %#v, want %s", payload.Board, board.ID)
	}
	if len(payload.Tasks) != 1 || payload.Tasks[0].ID != matching.ID {
		t.Fatalf("filtered API tasks = %#v, want only %s", payload.Tasks, matching.ID)
	}
}

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
