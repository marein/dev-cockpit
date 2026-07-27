package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/dev-cockpit/internal/coder"
)

// The manager only pre-trusts a directory when the runtime says it can.
func TestRuntimeTrustsWorkdirs(t *testing.T) {
	var r any = runtime{}
	if _, ok := r.(coder.WorkdirTruster); !ok {
		t.Fatal("the claude runtime must be able to trust a working directory")
	}
}

func TestTrustWorkdirWritesTheFlagClaudeReads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	work := filepath.Join(t.TempDir(), "fresh-project")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := (runtime{}).TrustWorkdir(work); err != nil {
		t.Fatalf("TrustWorkdir: %v", err)
	}

	config := readTestConfig(t, filepath.Join(home, ".claude.json"))
	projects, ok := config["projects"].(map[string]any)
	if !ok {
		t.Fatalf("no projects map: %v", config)
	}
	entry, ok := projects[resolved(t, work)].(map[string]any)
	if !ok {
		t.Fatalf("no entry for %s: %v", work, projects)
	}
	if entry[trustFlag] != true {
		t.Errorf("%s = %v, want true", trustFlag, entry[trustFlag])
	}
	// claude reads other fields of an entry it created itself.
	for _, key := range []string{"allowedTools", "mcpServers", "projectOnboardingSeenCount"} {
		if _, ok := entry[key]; !ok {
			t.Errorf("entry misses %s, it does not look like one claude wrote: %v", key, entry)
		}
	}
}

// The file holds the login and every project's state. A run must keep all of
// it, must not widen the permissions, and must not rewrite what it did not
// change.
func TestTrustWorkdirKeepsTheRestOfTheConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	path := filepath.Join(home, ".claude.json")
	original := `{"oauthAccount":{"emailAddress":"a@b.c"},"numStartups":41,` +
		`"projects":{"/other":{"hasTrustDialogAccepted":true,"lastCost":0.5}}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()

	if err := (runtime{}).TrustWorkdir(work); err != nil {
		t.Fatalf("TrustWorkdir: %v", err)
	}

	config := readTestConfig(t, path)
	if account, ok := config["oauthAccount"].(map[string]any); !ok || account["emailAddress"] != "a@b.c" {
		t.Errorf("the login is gone: %v", config["oauthAccount"])
	}
	if got, want := jsonNumber(t, config["numStartups"]), "41"; got != want {
		t.Errorf("numStartups = %s, want %s (numbers must round-trip verbatim)", got, want)
	}
	projects := config["projects"].(map[string]any)
	other, ok := projects["/other"].(map[string]any)
	if !ok || other["hasTrustDialogAccepted"] != true || jsonNumber(t, other["lastCost"]) != "0.5" {
		t.Errorf("another project's entry changed: %v", projects["/other"])
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(path + ".dc.tmp"); !os.IsNotExist(err) {
		t.Error("the temporary file was left behind")
	}
}

// An already trusted directory, and one under an already trusted parent, cost
// no write at all: claude walks the path upwards, so a user who trusted the
// projects root once keeps a config without an entry per project.
func TestTrustWorkdirLeavesATrustedTreeAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	path := filepath.Join(home, ".claude.json")
	root := t.TempDir()
	child := filepath.Join(root, "project")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	config := `{"projects":{"` + resolved(t, root) + `":{"hasTrustDialogAccepted":true}}}`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, marker, marker); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{root, child} {
		if err := (runtime{}).TrustWorkdir(dir); err != nil {
			t.Fatalf("TrustWorkdir(%s): %v", dir, err)
		}
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(marker) {
		t.Error("the config was rewritten for a directory claude already trusts")
	}
}

// A config that does not parse is somebody else's broken file: say so, never
// replace it with a fresh one.
func TestTrustWorkdirRefusesAnUnreadableConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (runtime{}).TrustWorkdir(t.TempDir()); err == nil {
		t.Fatal("an unparsable config must be reported")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{not json" {
		t.Errorf("the broken config was overwritten: %s", data)
	}
}

// CLAUDE_CONFIG_DIR moves the file, and a .config.json inside the claude
// directory wins over ~/.claude.json, exactly as claude resolves it.
func TestConfigPathFollowsClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got, want := mustConfigPath(t), filepath.Join(home, ".claude.json"); got != want {
		t.Errorf("config path = %s, want %s", got, want)
	}

	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(home, ".claude", ".config.json")
	if err := os.WriteFile(inside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := mustConfigPath(t); got != inside {
		t.Errorf("config path = %s, want %s", got, inside)
	}

	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if got, want := mustConfigPath(t), filepath.Join(dir, ".claude.json"); got != want {
		t.Errorf("config path = %s, want %s", got, want)
	}
}

func mustConfigPath(t *testing.T) string {
	t.Helper()
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func readTestConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil {
		t.Fatalf("the written config does not parse: %v", err)
	}
	return config
}

func jsonNumber(t *testing.T, value any) string {
	t.Helper()
	number, ok := value.(json.Number)
	if !ok {
		t.Fatalf("%v is not a number", value)
	}
	return number.String()
}

func resolved(t *testing.T, dir string) string {
	t.Helper()
	key, err := trustKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
