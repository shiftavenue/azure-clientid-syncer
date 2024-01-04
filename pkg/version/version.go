package version

import (
	"fmt"
	"runtime"
)

var (
	// Vcs is the commit hash for the binary build
	Vcs string
	// BuildTime is the date for the binary build
	BuildTime string
	// BuildVersion is the aks-clientid-syncer version. Will be overwritten from build.
	BuildVersion string
)

// GetUserAgent returns a user agent of the format: aks-clientid-syncer/<version> (<goos>/<goarch>) <vcs>/<timestamp>
func GetUserAgent(component string) string {
	return fmt.Sprintf("aks-clientid-syncer/%s/%s (%s/%s) %s/%s", component, BuildVersion, runtime.GOOS, runtime.GOARCH, Vcs, BuildTime)
}
