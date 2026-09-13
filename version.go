package pingtunnel

import (
	"fmt"
	"strings"
)

// Version is manually set in code and bumped when a release is intended.
const Version = "2.9"

// Build-time variables injected via -ldflags.
var (
	BuildTime = ""
	GitCommit = ""
	GitBranch = ""
)

// GetVersionInfo returns a formatted string containing version, build time, and git commit.
func GetVersionInfo() string {
	parts := []string{fmt.Sprintf("version %s", Version)}
	if GitCommit != "" {
		parts = append(parts, fmt.Sprintf("git %s", GitCommit))
	}
	if GitBranch != "" {
		parts = append(parts, fmt.Sprintf("branch %s", GitBranch))
	}
	if BuildTime != "" {
		parts = append(parts, fmt.Sprintf("built at %s", BuildTime))
	}
	return strings.Join(parts, ", ")
}
