package pluginhost

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/plugin"
)

func memoryFiles(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, content := range files {
		out[name] = &fstest.MapFile{Data: []byte(content)}
	}
	return out
}

func applyMemory(serves []*Serve, dir string) {
	ApplyAssistantMemory(serves, dir, assistant.IsMemoryFile, assistant.IsMemoryTooLong)
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &out
}

// Without overwrite an existing file is kept and a missing one is written.
func TestApplyAssistantMemoryKeepsWhatIsThere(t *testing.T) {
	logs := captureLog(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "worktrees.md"), "edited by the person")
	serves := configured(t, plugin.Named[plugin.ServePlugin]{ID: "example", Plugin: serveFunc(func(s plugin.Serve) error {
		s.AddAssistantMemory(memoryFiles(map[string]string{
			"worktrees.md": "from the plugin",
			"fresh.md":     "new",
		}), false)
		return nil
	})})

	applyMemory(serves, dir)

	if got := readFile(t, filepath.Join(dir, "worktrees.md")); got != "edited by the person" {
		t.Fatalf("an existing memory file became %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "fresh.md")); got != "new" {
		t.Fatalf("fresh.md = %q", got)
	}
	if !strings.Contains(logs.String(), "plugin example: assistant memory worktrees.md kept") || !strings.Contains(logs.String(), "plugin example: assistant memory fresh.md written") {
		t.Fatalf("the log does not say what was kept and written: %s", logs)
	}
}

// A file the memory does not read, judged by its full path, is skipped and
// logged, with or without overwrite.
func TestApplyAssistantMemorySkipsWhatTheMemoryCannotRead(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		t.Run(fmt.Sprintf("overwrite %v", overwrite), func(t *testing.T) {
			logs := captureLog(t)
			dir := t.TempDir()
			serves := configured(t, plugin.Named[plugin.ServePlugin]{ID: "example", Plugin: serveFunc(func(s plugin.Serve) error {
				s.AddAssistantMemory(memoryFiles(map[string]string{
					"Notes.md":      "upper case",
					"my_notes.md":   "underscore",
					"nested/one.md": "nested",
					"notes.txt":     "plain",
					"kept.md":       "top",
				}), overwrite)
				return nil
			})})

			applyMemory(serves, dir)

			if names := dirNames(t, dir); !slices.Equal(names, []string{"kept.md"}) {
				t.Fatalf("the memory directory holds %v, want only kept.md", names)
			}
			for _, name := range []string{"Notes.md", "my_notes.md", "nested/one.md", "notes.txt"} {
				if !strings.Contains(logs.String(), "plugin example: assistant memory "+name+" skipped") {
					t.Fatalf("the skip of %s is not logged with the plugin id: %s", name, logs)
				}
			}
		})
	}
}

func TestApplyAssistantMemorySkipsWhatIsTooLong(t *testing.T) {
	logs := captureLog(t)
	dir := t.TempDir()
	serves := configured(t, plugin.Named[plugin.ServePlugin]{ID: "example", Plugin: serveFunc(func(s plugin.Serve) error {
		s.AddAssistantMemory(memoryFiles(map[string]string{
			"big.md":  "---\ntitle: Big\n---\n" + strings.Repeat("x", assistant.MaxMemoryBytes+1),
			"fits.md": "---\ntitle: Fits\n---\n" + strings.Repeat("x", assistant.MaxMemoryBytes),
		}), false)
		return nil
	})})

	applyMemory(serves, dir)

	if names := dirNames(t, dir); !slices.Equal(names, []string{"fits.md"}) {
		t.Fatalf("the memory directory holds %v, want only fits.md", names)
	}
	if !strings.Contains(logs.String(), "plugin example: assistant memory big.md skipped") {
		t.Fatalf("the skip of big.md is not logged with the plugin id: %s", logs)
	}
}

// After a kept, a written and an overwritten file only the targets are left.
func TestApplyAssistantMemoryLeavesNoTemporaryFile(t *testing.T) {
	captureLog(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "kept.md"), "mine")
	writeFile(t, filepath.Join(dir, "replaced.md"), "stale")
	serves := configured(t, plugin.Named[plugin.ServePlugin]{ID: "example", Plugin: serveFunc(func(s plugin.Serve) error {
		s.AddAssistantMemory(memoryFiles(map[string]string{"kept.md": "plugin", "written.md": "new"}), false)
		s.AddAssistantMemory(memoryFiles(map[string]string{"replaced.md": "current"}), true)
		return nil
	})})

	applyMemory(serves, dir)

	if names := dirNames(t, dir); !slices.Equal(names, []string{"kept.md", "replaced.md", "written.md"}) {
		t.Fatalf("the memory directory holds %v", names)
	}
	for name, want := range map[string]string{"kept.md": "mine", "replaced.md": "current", "written.md": "new"} {
		if got := readFile(t, filepath.Join(dir, name)); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestTempPatternIsNoMemoryFile(t *testing.T) {
	if name := strings.Replace(tempPattern, "*", "123456789", 1); assistant.IsMemoryFile(name) {
		t.Fatalf("the temporary file %s is a memory file name", name)
	}
}

func TestApplyAssistantMemoryOverwrites(t *testing.T) {
	captureLog(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "worktrees.md"), "stale")
	serves := configured(t, plugin.Named[plugin.ServePlugin]{ID: "example", Plugin: serveFunc(func(s plugin.Serve) error {
		s.AddAssistantMemory(memoryFiles(map[string]string{"worktrees.md": "current"}), true)
		return nil
	})})

	applyMemory(serves, dir)

	if got := readFile(t, filepath.Join(dir, "worktrees.md")); got != "current" {
		t.Fatalf("worktrees.md = %q, want it replaced", got)
	}
}

// The first plugin fills a target, a later one replaces it only with overwrite.
func TestApplyAssistantMemoryOrderAcrossPlugins(t *testing.T) {
	captureLog(t)
	declare := func(content string, overwrite bool) plugin.ServePlugin {
		return serveFunc(func(s plugin.Serve) error {
			s.AddAssistantMemory(memoryFiles(map[string]string{"shared.md": content, content + ".md": content}), overwrite)
			return nil
		})
	}
	kept := t.TempDir()
	applyMemory(configured(t,
		plugin.Named[plugin.ServePlugin]{ID: "first", Plugin: declare("first", false)},
		plugin.Named[plugin.ServePlugin]{ID: "second", Plugin: declare("second", false)},
	), kept)
	if got := readFile(t, filepath.Join(kept, "shared.md")); got != "first" {
		t.Fatalf("shared.md = %q, want the first declaration", got)
	}

	replaced := t.TempDir()
	applyMemory(configured(t,
		plugin.Named[plugin.ServePlugin]{ID: "first", Plugin: declare("first", false)},
		plugin.Named[plugin.ServePlugin]{ID: "second", Plugin: declare("second", true)},
	), replaced)
	if got := readFile(t, filepath.Join(replaced, "shared.md")); got != "second" {
		t.Fatalf("shared.md = %q, want the later declaration with overwrite", got)
	}
	for _, name := range []string{"first.md", "second.md"} {
		if _, err := os.Stat(filepath.Join(replaced, name)); err != nil {
			t.Fatalf("%s is missing: %v", name, err)
		}
	}
}

// A failed write is logged with the plugin id and the rest is still written.
func TestApplyAssistantMemoryGoesOnAfterAFailure(t *testing.T) {
	logs := captureLog(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "blocked.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	serves := configured(t, plugin.Named[plugin.ServePlugin]{ID: "example", Plugin: serveFunc(func(s plugin.Serve) error {
		s.AddAssistantMemory(memoryFiles(map[string]string{"blocked.md": "x", "zz.md": "after"}), true)
		return nil
	})})

	applyMemory(serves, dir)

	if got := readFile(t, filepath.Join(dir, "zz.md")); got != "after" {
		t.Fatalf("zz.md = %q, the failure stopped the rest", got)
	}
	if !strings.Contains(logs.String(), "plugin example: assistant memory blocked.md:") {
		t.Fatalf("the failure is not logged with the plugin id: %s", logs)
	}
	if names := dirNames(t, dir); !slices.Equal(names, []string{"blocked.md", "zz.md"}) {
		t.Fatalf("the memory directory holds %v, want blocked.md and zz.md", names)
	}
}

// An absent key is set, a present one, an empty value included, only with
// overwrite.
func TestApplySettings(t *testing.T) {
	logs := captureLog(t)
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	store.Set("chosen", "mine")
	store.Set("emptied", "")
	store.Set("stale", "old")
	serves := configured(t, plugin.Named[plugin.ServePlugin]{ID: "example", Plugin: serveFunc(func(s plugin.Serve) error {
		s.AddSettings(map[string]string{"chosen": "plugin", "emptied": "plugin", "missing": "plugin"}, false)
		s.AddSettings(map[string]string{"stale": "new"}, true)
		return nil
	})})

	ApplySettings(serves, store)

	for key, want := range map[string]string{"chosen": "mine", "emptied": "", "missing": "plugin", "stale": "new"} {
		if got, ok := store.Lookup(key); !ok || got != want {
			t.Errorf("%s = (%q, %v), want %q", key, got, ok, want)
		}
	}
	if !strings.Contains(logs.String(), "plugin example: settings key chosen kept") || !strings.Contains(logs.String(), "plugin example: settings key missing written") {
		t.Fatalf("the log does not say what was kept and written: %s", logs)
	}
}

func TestApplySettingsOrderAcrossPlugins(t *testing.T) {
	captureLog(t)
	declare := func(value string, overwrite bool) plugin.ServePlugin {
		return serveFunc(func(s plugin.Serve) error {
			s.AddSettings(map[string]string{"shared": value}, overwrite)
			return nil
		})
	}
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	ApplySettings(configured(t,
		plugin.Named[plugin.ServePlugin]{ID: "first", Plugin: declare("first", false)},
		plugin.Named[plugin.ServePlugin]{ID: "second", Plugin: declare("second", false)},
	), store)
	if got := store.Get("shared"); got != "first" {
		t.Fatalf("shared = %q, want the first declaration", got)
	}
	ApplySettings(configured(t,
		plugin.Named[plugin.ServePlugin]{ID: "first", Plugin: declare("first", false)},
		plugin.Named[plugin.ServePlugin]{ID: "second", Plugin: declare("second", true)},
	), store)
	if got := store.Get("shared"); got != "second" {
		t.Fatalf("shared = %q, want the later declaration with overwrite", got)
	}
}

func TestAddSettingsCopies(t *testing.T) {
	captureLog(t)
	values := map[string]string{"key": "added"}
	serves := configured(t, plugin.Named[plugin.ServePlugin]{ID: "example", Plugin: serveFunc(func(s plugin.Serve) error {
		s.AddSettings(values, false)
		return nil
	})})
	values["key"] = "changed later"
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	ApplySettings(serves, store)
	if got := store.Get("key"); got != "added" {
		t.Fatalf("key = %q, want the value at the time of the call", got)
	}
}

func TestSealedServeRefusesDefaults(t *testing.T) {
	adds := map[string]func(s plugin.Serve){
		"AddAssistantMemory": func(s plugin.Serve) { s.AddAssistantMemory(fstest.MapFS{}, false) },
		"AddSettings":        func(s plugin.Serve) { s.AddSettings(map[string]string{"k": "v"}, false) },
	}
	for name, add := range adds {
		t.Run(name, func(t *testing.T) {
			p := &recordingPlugin{}
			configured(t, plugin.Named[plugin.ServePlugin]{ID: "kept", Plugin: p})
			defer func() {
				msg, ok := recover().(string)
				if !ok || !strings.Contains(msg, "kept") {
					t.Fatalf("a late %s panicked with %v, want the plugin id", name, msg)
				}
			}()
			add(p.got)
			t.Fatalf("a sealed Serve accepted %s", name)
		})
	}
}
