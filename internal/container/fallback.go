package container

import (
	_ "embed"
	"os"
	"path/filepath"
)

// FallbackImage is the tag used when a project has no .devcontainer.
// The Dockerfile pins the same Ubuntu, Go, and Node.js versions as the
// repository Dev Container, without the IDE-only Playwright and PostgreSQL
// services.
const FallbackImage = "shipyard/agent-fallback:1"

//go:embed fallback/Dockerfile
var fallbackDockerfile []byte

// MaterializeFallback writes the embedded fallback Dockerfile into dir so a
// provider can build it without a checkout of deploy/agent-container.
func MaterializeFallback(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "Dockerfile"), fallbackDockerfile, 0o644)
}

// FallbackDockerfile returns the embedded image definition.
func FallbackDockerfile() []byte {
	return append([]byte(nil), fallbackDockerfile...)
}
