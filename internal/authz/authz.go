package authz

import (
	"net/http"
	"strings"
)

func UnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func IsAdminRole(role string) bool {
	return role == "owner" || role == "admin"
}

// CanonicalAPIPath maps JSON API paths onto the same resource space as the
// HTML control panel. /api/v1/settings/providers and /settings/providers must
// share one authorization policy; a prefix check on the raw request path
// would leave the JSON surface open.
func CanonicalAPIPath(path string) string {
	switch {
	case path == "/api/v1":
		return "/"
	case strings.HasPrefix(path, "/api/v1/"):
		return strings.TrimPrefix(path, "/api/v1")
	default:
		return path
	}
}

func IsAdminArea(path string) bool {
	path = CanonicalAPIPath(path)
	return strings.HasPrefix(path, "/settings/") ||
		strings.HasPrefix(path, "/agents") ||
		strings.HasPrefix(path, "/automations") ||
		strings.HasPrefix(path, "/skills") ||
		strings.HasPrefix(path, "/schedules") ||
		strings.HasPrefix(path, "/webhooks") ||
		strings.HasPrefix(path, "/audit") ||
		path == "/sandbox-profiles" ||
		strings.HasPrefix(path, "/sandbox-profiles/")
}

// RoleRestriction is the single member/admin split used by the control panel
// and MCP. Viewer mutation blocking is a second, narrower rule and does not
// grant members access to administrative surfaces.
func RoleRestriction(method, path, role string) (int, string) {
	if IsAdminArea(path) && !IsAdminRole(role) {
		return http.StatusForbidden, "Diese Aktion erfordert Administratorrechte."
	}
	if UnsafeMethod(method) && role == "viewer" {
		return http.StatusForbidden, "Diese Rolle darf keine Änderungen vornehmen."
	}
	return 0, ""
}
