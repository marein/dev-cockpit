package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSplitCommandGroupsWithoutInterpreting(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"docker compose up -d", []string{"docker", "compose", "up", "-d"}},
		{"  docker   compose\tdown  ", []string{"docker", "compose", "down"}},
		{`docker compose -f "my stack.yml" up`, []string{"docker", "compose", "-f", "my stack.yml", "up"}},
		{`sh -c 'echo hi'`, []string{"sh", "-c", "echo hi"}},
		{`echo a\ b`, []string{"echo", "a b"}},
		// Nothing is expanded, so a variable and a pattern travel as text.
		{"echo $HOME *.yml", []string{"echo", "$HOME", "*.yml"}},
		// An empty argument is a real one.
		{`echo "" x`, []string{"echo", "", "x"}},
		{"", nil},
	}
	for _, c := range cases {
		got, err := SplitCommand(c.line)
		if err != nil {
			t.Fatalf("%q answered %v", c.line, err)
		}
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Fatalf("%q split into %q, wanted %q", c.line, got, c.want)
		}
	}
	for _, line := range []string{`docker "up`, `docker 'up`, `docker up\`} {
		if _, err := SplitCommand(line); err == nil {
			t.Fatalf("%q was accepted", line)
		}
	}
}

// What shellQuote writes has to come back out of the splitter unchanged,
// otherwise the deletion's own down would name a different project than it
// was given.
func TestQuotingRoundTrips(t *testing.T) {
	for _, name := range []string{"plain", "with space", "it's", `back\slash`} {
		got, err := SplitCommand("docker compose -p " + shellQuote(name) + " down -v")
		if err != nil {
			t.Fatalf("%q answered %v", name, err)
		}
		if len(got) != 6 || got[3] != name {
			t.Fatalf("%q came back as %q", name, got)
		}
	}
}

func TestActionsTellsUnsetFromEmpty(t *testing.T) {
	if list := Actions("", false); len(list) != len(DefaultActions()) {
		t.Fatalf("a store without the key answered %+v", list)
	}
	if list := Actions("[]", true); len(list) != 0 {
		t.Fatalf("an emptied list answered %+v", list)
	}
	if list := Actions("", true); len(list) != 0 {
		t.Fatalf("an empty value answered %+v", list)
	}
	// Something unreadable is not an answer, so the defaults stand rather
	// than a cockpit with no buttons and no way to say why.
	if list := Actions("{oops", true); len(list) != len(DefaultActions()) {
		t.Fatalf("a damaged value answered %+v", list)
	}
	round, err := DecodeActions(EncodeActions(DefaultActions()))
	if err != nil || len(round) != len(DefaultActions()) || round[0].ID != "up" {
		t.Fatalf("the defaults did not round trip: %+v, %v", round, err)
	}
	if _, ok := ActionByID(DefaultActions(), "down-volumes"); !ok {
		t.Fatal("the destructive default is not addressable")
	}
	if action, _ := ActionByID(DefaultActions(), "down-volumes"); !action.Confirm {
		t.Fatal("the destructive default does not ask first")
	}
}

// Saving the defaults unchanged says nothing the absent key does not already
// say, so the store never has to carry them.
func TestIsDefaultRecognizesTheUntouchedList(t *testing.T) {
	if !IsDefault(DefaultActions()) {
		t.Fatal("the default list does not read as default")
	}
	// A round trip through the store must not change that answer, the settings
	// page saves what it read back out of the JSON.
	round, err := DecodeActions(EncodeActions(DefaultActions()))
	if err != nil {
		t.Fatal(err)
	}
	if !IsDefault(round) {
		t.Fatal("the default list stopped reading as default after a round trip")
	}
	// One field is enough to make it somebody's own list.
	changed := DefaultActions()
	changed[2].Timeout = "45m"
	if IsDefault(changed) {
		t.Fatal("an edited list reads as default")
	}
	if IsDefault(DefaultActions()[:3]) {
		t.Fatal("a shortened list reads as default")
	}
	if IsDefault([]Action{}) || IsDefault(nil) {
		t.Fatal("an empty list reads as default")
	}
}

func TestResolveAnswersArgvAndTimeout(t *testing.T) {
	argv, timeout, err := upAction().Resolve(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(argv, " ") != "docker compose up -d" || timeout != 10*time.Minute {
		t.Fatalf("resolve answered %q, %s", argv, timeout)
	}
	// A timeout nobody can read is not no timeout.
	if _, d, err := (Action{Command: "docker ps", Timeout: "soon"}).Resolve("", ""); err != nil || d != defaultActionTimeout {
		t.Fatalf("an unreadable timeout answered %s, %v", d, err)
	}
	if _, _, err := (Action{Command: "   "}).Resolve("", ""); err == nil {
		t.Fatal("an empty command resolved")
	}
}

// A relatively named program is searched from the stack directory upwards to
// the project root, and comes back absolute: detach resolves the program
// before it stands in the working directory.
func TestResolveFindsAProjectScriptUpwards(t *testing.T) {
	root := t.TempDir()
	stack := filepath.Join(root, "deploy", "ops")
	if err := os.MkdirAll(stack, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "up.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	action := Action{Command: "./up.sh --now", Timeout: "1m"}
	argv, _, err := action.Resolve(stack, root)
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != script || argv[1] != "--now" {
		t.Fatalf("resolve answered %q", argv)
	}
	// The nearer one wins.
	near := filepath.Join(stack, "up.sh")
	if err := os.WriteFile(near, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if argv, _, _ := action.Resolve(stack, root); argv[0] != near {
		t.Fatalf("the near script lost against the far one: %q", argv)
	}
	// Nothing to find is an error, not a run that fails in the dark.
	if _, _, err := (Action{Command: "./nope.sh"}).Resolve(stack, root); err == nil {
		t.Fatal("a program that is nowhere resolved")
	}
	// Without a root only the stack directory itself is looked at.
	if _, _, err := action.Resolve(filepath.Join(root, "deploy"), ""); err == nil {
		t.Fatal("the search left the stack directory without a root")
	}
}

// The deletion's own down names the compose project, else compose derives a
// name from the directory and clears nothing while reporting success.
func TestPurgeActionNamesTheComposeProject(t *testing.T) {
	argv, timeout, err := PurgeAction("app_dev").Resolve(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(argv, " ") != "docker compose -p app_dev down -v" {
		t.Fatalf("purge answered %q", argv)
	}
	if timeout != 5*time.Minute {
		t.Fatalf("purge timeout answered %s", timeout)
	}
	// A stack the daemon names nothing for still comes down where it stands.
	argv, _, err = PurgeAction("").Resolve(t.TempDir(), "")
	if err != nil || strings.Join(argv, " ") != "docker compose down -v" {
		t.Fatalf("a nameless purge answered %q, %v", argv, err)
	}
}
