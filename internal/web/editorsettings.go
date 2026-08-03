package web

import "strconv"

// The editor's cross-device settings, edited on the tabs of /settings/editor and
// stored as flat values in the shared settings store. A fresh install has no
// entries at all, so the defaults below are what actually applies, and a value
// that was stored once and later became invalid falls back to the same default.
// The keys are hyphenated like every other one in that file; the form fields
// they are written from keep the underscores every form in the app uses.
const (
	editorGitPollSecondsKey = "editor-git-poll-seconds"
	editorDiffMaxLinesKey   = "editor-diff-max-lines"
	editorDiffMaxKiBKey     = "editor-diff-max-kib"
)

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
	// The rest is read by the diff.
	DiffMaxLines int
	DiffMaxKiB   int
}

// editorSettings reads the effective editor settings.
func (s *Server) editorSettings() editorSettings {
	return editorSettings{
		GitPollSeconds: s.settingInt(editorGitPollSecondsKey, 2, 0, 60),
		DiffMaxLines:   s.settingInt(editorDiffMaxLinesKey, 5000, 0, 200000),
		DiffMaxKiB:     s.settingInt(editorDiffMaxKiBKey, 512, 0, 2048),
	}
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
