package copilot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/local/dev-cockpit/internal/filesystem"
)

// EnsureBeepSetting turns on the CLI's global beep option, which rings BEL in
// the pane when a session needs attention (and when a turn finishes). The
// bell is the only in-band signal the CLI offers for approval dialogs, so the
// bell watcher depends on it. Settings are global per user; every other key
// in settings.json is preserved and an already enabled beep is left alone.
func EnsureBeepSetting() error {
	home, err := filesystem.HomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".copilot", "settings.json")
	settings := map[string]any{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if enabled, ok := settings["beep"].(bool); ok && enabled {
		return nil
	}
	settings["beep"] = true
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
