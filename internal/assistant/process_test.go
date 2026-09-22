package assistant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The provider failure the user can act on is the one worth naming: a CLI that
// was never logged in on this machine. Everything else stays generic.
func TestLooksLikeLogin(t *testing.T) {
	yes := []string{
		"Invalid API key · Please run /login",
		"You are not logged in. Run copilot and sign in.",
		"Error: authentication required",
		"HTTP 401 Unauthorized",
	}
	for _, line := range yes {
		if !LooksLikeLogin(line) {
			t.Errorf("expected a login hint for %q", line)
		}
	}
	no := []string{
		"panic: runtime error: index out of range",
		"error: max tokens exceeded",
		"tool use failed: permission denied for /etc/shadow",
		"",
	}
	for _, line := range no {
		if LooksLikeLogin(line) {
			t.Errorf("unexpected login hint for %q", line)
		}
	}
}

// startedEnv runs c through start in workdir and answers the environment its
// process found. The program is env itself and no shell in front of it: sh
// puts PWD right when the one it inherited does not name its directory, so a
// shell would hide exactly what this asks about.
func startedEnv(t *testing.T, c Command, workdir string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	p, err := start(c, workdir, out, filepath.Join(dir, "err"), filepath.Join(dir, "lock"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	p.Wait()
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the process wrote no environment: %v", err)
	}
	seen := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		key, value, _ := strings.Cut(line, "=")
		seen[key] = value
	}
	return seen
}

// A turn's process sees its workdir as PWD whether or not the command asks for
// variables of its own: os/exec writes that line only when it inherits the
// environment, and opencode reads its project root off it, so a copy carrying
// the server's own PWD ran the turn against the server's directory. The
// coder's variables still land on top of the inherited environment, and a
// command without any is left to what os/exec does by itself.
func TestATurnRunsWithPWDAtItsWorkdir(t *testing.T) {
	// The server carries the PWD of the shell it was started from.
	t.Setenv("PWD", "/")
	workdir := t.TempDir()

	seen := startedEnv(t, Command{Name: "env", Env: []string{"DC_FAKE_TURN=from the command"}}, workdir)
	if seen["PWD"] != workdir {
		t.Fatalf("want PWD=%s in a process with the command's variables, got %q", workdir, seen["PWD"])
	}
	if seen["DC_FAKE_TURN"] != "from the command" {
		t.Fatalf("want the command's variable in the process, got %q", seen["DC_FAKE_TURN"])
	}
	if seen["PATH"] == "" {
		t.Fatal("want the server's own environment kept under the command's variables")
	}

	seen = startedEnv(t, Command{Name: "env"}, workdir)
	if seen["PWD"] != workdir {
		t.Fatalf("want PWD=%s in a process without variables of its own, got %q", workdir, seen["PWD"])
	}
	if value, ok := seen["DC_FAKE_TURN"]; ok {
		t.Fatalf("want no variable of the command in a process without any, got %q", value)
	}
	if seen["PATH"] == "" {
		t.Fatal("want a command without variables to inherit the environment")
	}

	// A relative workdir lands absolute, the way os/exec writes it.
	t.Chdir(filepath.Dir(workdir))
	seen = startedEnv(t, Command{Name: "env", Env: []string{"DC_FAKE_TURN=from the command"}}, filepath.Base(workdir))
	if seen["PWD"] != workdir {
		t.Fatalf("want a relative workdir as PWD=%s, got %q", workdir, seen["PWD"])
	}
}
