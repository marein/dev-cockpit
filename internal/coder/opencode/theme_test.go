package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The session environment is what points opencode at the pin and tui
// configs, and the paths travel only when the files landed on the disk: a
// pin selecting a theme that is not in place crashes the TUI at start.
func TestEnvCarriesThePinConfig(t *testing.T) {
	env := runtime{ensureConfig: func() (string, string, error) {
		return "/config/opencode/dev-cockpit-config.json", "/config/opencode/dev-cockpit-tui.json", nil
	}}.Env()
	if env[themeEnv] != "/config/opencode/dev-cockpit-config.json" {
		t.Errorf("session environment misses the pin config: %v", env)
	}
	if env[tuiEnv] != "/config/opencode/dev-cockpit-tui.json" {
		t.Errorf("session environment misses the tui config: %v", env)
	}
	refused := runtime{
		notifyInbox:  "/inbox",
		ensureConfig: func() (string, string, error) { return "", "", os.ErrPermission },
	}.Env()
	if _, ok := refused[themeEnv]; ok {
		t.Errorf("a refused write must withhold the pin path: %v", refused)
	}
	if _, ok := refused[tuiEnv]; ok {
		t.Errorf("a refused write must withhold the tui path: %v", refused)
	}
	if refused[notifyEnv] != "/inbox" {
		t.Errorf("the notify inbox has to survive a refused write: %v", refused)
	}
}

// The files land under the config directory: the theme where opencode scans
// for themes, the pin and the tui config at the top where no loader picks
// them up by name.
func TestEnsureSessionConfigWritesTheFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	pin, tui, err := ensureSessionConfig()
	if err != nil {
		t.Fatalf("fresh write failed: %v", err)
	}
	if pin != filepath.Join(configDir(), pinConfigFile) {
		t.Errorf("pin path off: %s", pin)
	}
	if tui != filepath.Join(configDir(), tuiConfigFile) {
		t.Errorf("tui path off: %s", tui)
	}
	theme, err := os.ReadFile(filepath.Join(configDir(), "themes", themeName+".json"))
	if err != nil {
		t.Fatalf("theme file missing: %v", err)
	}
	if string(theme) != terminalTheme {
		t.Fatal("theme file differs from the source")
	}
	config, err := os.ReadFile(pin)
	if err != nil {
		t.Fatalf("pin config missing: %v", err)
	}
	if string(config) != pinConfig {
		t.Fatal("pin config differs from the source")
	}
	behavior, err := os.ReadFile(tui)
	if err != nil {
		t.Fatalf("tui config missing: %v", err)
	}
	if string(behavior) != tuiConfig {
		t.Fatal("tui config differs from the source")
	}
}

// Whatever holds the theme's path is rewritten: the file carries the
// cockpit's own name, so it is the cockpit's, and a stale copy would render
// with an outdated palette or, broken, crash the TUI at start.
func TestEnsureTerminalThemeRewritesWhateverHoldsThePath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := filepath.Join(configDir(), "themes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, themeName+".json"), []byte(`{"theme": {"background": "#123456"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pin, _, err := ensureSessionConfig()
	if err != nil {
		t.Fatalf("rewrite failed: %v", err)
	}
	content, _ := os.ReadFile(filepath.Join(dir, themeName+".json"))
	if string(content) != terminalTheme {
		t.Fatal("the theme file has to be brought current")
	}
	if _, err := os.Stat(pin); err != nil {
		t.Fatalf("the pin config has to stand beside the theme: %v", err)
	}
}

// The two constants have to stay parseable and consistent with each other,
// an unresolved reference or broken JSON crashes opencode's TUI at start
// (verified on 1.18.23). The base background stays "none" on purpose: the
// default background is what the terminal fills from the cockpit's palette,
// which is the whole mechanism; the panel and element surfaces stand out
// from it deliberately, so they must not be "none".
func TestThemeConstantsHoldTheContract(t *testing.T) {
	var theme struct {
		Theme map[string]json.RawMessage `json:"theme"`
	}
	if err := json.Unmarshal([]byte(terminalTheme), &theme); err != nil {
		t.Fatalf("theme is no JSON: %v", err)
	}
	if string(theme.Theme["background"]) != `"none"` {
		t.Errorf("background has to stay \"none\", got %s", theme.Theme["background"])
	}
	for _, key := range []string{"backgroundPanel", "backgroundElement"} {
		if string(theme.Theme[key]) == `"none"` {
			t.Errorf("%s is a raised surface and must not be \"none\"", key)
		}
	}
	var pin struct {
		Theme string `json:"theme"`
	}
	if err := json.Unmarshal([]byte(pinConfig), &pin); err != nil {
		t.Fatalf("pin config is no JSON: %v", err)
	}
	if pin.Theme != themeName {
		t.Errorf("pin selects %q, want %q", pin.Theme, themeName)
	}
	// The scroll speed lives in the tui config, never in the pin: the main
	// config has no tui section, a key there is silently ignored (verified
	// on 1.18.23 by measuring the scroll).
	var tui struct {
		ScrollSpeed int `json:"scroll_speed"`
	}
	if err := json.Unmarshal([]byte(tuiConfig), &tui); err != nil {
		t.Fatalf("tui config is no JSON: %v", err)
	}
	if tui.ScrollSpeed != 1 {
		t.Errorf("scroll speed = %d, want one line per wheel event", tui.ScrollSpeed)
	}
}
