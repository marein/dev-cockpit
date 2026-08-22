// Package distro is the importable entry point of dev-cockpit. The shipped
// binary is the thin main in cmd/dev-cockpit; a custom distribution writes a
// main of its own that calls Main, see the custom distributions section of
// the README. The CLI itself lives in internal/cli, this package is the
// facade in front of it.
package distro

import (
	"fmt"
	"os"
	"regexp"

	"github.com/marein/dev-cockpit/internal/cli"
	"github.com/marein/dev-cockpit/plugin"
)

// Build describes the distribution a binary belongs to: the release version,
// the web page of its repository, the release feed update checks read, in
// github or gitlab format, and the plugins compiled in. A zero field keeps
// the compiled default.
type Build struct {
	Version          string
	RepoURL          string
	UpdateFeedURL    string
	UpdateFeedFormat string
	// ServePlugins are constructed by the distribution's main and handed in
	// here as ordered named pairs, there is no registry and a plugin does not
	// name itself. They configure at serve start, when the configuration they
	// read is loaded. The surface is experimental, see the plugin package.
	ServePlugins []plugin.Named[plugin.ServePlugin]
}

// Main runs the dev-cockpit CLI and exits the process on an error. It is the
// one call a distribution main makes.
func Main(b Build) {
	// A bad wiring list is a build mistake and fails every invocation before
	// anything else runs, like an unknown update feed format does. Only the
	// wiring is checked here: the plugins themselves configure at serve
	// start, see plugin.ConfigureServe.
	if err := validateWiring(b.ServePlugins); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cli.Main(cli.Build{
		Version:          b.Version,
		RepoURL:          b.RepoURL,
		UpdateFeedURL:    b.UpdateFeedURL,
		UpdateFeedFormat: b.UpdateFeedFormat,
		ServePlugins:     b.ServePlugins,
	})
}

// idPattern is what a wiring id may look like: lowercase, starts with a
// letter, letters, digits and dashes. The id prefixes the final custom
// element names, so it stays this narrow.
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// validateWiring refuses, per plugin list, what the id prefixes and the
// error messages could not tell apart later: a nil plugin, an empty or
// element name incompatible id, two plugins under one id. Positions count
// from one, matching how the list reads in the main.
func validateWiring[T any](plugins []plugin.Named[T]) error {
	seen := make(map[string]bool, len(plugins))
	for i, p := range plugins {
		if any(p.Plugin) == nil {
			return fmt.Errorf("plugin %d is nil", i+1)
		}
		if p.ID == "" {
			return fmt.Errorf("plugin %d has an empty id", i+1)
		}
		if !idPattern.MatchString(p.ID) {
			return fmt.Errorf("%q is not a usable plugin id, use lowercase words joined by dashes", p.ID)
		}
		if seen[p.ID] {
			return fmt.Errorf("two plugins carry the id %q", p.ID)
		}
		seen[p.ID] = true
	}
	return nil
}
