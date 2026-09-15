package assistant

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// seedLegacy writes the layout a release before the assistants could live side
// by side left behind: one index, one transcript file per conversation, one
// jobs file for the single assistant there was, and one shared workspace with
// the memory, the generated instruction files, the files every conversation
// wrote and the uploads of each conversation in it.
func seedLegacy(t *testing.T, root string, index []legacySummary, transcripts map[string]Instance, jobs []Job) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, legacyTranscripts), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	statefile.Save(filepath.Join(root, "assistant.json"), 0o600, index)
	for id, c := range transcripts {
		statefile.Save(filepath.Join(root, legacyTranscripts, id+".json"), 0o600, c)
		mustWrite(t, filepath.Join(root, legacyWorkspace, uploadDirName, id, "pic.png"), "PNG of "+id)
	}
	statefile.Save(filepath.Join(root, legacyJobs), 0o600, jobs)
	mustWrite(t, filepath.Join(root, legacyWorkspace, legacyMemory, "likes-go.md"), "---\ntitle: Go\n---\nyes")
	mustWrite(t, filepath.Join(root, legacyWorkspace, FilesDirName, "note.txt"), "written by a conversation")
	for _, name := range instructionFiles {
		mustWrite(t, filepath.Join(root, legacyWorkspace, name), "generated")
	}
}

// The conversation that was live becomes the first assistant, with its jobs.
// Every other one goes: it lost its provider session when it was archived, so
// nobody could ever answer in it again, and nobody opens the cockpit to two
// hundred dead threads.
func TestMigrationKeepsTheLiveConversationAndDropsTheRest(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	now := time.Now().UTC()
	live := "11111111-1111-4111-8111-111111111111"
	old := "22222222-2222-4222-8222-222222222222"
	seedLegacy(t, root,
		[]legacySummary{
			{Summary: Summary{ID: live, Title: "Live one", CoderID: "claude", LastMessageAt: now}, Status: legacyStatusActive},
			{Summary: Summary{ID: old, Title: "Old one", CoderID: "claude", LastMessageAt: now.Add(-time.Hour)}, Status: "archived"},
		},
		map[string]Instance{
			live: {Summary: Summary{ID: live, Title: "Live one", CoderID: "claude"}},
			old:  {Summary: Summary{ID: old, Title: "Old one", CoderID: "claude"}},
		},
		[]Job{{Terminal: "term-1", State: JobSteering, CreatedAt: now}},
	)

	svc, _, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	list := svc.List()
	if len(list) != 1 || list[0].ID != live {
		t.Fatalf("want the live conversation as the one assistant, got %+v", list)
	}
	if _, err := os.Stat(filepath.Join(root, "instances", live, "transcript.json")); err != nil {
		t.Fatalf("want the transcript in the assistant's own directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, legacyTranscripts)); !os.IsNotExist(err) {
		t.Fatalf("want the old transcript directory gone, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "archive")); !os.IsNotExist(err) {
		t.Fatal("the migration grew an archive directory")
	}
	if _, err := svc.Get(old); err == nil {
		t.Fatal("a conversation that was over came back as an assistant")
	}

	// The jobs were the one assistant's, so they go to the one that stays and
	// keep steering their coders.
	jobs := NewJobs(NewStore(dir))
	job, ok := jobs.Find("term-1")
	if !ok || job.Owner != live || !job.State.Open() {
		t.Fatalf("want the job carried over to the live assistant, got %+v (%v)", job, ok)
	}
	if _, err := os.Stat(filepath.Join(root, legacyJobs)); !os.IsNotExist(err) {
		t.Fatalf("want the old jobs file moved, got %v", err)
	}

	// The workspace became the live one's: what the conversations wrote and
	// its own uploads are in it, the uploads of the archived one went with
	// that conversation, the memory moved up to where every assistant reads
	// it, and the generated files are gone, the next turn writes its own.
	own := filepath.Join(root, "instances", live, workspaceDirName)
	for _, path := range []string{
		filepath.Join(own, FilesDirName, "note.txt"),
		filepath.Join(own, uploadDirName, "pic.png"),
		filepath.Join(root, "memory", "likes-go.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("want %s carried over: %v", path, err)
		}
	}
	for _, path := range []string{
		filepath.Join(own, uploadDirName, old),
		filepath.Join(own, uploadDirName, live),
		filepath.Join(own, "memory"),
		filepath.Join(root, legacyWorkspace),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("want %s gone, got %v", path, err)
		}
	}
	// The shared instruction file is gone with the shared workspace; what stands
	// in its place is this assistant's own, written by the same start.
	if text := mustRead(t, filepath.Join(own, "CLAUDE.md")); text == "generated" || !strings.Contains(text, "You are `"+live+"`") {
		t.Fatalf("want the assistant's own instructions in its workspace, got:\n%.200s", text)
	}
	// And a link an old answer carries still lands on the file.
	_, workspace, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{})
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if _, err := workspace.ResolveWorkspaceFile(live, FilesDirName+"/note.txt"); err != nil {
		t.Fatalf("an old link does not resolve any more: %v", err)
	}
	if entries := workspace.Memory(); len(entries) != 1 || entries[0].Title != "Go" {
		t.Fatalf("want the memory read from its new place, got %+v", entries)
	}
}

// A state directory that has already moved is not looked at twice, and a fresh
// install has nothing to move at all.
func TestMigrationRunsOnceAndSkipsAFreshInstall(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{}); err != nil {
		t.Fatalf("fresh install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(Root(dir), "archive")); !os.IsNotExist(err) {
		t.Fatal("a fresh install must not grow an archive directory")
	}

	svc, _, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{})
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	created, err := svc.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	again, _, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{})
	if err != nil {
		t.Fatalf("third start: %v", err)
	}
	if list := again.List(); len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("want the assistant untouched by a later start, got %+v", list)
	}
}

// A transcript the index never knew about is a conversation that is over like
// any other: it goes with the directory, so nothing is left behind that would
// make the migration look unfinished and run again.
func TestMigrationDropsATranscriptTheIndexNeverKnew(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	live := "11111111-1111-4111-8111-111111111111"
	seedLegacy(t, root,
		[]legacySummary{{Summary: Summary{ID: live, Title: "Live one", CoderID: "claude"}, Status: legacyStatusActive}},
		map[string]Instance{live: {Summary: Summary{ID: live, Title: "Live one", CoderID: "claude"}}},
		nil,
	)
	stray := filepath.Join(root, legacyTranscripts, "stray.json")
	if err := os.WriteFile(stray, []byte(`{"id":"stray"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc, _, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, legacyTranscripts)); !os.IsNotExist(err) {
		t.Fatalf("want the old directory gone with its leftovers, got %v", err)
	}
	// The one that was live is still there, which is what the sweep may not
	// take with it.
	if list := svc.List(); len(list) != 1 || list[0].ID != live {
		t.Fatalf("want the live conversation carried over, got %+v", list)
	}
	if _, err := os.Stat(filepath.Join(root, "instances", live, "transcript.json")); err != nil {
		t.Fatalf("want its transcript in place: %v", err)
	}
}

// The last release made its transcript directory with the first transcript it
// saved, while the memory sheet wrote into the shared workspace from the first
// page on. So a state directory that only ever held memory has a workspace and
// no transcript directory, and the memory in it has to come up like any other.
func TestMigrationLiftsTheMemoryOfAWorkspaceThatNeverHadAConversation(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	mustWrite(t, filepath.Join(root, legacyWorkspace, legacyMemory, "likes-go.md"), "---\ntitle: Go\n---\nyes")
	for _, name := range instructionFiles {
		mustWrite(t, filepath.Join(root, legacyWorkspace, name), "generated")
	}

	svc, _, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if len(svc.List()) != 0 {
		t.Fatalf("want no assistant, got %+v", svc.List())
	}
	if _, err := os.Stat(filepath.Join(root, "memory", "likes-go.md")); err != nil {
		t.Fatalf("want the memory lifted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, legacyWorkspace)); !os.IsNotExist(err) {
		t.Fatalf("want the old workspace gone, got %v", err)
	}
}

// A workspace directory lingering beside instances that already exist is a
// leftover of a start that moved without it, not the old layout: reading it
// as one would move nothing (the memory already moved) and write an empty
// index over the assistants that live.
func TestMigrationLeavesAStrayWorkspaceBesideTheNewLayoutAlone(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	svc, _, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	created, err := svc.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	mustWrite(t, filepath.Join(root, "memory", "moved.md"), "---\ntitle: Moved\n---\nyes")
	mustWrite(t, filepath.Join(root, legacyWorkspace, legacyMemory, "stale.md"), "---\ntitle: Stale\n---\nno")

	again, _, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{})
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if list := again.List(); len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("want the assistant untouched, got %+v", list)
	}
	if _, err := os.Stat(filepath.Join(root, "memory", "moved.md")); err != nil {
		t.Fatalf("want the moved memory in place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, legacyWorkspace, legacyMemory, "stale.md")); err != nil {
		t.Fatalf("want the stray workspace left alone: %v", err)
	}
}

// An index full of conversations none of which was live leaves no assistant
// behind, and the jobs go with them: a job whose owner does not exist can never
// be checked and never reported, so it is not state worth keeping.
func TestMigrationWithoutALiveConversationKeepsNothing(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	old := "22222222-2222-4222-8222-222222222222"
	seedLegacy(t, root,
		[]legacySummary{{Summary: Summary{ID: old, Title: "Old one", CoderID: "claude"}, Status: "archived"}},
		map[string]Instance{old: {Summary: Summary{ID: old, Title: "Old one", CoderID: "claude"}}},
		[]Job{{Terminal: "term-1", State: JobSteering}},
	)

	svc, _, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if len(svc.List()) != 0 {
		t.Fatalf("want no assistant, got %+v", svc.List())
	}
	if _, err := os.Stat(filepath.Join(root, legacyJobs)); !os.IsNotExist(err) {
		t.Fatalf("want the jobs nobody can own gone, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, legacyTranscripts)); !os.IsNotExist(err) {
		t.Fatalf("want the old transcript directory gone, got %v", err)
	}
	// The index the new shape writes is a list, readable and empty.
	data, err := os.ReadFile(filepath.Join(root, "assistant.json"))
	if err != nil {
		t.Fatalf("read the index: %v", err)
	}
	var index []Summary
	if err := json.Unmarshal(data, &index); err != nil || len(index) != 0 {
		t.Fatalf("want an empty index, got %q (%v)", data, err)
	}
	// Nobody is left to own the workspace, so it went with the conversations;
	// the memory is shared and stays.
	if _, err := os.Stat(filepath.Join(root, legacyWorkspace)); !os.IsNotExist(err) {
		t.Fatalf("want the old workspace gone, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "memory", "likes-go.md")); err != nil {
		t.Fatalf("want the memory kept: %v", err)
	}
}
