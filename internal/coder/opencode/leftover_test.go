package opencode

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
)

// The shapes opencode 1.19.0 leaves, read through the real driver: a finished
// compaction is none, a stopped one leaves its marker and the summary it cut
// off, a refused one its marker alone, an aborted one a summary that ended in
// an error, and a summary left without its marker is one too.
func TestTheLeftoversAreWhatNoFinishedSummaryAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), dbFileName)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	message := func(id string, at int, data string) string {
		return `('` + id + `','` + nativeID + `',` + strconv.Itoa(at) + `,'` + data + `')`
	}
	for _, statement := range []string{
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, data TEXT)`,
		`INSERT INTO message VALUES ` + strings.Join([]string{
			message("msg_01", 1, `{"role":"user"}`),
			message("msg_02", 2, `{"role":"assistant","parentID":"msg_01","finish":"stop"}`),
			message("msg_03", 3, `{"role":"user"}`),
			message("msg_04", 4, `{"role":"assistant","parentID":"msg_03","summary":true,"finish":"stop"}`),
			message("msg_05", 5, `{"role":"user"}`),
			message("msg_06", 6, `{"role":"assistant","parentID":"msg_05","summary":true}`),
			message("msg_07", 7, `{"role":"user"}`),
			message("msg_08", 8, `{"role":"user"}`),
			message("msg_09", 9, `{"role":"assistant","parentID":"msg_08","summary":true,"error":{"name":"MessageAbortedError"}}`),
			message("msg_10", 10, `{"role":"assistant","parentID":"msg_gone","summary":true,"finish":"stop","error":{"name":"MessageAbortedError"}}`),
			message("msg_11", 11, `{"role":"user"}`),
			message("msg_12", 12, `{"role":"assistant","parentID":"msg_11","finish":"stop"}`),
		}, ", "),
		`INSERT INTO part VALUES ('prt_1','msg_01','{"type":"text"}'), ('prt_3','msg_03','{"type":"compaction","auto":false}'),
			('prt_5','msg_05','{"type":"compaction","auto":false}'), ('prt_7','msg_07','{"type":"compaction","auto":false}'),
			('prt_8','msg_08','{"type":"compaction","auto":false}'), ('prt_11','msg_11','{"type":"text"}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	ids, err := leftoverMessages(databaseQuery(path), nativeID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"msg_10", "msg_09", "msg_08", "msg_07", "msg_06", "msg_05"}; !slices.Equal(ids, want) {
		t.Fatalf("the leftovers are %v, want %v", ids, want)
	}
}

// leftoverRunner is a runner whose store also answers the leftovers, with a
// server for clearing them.
func leftoverRunner(t *testing.T, f *fakeServe) *runner {
	t.Helper()
	base := fixtureRepository(t, sessionFixtures)
	plain := base.query
	base.query = func(sql string) ([]byte, error) {
		if strings.Contains(sql, "AS leftover") {
			return f.store.query(sql)
		}
		return plain(sql)
	}
	r := testRunner(t)
	r.sessions = base
	r.serve = func(string) (*throwawayServe, error) { return f.start() }
	return r
}

// A turn after a stopped compaction would come back as the summary opencode
// finishes first, so the turn clears what it left before it runs; a turn
// with nothing left starts no server, and one whose clearing fails does not
// run.
func TestATurnClearsWhatAStoppedCompactionLeft(t *testing.T) {
	never := func(http.ResponseWriter, *http.Request) { t.Error("a turn never summarizes") }
	req := assistant.TurnRequest{SessionID: cockpitID, Resume: true, Workdir: t.TempDir(), Prompt: "What is 2 plus 2?"}

	left := &fakeServe{t: t, store: &fakeStore{leftovers: []string{"msg_summary", "msg_marker"}}, summarize: never}
	cmd, err := leftoverRunner(t, left).Command(req)
	if err != nil || cmd.Args[len(cmd.Args)-1] != "What is 2 plus 2?" {
		t.Fatalf("the turn after the clearing is %v, %v", cmd.Args, err)
	}
	if !calls(left, "POST S/abort", "DELETE S/message/msg_summary", "DELETE S/message/msg_marker") || left.stops != 1 || len(left.store.leftovers) != 0 {
		t.Fatalf("the leftovers were handled with %v, %d stops, %v left", left.calls, left.stops, left.store.leftovers)
	}

	clean := &fakeServe{t: t, store: &fakeStore{}, summarize: never}
	if _, err := leftoverRunner(t, clean).Command(req); err != nil || len(clean.calls) != 0 || clean.stops != 0 {
		t.Fatalf("a clean session started a server: %v, %v", clean.calls, err)
	}

	busy := &fakeServe{t: t, store: &fakeStore{leftovers: []string{"msg_marker"}}, summarize: never, busy: true}
	_, err = leftoverRunner(t, busy).Command(req)
	if err == nil || err.Error() != "An earlier compaction that did not finish could not be cleared from this conversation." || busy.stops != 1 {
		t.Fatalf("a failed clearing reads %v with %d stops", err, busy.stops)
	}
}

// The refusal of a delete is worded the way opencode words it.
func TestAFailedClearingSaysWhy(t *testing.T) {
	f := &fakeServe{t: t, store: &fakeStore{leftovers: []string{"msg_marker"}}, busy: true}
	s, _ := f.start()
	err := clearLeftovers(t.Context(), s, f.store.query, nativeID)
	if err == nil || err.Error() != "SessionBusyError: Session is busy" {
		t.Fatalf("a busy session reads %v", err)
	}
}
