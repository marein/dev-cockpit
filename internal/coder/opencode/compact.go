package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/clirun"
)

// No turn compacts an opencode session (verified on 1.19.0): `/compact` as
// the message of `opencode run` reaches the model as words, `--command
// compact` fails, and the server's `/api/session/{id}/compact` answers 503.
// What works is `POST /session/{id}/summarize` of a server, which answers
// once the summary is written. So a compaction runs CompactHelper, a command
// of the cockpit's own binary, detached like a turn: it boots a throwaway
// server, asks it, stops it and writes one record of how it went, which the
// runner's parser reads like any turn's output. A restart or an update of the
// cockpit leaves it running and picks its record up.

// CompactHelper is the argv of that command behind the binary. Its names stand
// in the command line of a running compaction, so they never change.
var CompactHelper = []string{"opencode", "session-compact"}

// compactPatience bounds one compaction. A short session takes seconds, a
// long one a minute or more; a server that has not answered by then hangs,
// and is stopped.
const compactPatience = 15 * time.Minute

// compactRecordType is the type of the record the helper writes, and the
// outcomes it names.
const (
	compactRecordType = "compaction"
	compactApplied    = "applied"
	compactNothing    = "nothing"
)

type compactionRecord struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Outcome   string `json:"outcome,omitempty"`
	Error     string `json:"error,omitempty"`
}

// CompactCommand runs CompactHelper on opencode's own id of the session,
// never one it would have to create.
func (r *runner) CompactCommand(req assistant.TurnRequest) (assistant.Command, error) {
	if !req.Resume {
		return assistant.Command{}, errors.New("The conversation session does not exist.")
	}
	native, err := validSessionID(r.sessions.nativeID(req.SessionID))
	if err != nil {
		return assistant.Command{}, err
	}
	self := clirun.Executable()
	if self == "" {
		return assistant.Command{}, errors.New("The cockpit could not find its own binary.")
	}
	args := append(append([]string{}, CompactHelper...), "--", native)
	return assistant.Command{Name: self, Args: args}, nil
}

// CompactSession is the body of CompactHelper: it compacts one session through
// a throwaway server in the directory it runs in, the session's own, and
// writes how it went as one record on out. It answers the exit code, zero
// whenever the record went out.
func CompactSession(sessionID string, out io.Writer) int {
	workdir, err := os.Getwd()
	rec := compactionRecord{Type: compactRecordType, SessionID: sessionID}
	if err != nil {
		rec.Error = err.Error()
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), compactPatience)
		rec = compact(ctx, compactor{
			query: databaseQuery(filepath.Join(dataDir(), dbFileName)),
			start: func() (*throwawayServe, error) { return startServe(workdir) },
		}, sessionID)
		cancel()
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return 1
	}
	if _, err := fmt.Fprintf(out, "%s\n", line); err != nil {
		return 1
	}
	return 0
}

type compactor struct {
	query func(sql string) ([]byte, error)
	start func() (*throwawayServe, error)
}

// compact asks a server to summarize the session with the model its newest
// answer ran on, which is what the call needs: a summarize without the
// session's provider and model fails. A session without an answer holds
// nothing to compact, and no server starts for it. What an earlier
// compaction left is cleared first, and what this one leaves when it does not
// go through after it, see leftover.go. The server is stopped before this
// returns, whatever happened.
func compact(ctx context.Context, c compactor, sessionID string) compactionRecord {
	rec := compactionRecord{Type: compactRecordType, SessionID: sessionID}
	native, err := validSessionID(sessionID)
	if err != nil {
		rec.Error = err.Error()
		return rec
	}
	out, err := c.query(fmt.Sprintf(usageQuery, native))
	if err != nil {
		rec.Error = "opencode's store could not be read: " + err.Error()
		return rec
	}
	var rows []struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		rec.Error = "opencode's store could not be read: " + err.Error()
		return rec
	}
	if len(rows) == 0 || strings.TrimSpace(rows[0].Provider) == "" || strings.TrimSpace(rows[0].Model) == "" {
		rec.Outcome = compactNothing
		return rec
	}
	s, err := c.start()
	if err != nil {
		rec.Error = err.Error()
		return rec
	}
	defer s.stop()
	if err := clearLeftovers(ctx, s, c.query, native); err != nil {
		rec.Error = "An earlier compaction that did not finish could not be cleared: " + err.Error()
		return rec
	}
	status, answer, err := s.call(ctx, http.MethodPost, "/session/"+native+"/summarize", map[string]string{
		"providerID": rows[0].Provider,
		"modelID":    rows[0].Model,
	})
	switch {
	case ctx.Err() != nil:
		rec.Error = fmt.Sprintf("It took longer than %d minutes.", int(compactPatience.Minutes()))
	case err != nil:
		rec.Error = err.Error()
	case status != http.StatusOK:
		rec.Error = summarizeRefusal(status, answer)
	case strings.TrimSpace(string(answer)) != "true":
		rec.Error = "opencode answered " + strings.TrimSpace(string(answer))
	default:
		rec.Outcome = compactApplied
	}
	// An aborted summarize answers true as well, so the store decides: a
	// compaction is applied once no part of it is left unfinished.
	if rec.Outcome == compactApplied {
		if left, err := leftoverMessages(c.query, native); err == nil && len(left) > 0 {
			rec.Outcome, rec.Error = "", "opencode finished no summary."
		}
	}
	if rec.Outcome == "" {
		cleanup, cancel := context.WithTimeout(context.Background(), leftoverPatience)
		defer cancel()
		if err := clearLeftovers(cleanup, s, c.query, native); err != nil {
			log.Printf("opencode compaction: what %s left could not be cleared, the next turn tries again: %v", native, err)
		}
	}
	return rec
}

// summarizeRefusal is opencode's own account of a refused summarize, the way
// a run's error record is worded, and the status where it gave none.
func summarizeRefusal(status int, answer []byte) string {
	var refusal errorRecord
	if json.Unmarshal(answer, &refusal.Error) == nil {
		if said := errorWording(refusal); said != "" {
			return said
		}
	}
	return fmt.Sprintf("opencode answered %d", status)
}

// readCompaction turns the helper's record into the report of the compaction.
func (p *parser) readCompaction(line []byte) {
	var rec compactionRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		p.skipUnreadable(line, err)
		return
	}
	p.compacted = true
	report := &assistant.CompactReport{Reason: rec.Error}
	switch rec.Outcome {
	case compactApplied:
		report.Outcome = assistant.CompactApplied
	case compactNothing:
		report.Outcome = assistant.CompactNothing
	}
	p.events <- assistant.Event{Kind: assistant.EventCompact, Compact: report}
}
