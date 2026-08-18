package statusline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyAndClearFollowTheMode(t *testing.T) {
	dir := t.TempDir()
	// A cockpit that was never told stands on the fallback, which writes the
	// line: whether it is used is decided per session start.
	if err := Sync(dir, ""); err != nil {
		t.Fatalf("sync without a setting: %v", err)
	}
	entries, ok := Load(dir)
	if !ok || len(entries) != len(Normalize(Defaults())) {
		t.Fatalf("the default wrote %d entries (%v), want the default line", len(entries), ok)
	}
	// What the renderer remembered belongs to the line and goes with it.
	cache := filepath.Join(Dir(dir), usageCacheName)
	writeFile(t, cache, "{}")
	// A stored list that is off is a list nobody threw away, and it writes no
	// line.
	if err := Sync(dir, Encode(Config{Mode: ModeOff, Entries: Defaults()})); err != nil {
		t.Fatalf("sync with the mode off: %v", err)
	}
	for _, path := range []string{ConfigPath(dir), cache, Dir(dir)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived the way out", path)
		}
	}
	if err := Sync(dir, Encode(Config{Mode: ModeAlways, Entries: Defaults()})); err != nil {
		t.Fatalf("sync with the mode always: %v", err)
	}
	if _, ok := Load(dir); !ok {
		t.Fatal("always wrote no line")
	}
	if err := Sync(dir, "{"); err != nil {
		t.Fatalf("sync with a damaged value: %v", err)
	}
	if _, err := os.Stat(ConfigPath(dir)); !os.IsNotExist(err) {
		t.Fatal("a damaged value left a line behind")
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
