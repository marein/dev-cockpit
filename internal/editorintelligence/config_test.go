package editorintelligence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigStoreDefaultsAndRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store := NewConfigStore(dir)

	settings := store.Load()
	if settings.Mode != ModeOff || !settings.AutoAI || settings.DebounceMs != 300 {
		t.Fatalf("defaults: %+v", settings)
	}
	if !settings.ProfileEnabled("go") {
		t.Fatal("profiles must default to enabled")
	}
	if settings.AIConfigured() {
		t.Fatal("AI must default to off")
	}

	settings.Mode = ModeLSPAI
	settings.DisabledProfiles = []string{"php"}
	settings.Ollama = OllamaSettings{Enabled: true, Model: "qwen2.5-coder"}
	store.Save(settings)

	loaded := store.Load()
	if loaded.Mode != ModeLSPAI || loaded.ProfileEnabled("php") || !loaded.ProfileEnabled("go") {
		t.Fatalf("roundtrip: %+v", loaded)
	}
	if !loaded.AIConfigured() {
		t.Fatal("AI must be configured after save")
	}

	info, err := os.Stat(filepath.Join(dir, "editor-settings.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("settings mode %o", info.Mode().Perm())
	}
}

func TestConfigStoreNormalizesBadValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "editor-settings.json")
	if err := os.WriteFile(path, []byte(`{"mode":"bogus","debounceMs":42}`), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := NewConfigStore(dir).Load()
	if settings.Mode != ModeOff || settings.DebounceMs != 300 {
		t.Fatalf("normalize: %+v", settings)
	}
}

func TestConfigStoreQuarantinesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "editor-settings.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := NewConfigStore(dir).Load()
	if settings.Mode != ModeOff {
		t.Fatalf("corrupt file must yield defaults: %+v", settings)
	}
	if _, err := os.Stat(path + ".broken"); err != nil {
		t.Fatalf("quarantine missing: %v", err)
	}
}

func TestSecretStoreMode(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretStore(dir)
	if store.HasToken() {
		t.Fatal("fresh store must have no token")
	}
	store.SetToken("super-secret")
	if !store.HasToken() || store.Token() != "super-secret" {
		t.Fatal("token roundtrip failed")
	}
	info, err := os.Stat(filepath.Join(dir, "editor-secrets.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("secret file mode %o", info.Mode().Perm())
	}
}

func TestRegistryProfiles(t *testing.T) {
	byExt := map[string]string{
		"main.go":     "go",
		"index.php":   "php",
		"app.py":      "python",
		"page.html":   "html",
		"page.htm":    "html",
		"style.css":   "css",
		"style.scss":  "scss",
		"unknown.rs":  "",
		"noextension": "",
	}
	for path, wantLang := range byExt {
		profile, lang, ok := ProfileForPath(path)
		if wantLang == "" {
			if ok {
				t.Fatalf("%q must have no profile", path)
			}
			continue
		}
		if !ok || lang != wantLang {
			t.Fatalf("%q: lang %q ok %v, want %q", path, lang, ok, wantLang)
		}
		if profile == nil || len(profile.Command) == 0 {
			t.Fatalf("%q: missing command", path)
		}
	}
	if len(Profiles()) != 5 {
		t.Fatalf("profile count %d", len(Profiles()))
	}
	for _, p := range Profiles() {
		if p.ID == "" || p.Label == "" || len(p.Command) == 0 {
			t.Fatalf("incomplete profile %+v", p)
		}
	}
	exts := OwnedExtensions()
	if strings.Join(exts, ",") != "css,go,htm,html,php,py,scss" {
		t.Fatalf("owned extensions %v", exts)
	}
}
