package statusline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncWritesTheLineInEveryMode(t *testing.T) {
	dir := t.TempDir()
	// A cockpit that was never told stands on the fallback and writes the
	// default line.
	if err := Sync(dir, ""); err != nil {
		t.Fatalf("sync without a setting: %v", err)
	}
	entries, ok := Load(dir)
	if !ok || len(entries) != len(Normalize(Defaults())) {
		t.Fatalf("the default wrote %d entries (%v), want the default line", len(entries), ok)
	}
	cache := filepath.Join(Dir(dir), usageCacheName)
	writeFile(t, cache, "{}")
	// Off only decides what a session gets when it starts. A session that
	// started with the line keeps drawing it, so the line is written and
	// nothing the renderer remembered goes.
	own := []Entry{{Kind: KindValue, Value: FreeTextValue, Text: "off"}}
	if err := Sync(dir, Encode(Config{Mode: ModeOff, Entries: own})); err != nil {
		t.Fatalf("sync with the mode off: %v", err)
	}
	if entries, ok := Load(dir); !ok || len(entries) != 1 || entries[0].Text != "off" {
		t.Fatalf("off left the line %v (%v), want its list written", entries, ok)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("off removed what the renderer remembered: %v", err)
	}
	if err := Sync(dir, Encode(Config{Mode: ModeAlways, Entries: Defaults()})); err != nil {
		t.Fatalf("sync with the mode always: %v", err)
	}
	if entries, ok := Load(dir); !ok || len(entries) != len(Normalize(Defaults())) {
		t.Fatal("always did not write its line")
	}
	// A value that cannot be read says so and leaves the line as it was.
	if err := Sync(dir, "{"); err == nil {
		t.Fatal("a damaged value synced without a word")
	}
	if entries, ok := Load(dir); !ok || len(entries) != len(Normalize(Defaults())) {
		t.Fatal("a damaged value touched the line")
	}
}

// The renderer only reads: a line it cannot parse stays where it is, for the
// serve process to replace, and draws nothing.
func TestLoadLeavesADamagedLineAlone(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, ConfigPath(dir), "{")
	if _, ok := Load(dir); ok {
		t.Fatal("a damaged line loaded")
	}
	if _, err := os.Stat(ConfigPath(dir)); err != nil {
		t.Fatalf("the damaged line was moved: %v", err)
	}
}

// claude hands the command to a shell, so a binary or a state directory with
// a space or a quote in its path must still arrive as one argument each.
func TestCommandSurvivesTheShell(t *testing.T) {
	base := filepath.Join(t.TempDir(), "it's a dir")
	binary := filepath.Join(base, "dev cockpit")
	writeFile(t, binary, "#!/bin/sh\nfor arg in \"$@\"; do printf '[%s]' \"$arg\"; done\n")
	if err := os.Chmod(binary, 0o700); err != nil {
		t.Fatalf("make the binary executable: %v", err)
	}
	state := filepath.Join(base, "state dir")
	out, err := exec.Command("sh", "-c", Command(binary, state)).CombinedOutput()
	if err != nil {
		t.Fatalf("the shell did not run %q: %v, %q", Command(binary, state), err, out)
	}
	if want := "[claude][status-line][--state-dir][" + state + "]"; strings.TrimSpace(string(out)) != want {
		t.Fatalf("the arguments arrived as %q, want %q", out, want)
	}
}
