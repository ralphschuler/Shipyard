package govulngate

import (
	"fmt"
	"strconv"
	"strings"
)

type Version struct {
	Major, Minor, Patch int
	Raw                 string
}

func ParseGoVersion(raw string) (Version, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "go")
	if fields := strings.Fields(trimmed); len(fields) > 0 {
		trimmed = fields[0]
	}
	if trimmed == "" {
		return Version{}, fmt.Errorf("empty Go version")
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) < 2 {
		return Version{}, fmt.Errorf("invalid Go version %q", raw)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Version{}, fmt.Errorf("invalid Go version %q", raw)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return Version{}, fmt.Errorf("invalid Go version %q", raw)
	}
	patch := 0
	if len(parts) > 2 {
		patchPart := parts[2]
		if idx := strings.IndexAny(patchPart, "-+"); idx >= 0 {
			patchPart = patchPart[:idx]
		}
		patch, err = strconv.Atoi(patchPart)
		if err != nil {
			return Version{}, fmt.Errorf("invalid Go version %q", raw)
		}
	}
	return Version{Major: major, Minor: minor, Patch: patch, Raw: strings.TrimSpace(raw)}, nil
}

func (v Version) Less(other Version) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor < other.Minor
	}
	return v.Patch < other.Patch
}

func IsPatched(version, minimum string) error {
	got, err := ParseGoVersion(version)
	if err != nil {
		return err
	}
	need, err := ParseGoVersion(minimum)
	if err != nil {
		return err
	}
	if got.Less(need) {
		return fmt.Errorf("%s is older than required patched toolchain %s", got.Raw, need.Raw)
	}
	return nil
}
