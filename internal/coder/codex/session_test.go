package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/terminal"
)

const (
	firstID  = "01a20f4f-7666-7eb3-b691-008d03cfe287"
	secondID = "01a20f67-5816-76a3-974b-ba6aea57b9bf"
	agentID  = "01a20f6a-0db4-7bf2-94cf-0eaf21c6aa21"
)

func writeRollout(t *testing.T, home, id string, meta map[string]any, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", "2026", "11", "24")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"id": id, "session_id": id, "source": "cli", "thread_source": "user", "base_instructions": map[string]string{"text": strings.Repeat("x", 300<<10)}}
	for k, v := range meta {
		payload[k] = v
	}
	line, err := json.Marshal(map[string]any{"type": "session_meta", "payload": payload})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-11-24T21-12-39-"+id+".jsonl")
	if err := os.WriteFile(path, append(line, []byte("\n{\"type\":\"event_msg\"}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeIndex(t *testing.T, home string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, indexFile), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testRepository(t *testing.T) (*sessionRepository, string, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := newSessionRepository(home)
	r.remove = func(string) error { t.Fatal("unexpected delete"); return nil }
	r.name = func(string, string) error { t.Fatal("unexpected naming"); return nil }
	return r, home, project
}

// The list is the rollout files: one session each, under codex's id, in the
// directory it was started in, named by the last line the index holds for it
// and newest first. A thread an agent of codex opened is no session of the
// user's, and a session whose directory is gone has nowhere to resume.
func TestSessionsAreReadFromTheRolloutFiles(t *testing.T) {
	r, home, project := testRepository(t)
	writeRollout(t, home, firstID, map[string]any{"cwd": project}, time.Hour)
	writeRollout(t, home, secondID, map[string]any{"cwd": project}, time.Minute)
	writeRollout(t, home, agentID, map[string]any{"cwd": project, "thread_source": "subagent"}, time.Second)
	writeRollout(t, home, "01a20f5d-2e4c-7561-8473-31a7c9f259fc", map[string]any{"cwd": filepath.Join(project, "gone")}, time.Second)
	writeRollout(t, home, "01a20f60-432c-79c2-9352-45ef7779ec7c", map[string]any{"cwd": project, "source": map[string]any{"subagent": "review"}, "thread_source": ""}, time.Second)
	writeIndex(t, home,
		`{"id":"`+firstID+`","thread_name":"Acknowledge greeting","updated_at":"2026-11-24T20:46:40Z"}`,
		`{"id":"`+firstID+`","thread_name":"second  name\n","updated_at":"2026-11-24T21:04:32Z"}`,
		`not json`,
	)

	got := r.List()
	if len(got) != 2 {
		t.Fatalf("sessions = %+v, want two", got)
	}
	if got[0].SessionID != secondID || got[0].Name != "coder-01a20f67" || got[0].CWD != project {
		t.Fatalf("newest = %+v, want the unnamed second session first", got[0])
	}
	if got[1].SessionID != firstID || got[1].Name != "second name" {
		t.Fatalf("older = %+v, want the first session under its last name", got[1])
	}
	for _, s := range got {
		if _, err := terminal.ValidateIdentifier(s.SessionID); err != nil {
			t.Fatalf("id %q cannot name a pane: %v", s.SessionID, err)
		}
	}
}

// codex writes the first line while the file already exists. A file caught
// in between is no session yet and is read again, never remembered as empty.
func TestARolloutStillBeingWrittenIsReadAgain(t *testing.T) {
	r, home, project := testRepository(t)
	path := writeRollout(t, home, firstID, map[string]any{"cwd": project}, 0)
	whole, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, whole[:200], 0o644); err != nil {
		t.Fatal(err)
	}
	if got := r.List(); len(got) != 0 {
		t.Fatalf("sessions = %+v, want none while the first line is not whole", got)
	}
	if err := os.WriteFile(path, whole, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := r.List(); len(got) != 1 || got[0].SessionID != firstID {
		t.Fatalf("sessions = %+v, want the session once its line is whole", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := r.List(); len(got) != 0 {
		t.Fatalf("sessions = %+v, want a session codex no longer has to be gone", got)
	}
}

func TestAFileThatOnlyLooksLikeARolloutIsNoSession(t *testing.T) {
	r, home, project := testRepository(t)
	path := writeRollout(t, home, firstID, map[string]any{"cwd": project, "id": secondID}, 0)
	if got := r.List(); len(got) != 0 {
		t.Fatalf("sessions = %+v, want none for a file whose name and record disagree", got)
	}
	if err := os.Rename(path, filepath.Join(filepath.Dir(path), "rollout-x-not-an-id.jsonl")); err != nil {
		t.Fatal(err)
	}
	if got := r.List(); len(got) != 0 {
		t.Fatalf("sessions = %+v, want none", got)
	}
}

// Deleting is codex's own command, the cockpit removes only what it added
// itself, the session's uploads.
func TestDeleteGoesThroughCodexAndTakesTheUploadsAlong(t *testing.T) {
	r, home, project := testRepository(t)
	writeRollout(t, home, firstID, map[string]any{"cwd": project}, time.Minute)
	writeRollout(t, home, secondID, map[string]any{"cwd": project}, time.Minute)
	var removed []string
	r.remove = func(id string) error { removed = append(removed, id); return nil }
	if _, err := r.SaveFile(firstID, "note.txt", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteSession(" " + firstID + " "); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != firstID {
		t.Fatalf("removed = %v, want codex asked for %s", removed, firstID)
	}
	if _, err := os.Stat(filepath.Join(home, filesDir, firstID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the uploads are still there: %v", err)
	}

	r.remove = func(string) error { return errors.New("codex delete failed: Error: failed to delete session") }
	if _, err := r.SaveFile(secondID, "kept.txt", strings.NewReader("kept")); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteSession(secondID); err == nil {
		t.Fatal("want codex's refusal passed on")
	}
	if files, err := r.ListFiles(secondID); err != nil || len(files) != 1 {
		t.Fatalf("files = %v, %v, want the uploads kept when codex kept the session", files, err)
	}
}

// An id is only ever codex's own shape, so it can name a directory and stand
// on a command line without holding an option or a path.
func TestAnIDThatIsNotCodexsIsRefusedEverywhere(t *testing.T) {
	r, _, _ := testRepository(t)
	for _, id := range []string{"", "../../etc", "--force", "01A20F4F-7666-7EB3-B691-008D03CFE287", firstID + "/..", "name with spaces"} {
		if err := r.DeleteSession(id); err == nil {
			t.Fatalf("delete took %q", id)
		}
		if err := r.NameSession(id, "x"); err == nil {
			t.Fatalf("naming took %q", id)
		}
		if _, err := r.ListFiles(id); err == nil {
			t.Fatalf("files took %q", id)
		}
		if _, err := r.SaveFile(id, "a.txt", strings.NewReader("x")); err == nil {
			t.Fatalf("upload took %q", id)
		}
	}
}

func TestSessionFilesLiveInADirectoryOfTheCockpitsOwn(t *testing.T) {
	r, home, project := testRepository(t)
	writeRollout(t, home, firstID, map[string]any{"cwd": project}, time.Minute)
	saved, err := r.SaveFile(firstID, "note.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, filesDir, firstID, saved.Name)); err != nil {
		t.Fatalf("the upload is not where it belongs: %v", err)
	}
	opened, err := r.OpenFile(firstID, saved.Name)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(opened)
	opened.Close()
	if string(body) != "hello" {
		t.Fatalf("body = %q", body)
	}
	if _, err := r.DeleteFile(firstID, saved.Name); err != nil {
		t.Fatal(err)
	}
	if files, err := r.ListFiles(firstID); err != nil || len(files) != 0 {
		t.Fatalf("files = %v, %v, want none", files, err)
	}
}

// Before its first message a coder runs under the key the cockpit minted, and
// codex writes the session under an id of its own later. A file kept under
// the key would be lost then, so there are no files until codex wrote it.
func TestASessionKeepsFilesOnlyOnceCodexWroteIt(t *testing.T) {
	r, home, _ := testRepository(t)
	if _, err := r.SaveFile(firstID, "note.txt", strings.NewReader("hello")); !errors.Is(err, coder.ErrNoSessionFiles) {
		t.Fatalf("upload = %v, want ErrNoSessionFiles", err)
	}
	if files, err := r.ListFiles(firstID); err != nil || len(files) != 0 {
		t.Fatalf("files = %v, %v, want none", files, err)
	}
	if _, err := os.Stat(filepath.Join(home, filesDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a directory was made for a session codex has not written: %v", err)
	}
}

// The tie between a pane and its session is the rollout the pane's process
// holds open, and nothing else it has open.
func TestTheSessionOfAProcessIsTheRolloutItHoldsOpen(t *testing.T) {
	r, home, project := testRepository(t)
	if id, ok := r.ProcessSession(os.Getpid()); ok {
		t.Fatalf("a process without a rollout answered %q", id)
	}
	other, err := os.Create(filepath.Join(project, "rollout-2026-11-24T21-12-39-"+secondID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if id, ok := r.ProcessSession(os.Getpid()); ok {
		t.Fatalf("a rollout outside codex's sessions answered %q", id)
	}
	held, err := os.Open(writeRollout(t, home, firstID, map[string]any{"cwd": project}, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if id, ok := r.ProcessSession(os.Getpid()); !ok || id != firstID {
		t.Fatalf("session = %q, %v, want %s", id, ok, firstID)
	}
}

func TestANameIsWrittenThroughCodexForAValidID(t *testing.T) {
	r, _, _ := testRepository(t)
	var named []string
	r.name = func(id, name string) error { named = append(named, id+"="+name); return nil }
	if err := r.NameSession(firstID, `-n "quoted"`); err != nil {
		t.Fatal(err)
	}
	if len(named) != 1 || named[0] != firstID+`=-n "quoted"` {
		t.Fatalf("named = %v", named)
	}
	var _ coder.SessionNamer = r
	var _ coder.LateSessionRecords = r
}

// The call speaks codex's app server protocol: initialize, then the name,
// each answered under its id, with whatever codex announces in between.
func TestNamingSpeaksTheAppServerProtocol(t *testing.T) {
	answer := func(lines ...string) io.Reader { return strings.NewReader(strings.Join(lines, "\n") + "\n") }
	var sent bytes.Buffer
	err := nameOver(&sent, answer(
		`{"method":"configWarning","params":{"summary":"`+strings.Repeat("w", 2<<20)+`"}}`,
		`{"id":0,"result":{"codexHome":"/x"}}`,
		`{"method":"thread/name/updated","params":{}}`,
		`{"id":1,"result":{}}`,
	), firstID, "a \"name\"\nwith a break")
	if err != nil {
		t.Fatal(err)
	}
	var requests []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(sent.String()), "\n") {
		var request map[string]any
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatalf("request is not one JSON line: %q", line)
		}
		requests = append(requests, request)
	}
	if len(requests) != 3 || requests[0]["method"] != "initialize" || requests[1]["method"] != "initialized" || requests[2]["method"] != "thread/name/set" {
		t.Fatalf("requests = %v", requests)
	}
	params := requests[2]["params"].(map[string]any)
	if params["threadId"] != firstID || params["name"] != "a \"name\"\nwith a break" {
		t.Fatalf("params = %v", params)
	}

	err = nameOver(io.Discard, answer(`{"id":0,"result":{}}`, `{"id":1,"error":{"code":-32600,"message":"thread not found"}}`), firstID, "x")
	if err == nil || !strings.Contains(err.Error(), "thread not found") {
		t.Fatalf("err = %v, want codex's refusal", err)
	}
	if err := nameOver(io.Discard, answer(`{"id":0,"result":{}}`), firstID, "x"); err == nil {
		t.Fatal("want an error when codex ends without an answer")
	}
}
