package container

import (
	"os"
	"regexp"
	"strings"
)

const (
	// AgentBaseRepository is the GHCR image published by the Release workflow
	// from deploy/agent-base/Dockerfile.
	AgentBaseRepository = "ghcr.io/ralphschuler/shipyard-agent-base"
	agentBaseLatestTag  = "latest"
)

// releaseVersionTag matches the tags the Release workflow publishes
// (v0.1.45, and the same form with a pre-release or build suffix).
var releaseVersionTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$`)

// AgentBaseImage is the image a project without a Dev Container should pull.
// A release binary's TASKBOARD_VERSION (for example v0.1.45) pins the tag to
// the image that release published. development builds and any other value
// use latest.
func AgentBaseImage() string {
	return AgentBaseRepository + ":" + agentBaseTag(os.Getenv("TASKBOARD_VERSION"))
}

// AgentBaseLatestImage is the unpinned tag. Callers use it only when the
// release tag cannot be pulled.
func AgentBaseLatestImage() string {
	return AgentBaseRepository + ":" + agentBaseLatestTag
}

func agentBaseTag(version string) string {
	version = strings.TrimSpace(version)
	if releaseVersionTag.MatchString(version) {
		return version
	}
	return agentBaseLatestTag
}
