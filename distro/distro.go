// Package distro is the importable entry point of dev-cockpit. The shipped
// binary is the thin main in cmd/dev-cockpit; a custom distribution writes a
// main of its own that calls Main, see the custom distributions section of
// the README. The CLI itself lives in internal/cli, this package is the
// facade in front of it.
package distro

import "github.com/marein/dev-cockpit/internal/cli"

// Build describes the distribution a binary belongs to: the release version,
// the web page of its repository, and the release feed update checks read,
// in github or gitlab format. A zero field keeps the compiled default.
type Build struct {
	Version          string
	RepoURL          string
	UpdateFeedURL    string
	UpdateFeedFormat string
}

// Main runs the dev-cockpit CLI and exits the process on an error. It is the
// one call a distribution main makes.
func Main(b Build) {
	cli.Main(cli.Build{
		Version:          b.Version,
		RepoURL:          b.RepoURL,
		UpdateFeedURL:    b.UpdateFeedURL,
		UpdateFeedFormat: b.UpdateFeedFormat,
	})
}
