package copilot

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
)

// The manager only pre-trusts a directory when the runtime says it can.
func TestRuntimeTrustsWorkdirs(t *testing.T) {
	var r any = runtime{}
	if _, ok := r.(coder.WorkdirTruster); !ok {
		t.Fatal("the copilot runtime must be able to trust a working directory")
	}
}

func TestTrustWorkdirAddsTheFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	work := t.TempDir()

	if err := (runtime{}).TrustWorkdir(work); err != nil {
		t.Fatalf("TrustWorkdir: %v", err)
	}

	config, _ := readTestConfig(t, filepath.Join(home, ".copilot", "config.json"))
	folders, ok := config[trustedFoldersKey].([]any)
	if !ok || len(folders) != 1 || folders[0] != resolved(t, work) {
		t.Fatalf("trusted folders = %v, want [%s]", config[trustedFoldersKey], work)
	}
}

// The file is copilot's own: it carries the login next to the list and two
// comment lines above the JSON that are not JSON at all. A run keeps both, and
// adds nothing when the folder is already listed.
func TestTrustWorkdirKeepsTheFileAsItIs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".copilot", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	header := "// User settings belong in settings.json.\n// This file is managed automatically.\n"
	original := header + `{"lastLoggedInUser":{"login":"marein"},"appTipShown":true,` +
		`"trustedFolders":["/already/there"]}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (runtime{}).TrustWorkdir(work); err != nil {
		t.Fatalf("TrustWorkdir: %v", err)
	}

	config, gotHeader := readTestConfig(t, path)
	if gotHeader != header {
		t.Errorf("header = %q, want %q", gotHeader, header)
	}
	if user, ok := config["lastLoggedInUser"].(map[string]any); !ok || user["login"] != "marein" {
		t.Errorf("the login is gone: %v", config["lastLoggedInUser"])
	}
	if config["appTipShown"] != true {
		t.Errorf("appTipShown = %v, want true", config["appTipShown"])
	}
	folders := config[trustedFoldersKey].([]any)
	if len(folders) != 2 || folders[0] != "/already/there" || folders[1] != resolved(t, work) {
		t.Fatalf("trusted folders = %v", folders)
	}

	marker := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, marker, marker); err != nil {
		t.Fatal(err)
	}
	if err := (runtime{}).TrustWorkdir(work); err != nil {
		t.Fatalf("second TrustWorkdir: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(marker) {
		t.Error("an already trusted folder was written again")
	}
	if after.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", after.Mode().Perm())
	}
}

func TestTrustWorkdirRefusesAnUnreadableConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".copilot", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("// managed\n{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (runtime{}).TrustWorkdir(t.TempDir()); err == nil {
		t.Fatal("an unparsable config must be reported")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "// managed\n{not json" {
		t.Errorf("the broken config was overwritten: %s", data)
	}
}

func readTestConfig(t *testing.T, path string) (map[string]any, string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	header, body, _ := strings.Cut(string(data), "{")
	config := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader([]byte("{" + body)))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil {
		t.Fatalf("the written config does not parse: %v", err)
	}
	return config, header
}

func resolved(t *testing.T, dir string) string {
	t.Helper()
	key, err := trustKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
