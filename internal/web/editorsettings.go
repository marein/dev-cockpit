package web

import (
	"strconv"

	"github.com/marein/dev-cockpit/internal/editorintelligence"
	"github.com/marein/dev-cockpit/internal/filesystem"
)

// The editor's cross-device settings, edited on the tabs of /settings/editor and
// stored as flat values in the shared settings store. A fresh install has no
// entries at all, so the defaults below are what actually applies, and a value
// that was stored once and later became invalid falls back to the same default.
// The keys are hyphenated like every other one in that file; the form fields
// they are written from keep the underscores every form in the app uses.
const (
	editorGitPollSecondsKey  = "editor-git-poll-seconds"
	editorFilePollSecondsKey = "editor-file-poll-seconds"
	editorExclusionsKey      = "editor-search-exclusions"
	editorDiffMaxLinesKey    = "editor-diff-max-lines"
	editorDiffMaxKiBKey      = "editor-diff-max-kib"
)

// editorLSPServerKey holds how a language's LSP server runs, one key per
// profile. The stored value is the server's name with the "-docker"
// marker, "off", or "auto". Absent, "auto" and unknown values all mean
// the automatic default, so a later version may add one more way to run
// a server without an old value hiding the feature.
func editorLSPServerKey(profileID string) string {
	return "editor-lsp-" + profileID
}

// lspChoice normalizes one stored value onto the three the select offers,
// "auto" for everything unknown. The settings page renders through it and
// the save accepts exactly what it answers unchanged, so the value scheme
// lives in this one place.
func lspChoice(stored string, p *editorintelligence.Profile) string {
	switch stored {
	case "off", p.Server + "-docker":
		return stored
	}
	return "auto"
}

// resolveLSPMode turns one stored value into whether the server runs. The
// two explicit values mean what they always meant, runnable or not.
// Everything else is the automatic default: Docker while it can run, else
// off, quietly.
func resolveLSPMode(stored, server string, dockerOK bool) (off bool) {
	switch stored {
	case "off":
		return true
	case server + "-docker":
		return false
	}
	return !dockerOK
}

// lspDockerHost answers the configured daemon the docker cache resolved,
// the same one the availability gate reads.
func (s *Server) lspDockerHost() string {
	return s.docker.State().Host
}

// lspCacheRoot is where the language servers' per project cache
// directories live, under this instance's state directory.
func (s *Server) lspCacheRoot() string {
	return editorintelligence.CacheRoot(s.cfg.StateDir)
}

// lspLauncher is the way a server runs, whatever the settings say about
// the language. The routes that start a server ask lspProfileLauncher,
// which answers nil for a language that is off; reading a file back out of
// a server's own source directories asks this one, because a tab opened
// before the switch is still on the screen and the paths it may read are
// the same either way.
func (s *Server) lspLauncher() editorintelligence.Launcher {
	return editorintelligence.DockerLauncher(s.lspCacheRoot(), s.lspDockerHost)
}

// lspProfileOff reports whether the language's navigation is off right
// now, explicitly or as the end of the automatic chain. The automatic
// default needs both the reachable daemon and the Docker launcher's own
// detection, the docker client; without them it is off.
func (s *Server) lspProfileOff(p *editorintelligence.Profile) bool {
	dockerOK := s.docker.State().Available && s.lspLauncher().Detect(p).Found
	return resolveLSPMode(s.settings.Get(editorLSPServerKey(p.ID)), p.Server, dockerOK)
}

// lspProfileLauncher is the resolved mode as the launcher the intelligence
// service runs the server with, nil while the language is off.
func (s *Server) lspProfileLauncher(p *editorintelligence.Profile) editorintelligence.Launcher {
	if s.lspProfileOff(p) {
		return nil
	}
	return s.lspLauncher()
}

// editorSettings are the effective values, defaults filled in. Whether the
// editor shows git at all is not among them: it shows it where there is a
// repository, and nothing where there is none. Neither is how a comparison
// looks, the choice of view and the folding of unchanged parts: those describe
// the screen in front of you, so they live per device in the editor's own
// settings and never reach the server. What is left here is what describes the
// install, and the limits are exactly that, a house rule against a slow device.
type editorSettings struct {
	// GitPollSeconds is how often the server looks for a change while at least
	// one editor is watching. Zero means it never does, and the refresh button
	// above the file tree is the only way to a fresh status.
	GitPollSeconds int
	// FilePollSeconds is how often the server looks at the paths an editor has
	// on the screen. It is deliberately its own value and not the one above:
	// the two measure different work. One git status walks the whole working
	// copy and may take a hand off the index, so it has to be rare; fifty stat
	// calls on the open tabs and the unfolded folders cost nothing, so they may
	// be frequent. Hanging both on one number would force one of them into the
	// wrong frequency. Zero turns the watch off.
	FilePollSeconds int
	// The rest is read by the diff.
	DiffMaxLines int
	DiffMaxKiB   int
	// Exclusions are the folders the quick open palette and the content search
	// stay out of. Emptying the list is a real choice, so it is kept apart from
	// never having chosen: an install that never opens the form gets the
	// defaults, one that saved an empty list searches everything.
	Exclusions filesystem.Exclusions
}

// editorSettings reads the effective editor settings.
func (s *Server) editorSettings() editorSettings {
	return editorSettings{
		GitPollSeconds:  s.settingInt(editorGitPollSecondsKey, 2, 0, 60),
		FilePollSeconds: s.settingInt(editorFilePollSecondsKey, 1, 0, 60),
		DiffMaxLines:    s.settingInt(editorDiffMaxLinesKey, 50000, 0, 500000),
		DiffMaxKiB:      s.settingInt(editorDiffMaxKiBKey, 4096, 0, 16384),
		Exclusions:      s.exclusions(),
	}
}

// exclusions reads the configured folder exclusions, falling back to the
// defaults only while nothing has ever been saved.
func (s *Server) exclusions() filesystem.Exclusions {
	raw, ok := s.settings.Lookup(editorExclusionsKey)
	if !ok {
		return filesystem.DefaultExclusionSet()
	}
	return filesystem.ParseExclusions(raw)
}

// settingInt reads a stored number, clamped into the range the setting accepts.
func (s *Server) settingInt(key string, fallback, min, max int) int {
	value, err := strconv.Atoi(s.settings.Get(key))
	if err != nil {
		return fallback
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// storeInt writes a number, clamped like settingInt reads it. A value that is
// not a number keeps the current setting.
func (s *Server) storeInt(key, value string, min, max int) {
	n, err := strconv.Atoi(value)
	if err != nil {
		return
	}
	if n < min {
		n = min
	}
	if n > max {
		n = max
	}
	s.settings.Set(key, strconv.Itoa(n))
}

// storeExclusions writes the folder exclusions in their canonical form, so the
// list that comes back to the form is the list that applies.
func (s *Server) storeExclusions(raw string) {
	s.settings.Set(editorExclusionsKey, filesystem.ParseExclusions(raw).String())
}
