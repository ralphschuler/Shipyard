package authz

import (
	"net/http"
	"testing"
)

func TestRoleRestrictionMatchesControlPanelMatrix(t *testing.T) {
	if status, _ := RoleRestriction(http.MethodGet, "/api/v1/settings/providers", "member"); status != http.StatusForbidden {
		t.Fatal("members must not read provider settings")
	}
	if status, _ := RoleRestriction(http.MethodPost, "/api/v1/boards", "member"); status != 0 {
		t.Fatal("members may create boards")
	}
	if status, _ := RoleRestriction(http.MethodPost, "/api/v1/boards", "viewer"); status != http.StatusForbidden {
		t.Fatal("viewers must not create boards")
	}
	if status, _ := RoleRestriction(http.MethodGet, "/api/v1/boards", "viewer"); status != 0 {
		t.Fatal("viewers may list boards")
	}
	if !IsAdminRole("owner") || !IsAdminRole("admin") || IsAdminRole("member") {
		t.Fatal("admin role check drifted")
	}
}
