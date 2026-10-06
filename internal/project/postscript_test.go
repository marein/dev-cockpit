package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSavePostScriptNormalizesRefusesAndDeletes(t *testing.T) {
	path := filepath.Join(t.TempDir(), PostScriptFile)
	if err := SavePostScript(path, "#!/bin/sh\r\necho a\r\necho b"); err != nil {
		t.Fatal(err)
	}
	if got := ReadPostScript(path); got != "#!/bin/sh\necho a\necho b\n" {
		t.Fatalf("stored %q", got)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
	if err := SavePostScript(path, "echo no interpreter\n"); !errors.Is(err, ErrNoInterpreter) {
		t.Fatalf("a script without #! answered %v", err)
	}
	if got := ReadPostScript(path); !strings.HasPrefix(got, "#!/bin/sh") {
		t.Fatalf("a refused script replaced the stored one: %q", got)
	}
	if err := SavePostScript(path, " \r\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("an empty save left the file: %v", err)
	}
	if err := SavePostScript(path, ""); err != nil {
		t.Fatalf("deleting a missing script failed: %v", err)
	}
}

func TestRunPostScriptRunsInTheWorktreeWithItsEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), PostScriptFile)
	if run := RunPostScript(context.Background(), path, t.TempDir(), PostScriptVars{}); run != nil {
		t.Fatalf("a missing script ran: %+v", run)
	}
	writeTestFile(t, path, "#!/bin/sh\npwd\necho \"branch $DC_BRANCH\"\necho oops >&2\n")
	dir := t.TempDir()
	run := RunPostScript(context.Background(), path, dir, PostScriptVars{Branch: "feature"})
	if run == nil || run.Err != "" {
		t.Fatalf("the script failed: %+v", run)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if run.Output != real+"\nbranch feature\noops\n" {
		t.Fatalf("output %q", run.Output)
	}
	if info, _ := os.Stat(path); info.Mode().Perm()&0o100 == 0 {
		t.Fatal("a script without the executable bit did not get it back")
	}
}

func TestRunPostScriptReportsAFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), PostScriptFile)
	if err := SavePostScript(path, "#!/bin/sh\necho broken\nexit 3\n"); err != nil {
		t.Fatal(err)
	}
	if run := RunPostScript(context.Background(), path, t.TempDir(), PostScriptVars{}); run.Err != "exit status 3" || run.Output != "broken\n" {
		t.Fatalf("a failing script answered %+v", run)
	}
	if err := SavePostScript(path, "#!/no/such/interpreter\n"); err != nil {
		t.Fatal(err)
	}
	if run := RunPostScript(context.Background(), path, t.TempDir(), PostScriptVars{}); run.Err == "" {
		t.Fatal("a missing interpreter passed")
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	buf := &tailBuffer{max: 4}
	buf.Write([]byte("abc"))
	buf.Write([]byte("defg"))
	if got := buf.String(); got != "…defg" {
		t.Fatalf("kept %q", got)
	}
}

func TestPostScriptSummaryNamesTheEnvironment(t *testing.T) {
	want := "Runs in the new worktree, max 15s, env: DC_SOURCE_PROJECT DC_SOURCE_DIR DC_WORKTREE_DIR DC_WORKTREE_PROJECT DC_BRANCH."
	if got := PostScriptSummary(); got != want {
		t.Fatalf("summary %q", got)
	}
	if slices.Contains(PostScriptEnv, "DC_PROJECT") {
		t.Fatal("the released DC_PROJECT is documented")
	}
}

func TestRunPostScriptKeepsTheReleasedProjectName(t *testing.T) {
	path := filepath.Join(t.TempDir(), PostScriptFile)
	writeTestFile(t, path, "#!/bin/sh\necho \"$DC_SOURCE_PROJECT $DC_PROJECT\"\n")
	run := RunPostScript(context.Background(), path, t.TempDir(), PostScriptVars{Project: "app", WorktreeProject: "app-feature"})
	if run == nil || run.Err != "" || run.Output != "app app\n" {
		t.Fatalf("the script got %+v", run)
	}
}
