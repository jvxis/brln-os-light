package releaseinfo

import (
	_ "embed"
	"strings"
)

// releaseVersion is compiled into every LightningOS binary from the same
// version.txt that Vite publishes with the UI. This gives upgrade security
// checks an immutable build-time version without release-specific Go edits.
//
//go:embed ui/public/version.txt
var releaseVersion string

// Version returns the release version embedded at build time.
func Version() string {
	return strings.TrimSpace(releaseVersion)
}
