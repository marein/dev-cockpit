package statusline

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/statefile"
)

const configName = "config.json"

// rendered is what the serve process hands the renderer: the entries of the
// line, normalized once at the save.
type rendered struct {
	Entries []Entry `json:"entries"`
}

// Dir is the status line's own folder in the state directory, below claude's,
// so nothing of another coder or feature can ever share a name with it. It
// holds the line the serve process writes and what the renderer remembers
// between two redraws, the usage answer and the last cost reading per coder:
// derived readings that only the renderer reads, and no state.
func Dir(stateDir string) string { return filepath.Join(stateDir, "claude", "status-line") }

// ConfigPath is the line the renderer draws, written in every mode. The mode
// only decides whether a session gets a statusLine when it starts; one that
// got it reads this file at every redraw until it ends, whatever the mode is
// by then.
func ConfigPath(stateDir string) string { return filepath.Join(Dir(stateDir), configName) }

// Command is the statusLine command claude runs: this binary's own renderer,
// pointed at the state directory whose line it draws. claude hands the command
// to a shell, so both paths are quoted.
func Command(binary, stateDir string) string {
	return clirun.ShellQuote(binary) + " claude status-line --state-dir " + clirun.ShellQuote(stateDir)
}

// Apply writes the line for these entries, atomically, so a session drawing
// while a save runs never reads half of it.
func Apply(stateDir string, entries []Entry) error {
	return statefile.Write(ConfigPath(stateDir), 0o600, rendered{Entries: Normalize(entries)})
}

// Sync writes the line again from what the settings store holds, nothing
// stored being the default configuration, in every mode. A value that cannot
// be read leaves the line that is there alone, because a line drawn out of a
// guess is worse than the last one that was saved.
func Sync(stateDir, raw string) error {
	config, err := Decode(raw)
	if err != nil {
		return err
	}
	return Apply(stateDir, config.Entries)
}

// Load reads the line for the renderer. It only reads: the file belongs to the
// serve process, so one it cannot parse is left exactly as it is and the line
// stays empty.
func Load(stateDir string) ([]Entry, bool) {
	data, err := os.ReadFile(ConfigPath(stateDir))
	if err != nil {
		return nil, false
	}
	var line rendered
	if json.Unmarshal(data, &line) != nil {
		return nil, false
	}
	return line.Entries, true
}
