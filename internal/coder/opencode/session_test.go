package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureRow is one session the way the CLI answers the list query, recorded
// against opencode 1.18.23. A row without a directory gets a real one, so the
// vanished-directory check keeps it.
type fixtureRow struct {
	id      string
	title   string
	dir     string
	cockpit string
	updated int64
}

var sessionFixtures = []fixtureRow{
	{id: "ses_fc6480f13ffeS2hWoSoid3ir6k", title: "Greeting and one-word answer", updated: 1787674753933},
	{id: "ses_fc6374637ffeU6S5j7RziNAGjI", title: "assistant chat", cockpit: "11111111-2222-4333-8444-555555555555", updated: 1787675851208},
}

func touchDB(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, dbFileName), []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func listJSON(t *testing.T, rows []fixtureRow) []byte {
	t.Helper()
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		entry := map[string]any{
			"id": row.id, "title": row.title, "directory": row.dir, "time_updated": row.updated,
		}
		if row.cockpit != "" {
			entry["cockpit"] = row.cockpit
		} else {
			entry["cockpit"] = nil
		}
		out = append(out, entry)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// fixtureRepository answers the list query from recorded rows, the way the
// installed CLI would.
func fixtureRepository(t *testing.T, rows []fixtureRow) *sessionRepository {
	t.Helper()
	root := t.TempDir()
	touchDB(t, root)
	answered := make([]fixtureRow, len(rows))
	copy(answered, rows)
	for i := range answered {
		if answered[i].dir == "" {
			answered[i].dir = t.TempDir()
		}
	}
	return &sessionRepository{
		dataRoot: root,
		query: func(sql string) ([]byte, error) {
			switch {
			case strings.Contains(sql, "parent_id IS NULL"):
				return listJSON(t, answered), nil
			case strings.Contains(sql, "AS tokens"):
				// The usage reading of a turn's end, see assistant.go: this
				// store recorded none.
				return []byte("[]"), nil
			default:
				t.Fatalf("unexpected query: %s", sql)
				return nil, nil
			}
		},
		remove: func(string) error { return nil },
	}
}

func TestListReadsTheSessionRows(t *testing.T) {
	dir := t.TempDir()
	r := fixtureRepository(t, []fixtureRow{
		{id: "ses_aaa", title: "one", dir: dir, updated: 1787674753933},
	})
	sessions := r.List()
	if len(sessions) != 1 {
		t.Fatalf("sessions = %v, want one", sessions)
	}
	s := sessions[0]
	if s.SessionID != "ses_aaa" || s.Name != "one" || s.CWD == "" {
		t.Fatalf("session = %+v", s)
	}
	if !s.UpdatedAt.Equal(time.UnixMilli(1787674753933).UTC()) {
		t.Fatalf("updated = %v, want the row's milliseconds", s.UpdatedAt)
	}
}

// The placeholder title is machine text ("New session - <timestamp>"), so
// such a session lists as unnamed and gets the stable fallback name.
func TestListTreatsThePlaceholderTitleAsUnnamed(t *testing.T) {
	r := fixtureRepository(t, []fixtureRow{
		{id: "ses_fc6480f13ffeS2hWoSoid3ir6k", title: "New session - 2026-08-25T16:19:11.212Z", updated: 1},
	})
	sessions := r.List()
	if len(sessions) != 1 || sessions[0].Name != "coder-ses_fc64" {
		t.Fatalf("sessions = %v, want the fallback name", sessions)
	}
}

// A conversation session pre-created for the assistant carries the cockpit's
// id in its metadata and is listed under it: that id is what the reservation
// hides and what a handover resumes.
func TestListReportsAConversationUnderItsCockpitId(t *testing.T) {
	r := fixtureRepository(t, sessionFixtures)
	var ids []string
	for _, s := range r.List() {
		ids = append(ids, s.SessionID)
	}
	joined := strings.Join(ids, ",")
	if !strings.Contains(joined, "11111111-2222-4333-8444-555555555555") {
		t.Fatalf("ids = %v, want the cockpit id listed", ids)
	}
	if strings.Contains(joined, "ses_fc6374637ffeU6S5j7RziNAGjI") {
		t.Fatalf("ids = %v, want opencode's own id hidden behind the mapping", ids)
	}
	if got := r.nativeID("11111111-2222-4333-8444-555555555555"); got != "ses_fc6374637ffeU6S5j7RziNAGjI" {
		t.Fatalf("nativeID = %q, want opencode's own id", got)
	}
}

func TestListSkipsSessionsWhoseDirectoryVanished(t *testing.T) {
	dir := t.TempDir()
	r := fixtureRepository(t, []fixtureRow{{id: "ses_aaa", title: "one", dir: dir, updated: 1}})
	if got := len(r.List()); got != 1 {
		t.Fatalf("want the session while its directory stands, got %d", got)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if got := len(r.List()); got != 0 {
		t.Fatalf("want the session dropped with its directory, got %d", got)
	}
}

// Without the database opencode has never run here, and asking the CLI would
// create one: the list answers nothing and runs no query at all.
func TestListAsksNothingWithoutADatabase(t *testing.T) {
	r := &sessionRepository{
		dataRoot: t.TempDir(),
		query: func(sql string) ([]byte, error) {
			t.Fatal("no database, no query")
			return nil, nil
		},
	}
	if got := r.List(); len(got) != 0 {
		t.Fatalf("sessions = %v, want none", got)
	}
}

// Reading through the CLI costs a process start, so the parsed list is cached
// against the database files and a scan only pays when the database moved.
func TestListCachesWhileTheDatabaseStandsStill(t *testing.T) {
	root := t.TempDir()
	touchDB(t, root)
	dir := t.TempDir()
	queries := 0
	r := &sessionRepository{
		dataRoot: root,
		query: func(sql string) ([]byte, error) {
			queries++
			return listJSON(t, []fixtureRow{{id: "ses_aaa", title: "one", dir: dir, updated: 1}}), nil
		},
	}
	r.List()
	r.List()
	if queries != 1 {
		t.Fatalf("want one query for an unmoved database, got %d", queries)
	}
	if err := os.WriteFile(filepath.Join(root, dbFileName), []byte("db moved"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.List()
	if queries != 2 {
		t.Fatalf("want a fresh query after the database moved, got %d", queries)
	}
}

// A session created moments ago has to be found: its insert moves the
// database files, so the next reading scans fresh and the lookups a resume
// and a turn stand on (exists, nativeID) see the young mapping.
func TestAMovedDatabaseAnswersTheYoungSession(t *testing.T) {
	root := t.TempDir()
	touchDB(t, root)
	dir := t.TempDir()
	rows := []fixtureRow{}
	r := &sessionRepository{
		dataRoot: root,
		query:    func(sql string) ([]byte, error) { return listJSON(t, rows), nil },
	}
	if r.exists("11111111-2222-4333-8444-555555555555") {
		t.Fatal("an empty store lists nothing")
	}
	rows = []fixtureRow{{id: "ses_fresh", title: "chat", dir: dir, updated: 1, cockpit: "11111111-2222-4333-8444-555555555555"}}
	if err := os.WriteFile(filepath.Join(root, dbFileName), []byte("db moved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !r.exists("11111111-2222-4333-8444-555555555555") {
		t.Fatal("the young session has to be found")
	}
	if got := r.nativeID("11111111-2222-4333-8444-555555555555"); got != "ses_fresh" {
		t.Fatalf("nativeID = %q, want the fresh mapping", got)
	}
}

// A delete goes through opencode's own command under opencode's own id, and
// takes the cockpit's upload directory with it.
func TestDeleteSessionRemovesRecordAndFiles(t *testing.T) {
	r := fixtureRepository(t, sessionFixtures)
	removed := ""
	r.remove = func(nativeID string) error {
		removed = nativeID
		return nil
	}
	files, err := r.filesDir("11111111-2222-4333-8444-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(files, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteSession("11111111-2222-4333-8444-555555555555"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if removed != "ses_fc6374637ffeU6S5j7RziNAGjI" {
		t.Fatalf("removed = %q, want opencode's own id", removed)
	}
	if _, err := os.Stat(files); !os.IsNotExist(err) {
		t.Fatalf("the upload directory has to go with the session, stat: %v", err)
	}
}

// The id comes from outside, so nothing of it may travel into a query or a
// path: only the tmux-safe alphabet passes.
func TestSessionIdsAreHeldToTheirAlphabet(t *testing.T) {
	r := fixtureRepository(t, nil)
	for _, id := range []string{"", "../escape", "a'b", "ses x"} {
		if err := r.DeleteSession(id); err == nil {
			t.Errorf("delete %q: want a refusal", id)
		}
		if _, err := r.filesDir(id); err == nil {
			t.Errorf("filesDir %q: want a refusal", id)
		}
	}
}
