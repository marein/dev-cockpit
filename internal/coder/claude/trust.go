package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/local/dev-cockpit/internal/filesystem"
)

// trustFlag is the key claude keeps its answer to the workspace trust dialog
// under, one entry per directory in its config. claude names it in the clear
// when it refuses an untrusted workspace ("or set projects[<dir>]
// .hasTrustDialogAccepted: true in <config>"), and there is no flag, no
// environment variable and no --settings key that does the same, so this is
// the state the dialog writes.
const trustFlag = "hasTrustDialogAccepted"

// newProjectEntry is the record claude creates for a directory it did not know
// yet, with the trust answered. The other fields carry claude's own defaults,
// so an entry this writes looks like one claude wrote.
func newProjectEntry() map[string]any {
	return map[string]any{
		"allowedTools":                            []any{},
		"mcpContextUris":                          []any{},
		"mcpServers":                              map[string]any{},
		"enabledMcpjsonServers":                   []any{},
		"disabledMcpjsonServers":                  []any{},
		trustFlag:                                 true,
		"projectOnboardingSeenCount":              0,
		"hasClaudeMdExternalIncludesApproved":     false,
		"hasClaudeMdExternalIncludesWarningShown": false,
	}
}

// TrustWorkdir records dir as trusted, so a session that starts there comes up
// on its task instead of on the trust dialog. It writes nothing when claude
// already trusts the directory, which includes trust inherited from a parent:
// claude walks the path upwards looking for the flag, and so does this, so a
// user who trusted the projects root once keeps a config without an entry per
// project.
func (runtime) TrustWorkdir(dir string) error {
	key, err := trustKey(dir)
	if err != nil {
		return err
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	config, mode, err := readConfig(path)
	if err != nil {
		return err
	}
	projects, _ := config["projects"].(map[string]any)
	if trusted(projects, key) {
		return nil
	}
	if projects == nil {
		projects = map[string]any{}
	}
	entry, ok := projects[key].(map[string]any)
	if !ok {
		entry = newProjectEntry()
	}
	entry[trustFlag] = true
	projects[key] = entry
	config["projects"] = projects
	return writeConfig(path, config, mode)
}

// trustKey is the path claude looks the directory up under: absolute, cleaned,
// and with symlinks resolved, because the key claude derives comes from the
// process working directory, which the operating system reports resolved.
func trustKey(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("no directory")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// trusted reports whether claude already trusts the key or one of its parents.
func trusted(projects map[string]any, key string) bool {
	for dir := key; ; {
		if entry, ok := projects[dir].(map[string]any); ok {
			if flag, ok := entry[trustFlag].(bool); ok && flag {
				return true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// configPath resolves the file claude keeps that state in, the way claude
// resolves it: CLAUDE_CONFIG_DIR replaces the home directory, and a
// `.config.json` inside the claude directory wins over `~/.claude.json` when
// it exists.
func configPath() (string, error) {
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	dir := configDir
	if dir == "" {
		home, err := filesystem.HomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".claude")
		configDir = home
	}
	if _, err := os.Stat(filepath.Join(dir, ".config.json")); err == nil {
		return filepath.Join(dir, ".config.json"), nil
	}
	return filepath.Join(configDir, ".claude.json"), nil
}

// readConfig loads the config, keeping every key and every number exactly as
// it was written: this is claude's file, it holds the login and the state of
// every project, and it is rewritten here in full. A file that does not parse
// is an error, never something to replace with a fresh one.
func readConfig(path string) (map[string]any, os.FileMode, error) {
	mode := os.FileMode(0o600)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, mode, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	config := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil {
		return nil, 0, fmt.Errorf("parse %s: %w", path, err)
	}
	return config, mode, nil
}

// writeConfig replaces the config atomically and under its own permissions,
// which are the login's: a temporary file next to it, then a rename.
func writeConfig(path string, config map[string]any, mode os.FileMode) error {
	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".dc.tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
