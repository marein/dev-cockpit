package docker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestForDirMatchesWorkingDir(t *testing.T) {
	state := State{Containers: []Container{
		{Name: "a", WorkingDir: "/home/me/projects/app"},
		{Name: "b", WorkingDir: "/home/me/projects/app/ops"},
		{Name: "c", WorkingDir: "/home/me/projects/app-two"},
		{Name: "d", WorkingDir: ""},
	}}
	got := state.ForDir("/home/me/projects/app")
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("ForDir answered %+v", got)
	}
	if len(state.ForDir("/home/me/projects/other")) != 0 {
		t.Fatalf("unrelated dir matched")
	}
	if len(state.ForDir("")) != 0 {
		t.Fatalf("empty dir matched")
	}
}

func TestForDirOrdersByStatusAndKeepsTheRestStable(t *testing.T) {
	dir := "/home/me/projects/app"
	state := State{Containers: []Container{
		{Name: "up-a", State: "running", WorkingDir: dir},
		{Name: "gone", State: "exited", WorkingDir: dir},
		{Name: "sick", State: "running", Health: "unhealthy", WorkingDir: dir},
		{Name: "up-b", State: "running", WorkingDir: dir},
		{Name: "dead", State: "dead", WorkingDir: dir},
	}}
	want := []string{"sick", "dead", "up-a", "up-b", "gone"}
	got := state.ForDir(dir)
	if len(got) != len(want) {
		t.Fatalf("ForDir answered %d containers, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("position %d is %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestForDirFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	state := State{Containers: []Container{{Name: "a", WorkingDir: resolved}}}
	if got := state.ForDir(link); len(got) != 1 {
		t.Fatalf("symlinked project dir did not match, got %+v", got)
	}
}

func TestSortContainersIsStableByProjectServiceName(t *testing.T) {
	list := []Container{
		{Project: "b", Service: "web", Name: "b-web-1"},
		{Project: "a", Service: "web", Name: "a-web-2"},
		{Project: "a", Service: "db", Name: "a-db-1"},
		{Project: "a", Service: "web", Name: "a-web-1"},
	}
	sortContainers(list)
	want := []string{"a-db-1", "a-web-1", "a-web-2", "b-web-1"}
	for i, name := range want {
		if list[i].Name != name {
			t.Fatalf("position %d is %q, want %q", i, list[i].Name, name)
		}
	}
}

func TestRunning(t *testing.T) {
	for state, want := range map[string]bool{
		"running": true, "restarting": true, "paused": true,
		"exited": false, "created": false, "dead": false,
	} {
		if got := (Container{State: state}).Running(); got != want {
			t.Fatalf("Running for %q answered %v", state, got)
		}
	}
}

func TestRelevantEvent(t *testing.T) {
	for line, want := range map[string]bool{
		`{"Type":"container","Action":"start"}`:                   true,
		`{"Type":"container","Action":"die"}`:                     true,
		`{"Type":"container","Action":"health_status: healthy"}`:  true,
		`{"Type":"container","Action":"exec_create: sh -c true"}`: false,
		`{"Type":"container","Action":"exec_die"}`:                false,
		`{"Type":"image","Action":"pull"}`:                        false,
		`not json`:                                                false,
	} {
		if got := relevantEvent([]byte(line)); got != want {
			t.Fatalf("relevantEvent(%s) answered %v", line, got)
		}
	}
}
