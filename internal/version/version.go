// Package version exposes release metadata shared by every Xendfile binary.
package version

import (
	_ "embed"
	"strconv"
	"strings"
)

// These embedded text files are the repository's release metadata source of
// truth. Build and packaging scripts read the same files directly.
//
//go:embed VERSION
var versionText string

//go:embed PROTOCOL
var protocolText string

//go:embed MIN_COMPATIBLE_VERSION
var minimumCompatibleVersionText string

var (
	Current                  = strings.TrimSpace(versionText)
	Protocol                 = mustInteger(protocolText)
	MinimumCompatibleVersion = strings.TrimSpace(minimumCompatibleVersionText)
)

func mustInteger(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 1 {
		panic("invalid embedded Xendfile protocol version")
	}
	return parsed
}
