package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"taskboard/internal/store"
)

func TestMCPAuthorizationUsesLiveRoleAndLeavesAudit(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405000000000")
	email := "mcp-authz-" + suffix + "@example.test"
	var userID string
	if err := s.DB.QueryRow(ctx, `INSERT INTO users(email,display_name,password_hash,role) VALUES($1,$2,'unused-hash','member') RETURNING id::text`, email, "MCP Authz "+suffix).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.DB.Exec(ctx, `DELETE FROM audit_events WHERE user_id=$1::uuid`, userID)
		_, _ = s.DB.Exec(ctx, `DELETE FROM api_tokens WHERE user_id=$1::uuid`, userID)
		_, _ = s.DB.Exec(ctx, `DELETE FROM users WHERE id=$1::uuid`, userID)
	})

	raw := "tb_mcpauthz" + suffix
	sum := sha256.Sum256([]byte(raw))
	hash := base64.RawURLEncoding.EncodeToString(sum[:])
	if _, err := s.CreateAPIToken(ctx, userID, "member-token", hash, raw[:11], nil); err != nil {
		t.Fatal(err)
	}

	server := New(s, nil)
	providersBefore, err := s.Providers(ctx)
	if err != nil {
		t.Fatal(err)
	}

	forbidden := mcpCall(t, server, raw, "update_provider_setting", map[string]string{
		"provider": "openai", "model": "should-not-save", "enabled": "true",
	})
	if forbidden.Error.Code != -32001 || forbidden.Error.Message != errForbidden.Error() {
		t.Fatalf("member provider update = %#v, want forbidden", forbidden.Error)
	}
	providersAfter, err := s.Providers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(providersAfter) != len(providersBefore) {
		t.Fatalf("forbidden provider update changed settings: before=%d after=%d", len(providersBefore), len(providersAfter))
	}
	assertAudit(t, s, userID, "mcp.update_provider_setting", "forbidden")

	created := mcpCall(t, server, raw, "create_board", map[string]string{"name": "mcp-authz-board-" + suffix})
	if created.Error.Message != "" {
		t.Fatalf("member create_board failed: %#v", created.Error)
	}
	t.Cleanup(func() {
		boards, listErr := s.ListBoards(ctx)
		if listErr != nil {
			return
		}
		for _, board := range boards {
			if strings.Contains(board.Name, suffix) {
				_ = s.DeleteBoard(ctx, board.ID)
			}
		}
	})

	if _, err = s.DB.Exec(ctx, `UPDATE users SET role='viewer' WHERE id=$1::uuid`, userID); err != nil {
		t.Fatal(err)
	}
	denied := mcpCall(t, server, raw, "create_board", map[string]string{"name": "mcp-authz-demoted-" + suffix})
	if denied.Error.Code != -32001 || denied.Error.Message != errForbidden.Error() {
		t.Fatalf("demoted viewer create_board = %#v, want forbidden", denied.Error)
	}
	boards, err := s.ListBoards(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, board := range boards {
		if board.Name == "mcp-authz-demoted-"+suffix {
			t.Fatal("demoted viewer created a board")
		}
	}
	assertAudit(t, s, userID, "mcp.create_board", "forbidden")
}

type mcpResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func mcpCall(t *testing.T, server *Server, token, name string, arguments map[string]string) mcpResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": arguments},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("%s HTTP status = %d body=%s", name, res.Code, res.Body.String())
	}
	var payload mcpResponse
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func assertAudit(t *testing.T, s *store.Store, userID, kind, status string) {
	t.Helper()
	var got string
	err := s.DB.QueryRow(context.Background(), `SELECT metadata->>'status' FROM audit_events WHERE user_id=$1::uuid AND kind=$2 ORDER BY created_at DESC LIMIT 1`, userID, kind).Scan(&got)
	if err != nil {
		t.Fatalf("audit %s: %v", kind, err)
	}
	if got != status {
		t.Fatalf("audit %s status = %q, want %q", kind, got, status)
	}
}

func integrationStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("SHIPYARD_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("SHIPYARD_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	t.Cleanup(func() { s.DB.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate integration database: %v", err)
	}
	return s
}
