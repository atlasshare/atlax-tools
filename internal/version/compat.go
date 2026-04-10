// Package version provides version tracking and compatibility checks
// for the ats CLI against atlax binary versions.
package version

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ToolVersion is the ats CLI version. Overridden by ldflags at build time.
var ToolVersion = "0.1.0-dev"

// BuildCommit is the git commit hash. Set by ldflags at build time.
var BuildCommit = "unknown"

// BuildDate is the build timestamp. Set by ldflags at build time.
var BuildDate = "unknown"

const (
	// Minimum atlax binary version this tool supports.
	MinAtlaxMajor = 0
	MinAtlaxMinor = 1
	MinAtlaxPatch = 0
)

// Semver holds a parsed semantic version.
type Semver struct {
	Major int
	Minor int
	Patch int
	Raw   string
}

func (s Semver) String() string {
	if s.Raw != "" {
		return s.Raw
	}
	return fmt.Sprintf("%d.%d.%d", s.Major, s.Minor, s.Patch)
}

// Parse extracts a semver from a string like "v0.2.1" or "0.2.1-beta".
func Parse(raw string) (Semver, error) {
	re := regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)`)
	m := re.FindStringSubmatch(raw)
	if m == nil {
		return Semver{Raw: raw}, fmt.Errorf("cannot parse version %q", raw)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	return Semver{Major: major, Minor: minor, Patch: patch, Raw: raw}, nil
}

// IsCompatible checks if a detected atlax version meets the minimum.
func IsCompatible(v Semver) bool {
	if v.Major != MinAtlaxMajor {
		return v.Major > MinAtlaxMajor
	}
	if v.Minor != MinAtlaxMinor {
		return v.Minor > MinAtlaxMinor
	}
	return v.Patch >= MinAtlaxPatch
}

// DetectBinaryVersion runs an atlax binary with --version and parses output.
func DetectBinaryVersion(binaryPath string) (Semver, error) {
	out, err := exec.Command(binaryPath, "--version").CombinedOutput()
	if err != nil {
		// Try -version (some older builds)
		out, err = exec.Command(binaryPath, "-version").CombinedOutput()
		if err != nil {
			return Semver{}, fmt.Errorf("could not detect version for %s: %w", binaryPath, err)
		}
	}
	return Parse(strings.TrimSpace(string(out)))
}
