package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/clirun"
)

func compactOf(events []assistant.Event) *assistant.CompactReport {
	for _, ev := range events {
		if ev.Kind == assistant.EventCompact {
			return ev.Compact
		}
	}
	return nil
}

// A compaction is no turn of opencode: it runs the cockpit's own helper on
// opencode's id of the session, behind the end of options, and never creates
// a session.
func TestACompactionRunsTheHelperOnTheNativeSession(t *testing.T) {
	r := testRunner(t)
	cmd, err := r.CompactCommand(assistant.TurnRequest{SessionID: cockpitID, Resume: true, Workdir: t.TempDir(), Prompt: "ignored", Model: "opencode/big-pickle"})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	want := append(append([]string{}, CompactHelper...), "--", nativeID)
	if cmd.Name != clirun.Executable() || !slices.Equal(cmd.Args, want) {
		t.Fatalf("the compaction runs as %s %v, want %v", cmd.Name, cmd.Args, want)
	}
	if _, err := r.CompactCommand(assistant.TurnRequest{SessionID: cockpitID, Workdir: t.TempDir()}); err == nil {
		t.Fatal("a session that does not exist has to be refused, never created")
	}
}

// The server of a compaction is bound to loopback and gets its password
// through its environment, never on its command line.
func TestTheServerListensOnLoopbackBehindItsPassword(t *testing.T) {
	cmd := serveCommand(t.TempDir(), 4242, "secret-pass")
	argv := strings.Join(cmd.Args, " ")
	if argv != "opencode serve --port 4242 --hostname 127.0.0.1 --pure" {
		t.Fatalf("the server runs as %s", argv)
	}
	if !slices.Contains(cmd.Env, "OPENCODE_SERVER_PASSWORD=secret-pass") {
		t.Fatal("the password is not in the server's environment")
	}
}

// fakeServe is a server behind the password over a store of leftovers: its
// summarize answers as the test says, abort answers true, a delete takes the
// message out of the store. It notes every call and every stop.
type fakeServe struct {
	t         *testing.T
	summarize func(w http.ResponseWriter, r *http.Request)
	store     *fakeStore
	asked     map[string]string
	calls     []string
	stops     int
	// busy makes every delete answer what opencode answers while the
	// session runs.
	busy bool
}

func (f *fakeServe) start() (*throwawayServe, error) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != "one-time" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/summarize"):
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &f.asked)
			f.summarize(w, r)
		case r.Method == http.MethodDelete && f.busy:
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"name":"SessionBusyError","data":{"message":"Session is busy"}}`)
		case r.Method == http.MethodDelete:
			f.store.leftovers = slices.DeleteFunc(f.store.leftovers, func(id string) bool { return strings.HasSuffix(r.URL.Path, "/message/"+id) })
			_, _ = io.WriteString(w, "true")
		default:
			_, _ = io.WriteString(w, "true")
		}
	}))
	f.t.Cleanup(srv.Close)
	return &throwawayServe{base: srv.URL, password: "one-time", client: srv.Client(), end: func() { f.stops++ }}, nil
}

// fakeStore answers the reading of the session's newest answer and of what
// unfinished compactions left in it.
type fakeStore struct {
	answers   []map[string]any
	leftovers []string
}

func (s *fakeStore) query(sql string) ([]byte, error) {
	if !strings.Contains(sql, nativeID) {
		return []byte("[]"), nil
	}
	if strings.Contains(sql, "AS leftover") {
		rows := []map[string]string{}
		for _, id := range s.leftovers {
			rows = append(rows, map[string]string{"leftover": id})
		}
		return json.Marshal(rows)
	}
	return json.Marshal(s.answers)
}

var answered = map[string]any{"provider": "opencode", "model": "big-pickle", "tokens": 24396}

func compactWith(t *testing.T, ctx context.Context, f *fakeServe) compactionRecord {
	t.Helper()
	return compact(ctx, compactor{query: f.store.query, start: f.start}, nativeID)
}

func calls(f *fakeServe, want ...string) bool {
	for i := range want {
		want[i] = strings.Replace(want[i], " S/", " /session/"+nativeID+"/", 1)
	}
	return slices.Equal(f.calls, want)
}

// The summarize call carries the provider and the model of the session's
// newest answer, and its true is the compaction applied. The server is
// stopped before the record goes out.
func TestACompactionSummarizesWithTheSessionsModel(t *testing.T) {
	f := &fakeServe{t: t, store: &fakeStore{answers: []map[string]any{answered}}, summarize: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "true") }}
	rec := compactWith(t, context.Background(), f)
	if rec.Outcome != compactApplied || rec.Error != "" || rec.SessionID != nativeID {
		t.Fatalf("want the compaction applied, got %+v", rec)
	}
	if !calls(f, "POST S/summarize") || f.asked["providerID"] != "opencode" || f.asked["modelID"] != "big-pickle" {
		t.Fatalf("the server was asked %v with %v", f.calls, f.asked)
	}
	if f.stops != 1 {
		t.Fatalf("the server was stopped %d times", f.stops)
	}
}

// A session without an answer holds nothing to compact, and no server starts
// for it.
func TestASessionWithoutAnAnswerHasNothingToCompact(t *testing.T) {
	f := &fakeServe{t: t, store: &fakeStore{}, summarize: func(http.ResponseWriter, *http.Request) { t.Error("no server may be asked") }}
	rec := compactWith(t, context.Background(), f)
	if rec.Outcome != compactNothing || rec.Error != "" || f.stops != 0 {
		t.Fatalf("want nothing to compact without a server, got %+v and %d stops", rec, f.stops)
	}
}

// A refused summarize names opencode's own words, a hung one ends at the
// deadline. Either way what it left in the session is cleared, so the next
// turn is not spent on finishing it, and the server is stopped.
func TestAFailedCompactionSaysWhyAndLeavesNothing(t *testing.T) {
	refusedStore := &fakeStore{answers: []map[string]any{answered}}
	refused := &fakeServe{t: t, store: refusedStore, summarize: func(w http.ResponseWriter, _ *http.Request) {
		refusedStore.leftovers = []string{"msg_marker"}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"name":"BadRequest","data":{"message":"Missing key at providerID","kind":"Payload"}}`)
	}}
	rec := compactWith(t, context.Background(), refused)
	if rec.Outcome != "" || rec.Error != "BadRequest: Missing key at providerID" || refused.stops != 1 {
		t.Fatalf("a refusal reads %+v with %d stops", rec, refused.stops)
	}
	if !calls(refused, "POST S/summarize", "POST S/abort", "DELETE S/message/msg_marker") || len(refusedStore.leftovers) != 0 {
		t.Fatalf("a refusal left %v after %v", refusedStore.leftovers, refused.calls)
	}

	bare := &fakeServe{t: t, store: &fakeStore{answers: []map[string]any{answered}}, summarize: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }}
	if rec := compactWith(t, context.Background(), bare); rec.Error != "opencode answered 503" {
		t.Fatalf("a refusal without words reads %+v", rec)
	}

	hungStore := &fakeStore{answers: []map[string]any{answered}}
	hung := &fakeServe{t: t, store: hungStore, summarize: func(_ http.ResponseWriter, r *http.Request) {
		hungStore.leftovers = []string{"msg_summary", "msg_marker"}
		<-r.Context().Done()
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	rec = compactWith(t, ctx, hung)
	if rec.Outcome != "" || !strings.Contains(rec.Error, "longer than 15 minutes") || hung.stops != 1 {
		t.Fatalf("a hung server reads %+v with %d stops", rec, hung.stops)
	}
	if !calls(hung, "POST S/summarize", "POST S/abort", "DELETE S/message/msg_summary", "DELETE S/message/msg_marker") {
		t.Fatalf("a hung compaction was cleared with %v", hung.calls)
	}
}

// What a stopped compaction left is cleared before the next one asks, and a
// true that left the store unfinished, the answer of an aborted summarize,
// is no compaction.
func TestACompactionClearsWhatAnEarlierOneLeft(t *testing.T) {
	store := &fakeStore{answers: []map[string]any{answered}, leftovers: []string{"msg_old_summary", "msg_old_marker"}}
	f := &fakeServe{t: t, store: store, summarize: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "true") }}
	if rec := compactWith(t, context.Background(), f); rec.Outcome != compactApplied {
		t.Fatalf("want the compaction applied, got %+v", rec)
	}
	if !calls(f, "POST S/abort", "DELETE S/message/msg_old_summary", "DELETE S/message/msg_old_marker", "POST S/summarize") {
		t.Fatalf("the earlier leftovers were handled with %v", f.calls)
	}

	aborted := &fakeStore{answers: []map[string]any{answered}}
	a := &fakeServe{t: t, store: aborted, summarize: func(w http.ResponseWriter, _ *http.Request) {
		aborted.leftovers = []string{"msg_aborted", "msg_marker"}
		_, _ = io.WriteString(w, "true")
	}}
	rec := compactWith(t, context.Background(), a)
	if rec.Outcome != "" || rec.Error != "opencode finished no summary." || len(aborted.leftovers) != 0 {
		t.Fatalf("an aborted summarize reads %+v and left %v", rec, aborted.leftovers)
	}
}

// The helper's record is the report the compaction ends with. It reports no
// context: the newest answer in the store is from before the compaction.
func TestTheCompactionRecordIsItsReport(t *testing.T) {
	for _, tc := range []struct {
		rec     compactionRecord
		outcome string
		reason  string
	}{
		{compactionRecord{Outcome: compactApplied}, assistant.CompactApplied, ""},
		{compactionRecord{Outcome: compactNothing}, assistant.CompactNothing, ""},
		{compactionRecord{Error: "BadRequest: Missing key"}, "", "BadRequest: Missing key"},
	} {
		tc.rec.Type, tc.rec.SessionID = compactRecordType, nativeID
		line, err := json.Marshal(tc.rec)
		if err != nil {
			t.Fatal(err)
		}
		r := usageRunner(t, []map[string]any{{"provider": "opencode", "model": "big-pickle", "tokens": 24396}})
		events := runTurn(t, r, string(line))
		report := compactOf(events)
		if report == nil || report.Outcome != tc.outcome || report.Reason != tc.reason {
			t.Fatalf("%s reads %+v in %+v", line, report, events)
		}
		for _, ev := range events {
			if ev.Kind == assistant.EventUsage || ev.Kind == assistant.EventError {
				t.Fatalf("%s sent %+v", line, ev)
			}
		}
	}
}

// The summary a compaction writes is the newest assistant message, and what
// it took in is the conversation it summarized: the reading and the model of
// the compaction come from the answer before it.
func TestTheSummaryIsNoReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), dbFileName)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`,
		`INSERT INTO message VALUES
			('msg_1','` + nativeID + `',1,'{"role":"user","summary":{"diffs":[]}}'),
			('msg_2','` + nativeID + `',2,'{"role":"assistant","providerID":"opencode","modelID":"big-pickle","tokens":{"input":20000,"cache":{"read":4396,"write":0}}}'),
			('msg_3','` + nativeID + `',3,'{"role":"user"}'),
			('msg_4','` + nativeID + `',4,'{"role":"assistant","providerID":"opencode","modelID":"big-pickle","summary":true,"mode":"compaction","tokens":{"input":1373,"cache":{"read":0,"write":0}}}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	out, err := databaseQuery(path)(strings.ReplaceAll(usageQuery, "%s", nativeID))
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Model  string `json:"model"`
		Tokens int    `json:"tokens"`
	}
	if err := json.Unmarshal(out, &rows); err != nil || len(rows) != 1 || rows[0].Tokens != 24396 || rows[0].Model != "big-pickle" {
		t.Fatalf("the reading is %s", out)
	}
}
