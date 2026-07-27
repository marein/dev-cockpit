package copilot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/local/dev-cockpit/internal/filesystem"
)

// trustedFoldersKey is where the copilot CLI keeps the folders it may work in,
// the counterpart to claude's per project trust flag: one list in its own
// config, written when the CLI's trust prompt is answered.
const trustedFoldersKey = "trustedFolders"

// TrustWorkdir adds dir to the CLI's trusted folders, so a session that starts
// there comes up on its task instead of on the trust prompt. Already listed
// folders are left alone, and the file is otherwise kept as it is: it is
// copilot's own, machine written, and carries the login next to the list.
func (runtime) TrustWorkdir(dir string) error {
	folder, err := trustKey(dir)
	if err != nil {
		return err
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	header, config, mode, err := readConfig(path)
	if err != nil {
		return err
	}
	folders, _ := config[trustedFoldersKey].([]any)
	for _, entry := range folders {
		if name, ok := entry.(string); ok && name == folder {
			return nil
		}
	}
	config[trustedFoldersKey] = append(folders, folder)
	return writeConfig(path, header, config, mode)
}

// trustKey is the path the CLI compares against, absolute and with symlinks
// resolved, because what it compares is its own working directory.
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

func configPath() (string, error) {
	home, err := filesystem.HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".copilot", "config.json"), nil
}

// readConfig loads the config and hands back the comment lines the CLI writes
// above it ("This file is managed automatically"), which are not JSON and are
// put back verbatim. Numbers keep their exact notation, unknown keys are kept,
// and a file that does not parse is an error instead of something to replace.
func readConfig(path string) (string, map[string]any, os.FileMode, error) {
	mode := os.FileMode(0o600)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", map[string]any{}, mode, nil
	}
	if err != nil {
		return "", nil, 0, err
	}
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	header, body := splitHeader(data)
	config := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil {
		return "", nil, 0, fmt.Errorf("parse %s: %w", path, err)
	}
	return header, config, mode, nil
}

// splitHeader takes the leading comment and blank lines off, up to the line
// the JSON starts on.
func splitHeader(data []byte) (string, []byte) {
	var header strings.Builder
	rest := data
	for {
		line, tail, found := bytes.Cut(rest, []byte("\n"))
		trimmed := strings.TrimSpace(string(line))
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			return header.String(), rest
		}
		header.Write(line)
		header.WriteString("\n")
		if !found {
			return header.String(), nil
		}
		rest = tail
	}
}

func writeConfig(path, header string, config map[string]any, mode os.FileMode) error {
	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".dc.tmp"
	if err := os.WriteFile(tmp, append([]byte(header), append(out, '\n')...), mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
