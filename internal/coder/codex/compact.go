package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/clirun"
)

// No turn compacts a codex session (verified on 0.172.0): `codex exec` reads
// `/compact` as words for the model and has no flag for it. Its app server
// does, thread/compact/start on a loaded thread, which runs the compaction as
// a turn of its own, the summary written into the session's rollout as a
// compacted record. So a compaction runs CompactHelper, a command of the
// cockpit's own binary, detached like a turn: it starts an app server, loads
// the session, asks it, waits for the end of that turn and writes one record
// of how it went, which the runner's parser reads like any turn's output. A
// restart or an update of the cockpit leaves it running and picks its record
// up.

// CompactHelper is the argv of that command behind the binary. Its names stand
// in the command line of a running compaction, so they never change.
var CompactHelper = []string{"codex", "session-compact"}

// compactPatience bounds one compaction. A short session takes seconds, a
// long one a minute or more; a server that has not ended its turn by then
// hangs, and is stopped.
const compactPatience = 15 * time.Minute

const (
	compactRecordType = "compaction"
	compactApplied    = "applied"
	compactNothing    = "nothing"
)

type compactionRecord struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Outcome  string `json:"outcome,omitempty"`
	Error    string `json:"error,omitempty"`
}

// CompactCommand runs CompactHelper on codex's own id of the session, never
// one it would have to create, on the model the chat runs on.
func (r *runner) CompactCommand(req assistant.TurnRequest) (assistant.Command, error) {
	if !req.Resume {
		return assistant.Command{}, errors.New("The conversation session does not exist.")
	}
	native, err := validSessionID(r.sessions.nativeID(req.SessionID))
	if err != nil {
		return assistant.Command{}, errors.New("The conversation session has no valid id.")
	}
	self := clirun.Executable()
	if self == "" {
		return assistant.Command{}, errors.New("The cockpit could not find its own binary.")
	}
	args := append([]string{}, CompactHelper...)
	if req.Model != "" {
		args = append(args, "--model="+req.Model)
	}
	args = append(args, "--", native)
	return assistant.Command{Name: self, Args: args}, nil
}

// CompactSession is the body of CompactHelper. It answers the exit code, zero
// whenever the record went out.
func CompactSession(sessionID, model string, out io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), compactPatience)
	rec := compact(ctx, compactor{sessions: newSessionRepository(codexHome()), talk: overAppServer}, sessionID, model)
	cancel()
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
	sessions *sessionRepository
	talk     func(ctx context.Context, talk func(w io.Writer, r io.Reader) error) error
}

// compact asks codex to compact the session unless nothing was answered since
// it started or since its last compaction. codex compacts whatever it is
// given, a summary of a summary included, and says nothing to compact for no
// session; the others do, and a compaction costs about one answer.
func compact(ctx context.Context, c compactor, sessionID, model string) compactionRecord {
	rec := compactionRecord{Type: compactRecordType, ThreadID: sessionID}
	id, err := validSessionID(sessionID)
	if err != nil {
		rec.Error = err.Error()
		return rec
	}
	answered, err := c.sessions.answeredSinceCompaction(id)
	if err != nil {
		rec.Error = "codex's record of the session could not be read: " + err.Error()
		return rec
	}
	if !answered {
		rec.Outcome = compactNothing
		return rec
	}
	err = c.talk(ctx, func(w io.Writer, r io.Reader) error {
		return compactOver(w, r, id, model)
	})
	switch {
	case ctx.Err() != nil:
		rec.Error = fmt.Sprintf("It took longer than %d minutes.", int(compactPatience.Minutes()))
	case err != nil:
		rec.Error = err.Error()
	default:
		rec.Outcome = compactApplied
	}
	return rec
}

type compactNotice struct {
	ID     *int   `json:"id"`
	Method string `json:"method"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
	Params struct {
		ThreadID string `json:"threadId"`
		Item     struct {
			Type string `json:"type"`
		} `json:"item"`
		Turn struct {
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	} `json:"params"`
}

const compactRequest = 2

// compactOver reads past the answer to its request: it comes at once, and the
// compaction is only done once the turn it runs as ended (0.172.0: an item
// of type contextCompaction, then turn/completed).
func compactOver(w io.Writer, r io.Reader, threadID, model string) error {
	reader, err := handshake(w, r)
	if err != nil {
		return err
	}
	resume := map[string]any{"threadId": threadID, "excludeTurns": true}
	if model != "" {
		resume["model"] = model
	}
	if _, err := call(w, reader, 1, "thread/resume", resume); err != nil {
		return err
	}
	id := compactRequest
	if err := send(w, rpcMessage{ID: &id, Method: "thread/compact/start", Params: map[string]string{"threadId": threadID}}); err != nil {
		return err
	}
	compacted := false
	for {
		line, err := readLine(reader, nameLineLimit)
		var notice compactNotice
		if len(line) > 0 && json.Unmarshal(line, &notice) == nil {
			if done, result := readCompactNotice(notice, threadID, &compacted); done {
				return result
			}
		}
		if err != nil {
			return errors.New("codex ended before the compaction did.")
		}
	}
}

func readCompactNotice(n compactNotice, threadID string, compacted *bool) (bool, error) {
	if n.ID != nil && *n.ID == compactRequest && n.Method == "" {
		if n.Error != nil {
			return true, errors.New(clip(n.Error.Message))
		}
		return false, nil
	}
	if n.Params.ThreadID != threadID {
		return false, nil
	}
	switch n.Method {
	case "item/completed":
		if n.Params.Item.Type == "contextCompaction" {
			*compacted = true
		}
	case "turn/completed":
		turn := n.Params.Turn
		switch {
		case turn.Error != nil && strings.TrimSpace(turn.Error.Message) != "":
			return true, errors.New(clip(turn.Error.Message))
		case turn.Status == "interrupted":
			return true, errors.New("codex interrupted the compaction.")
		case turn.Status != "completed":
			return true, fmt.Errorf("codex ended the compaction with the status %s.", clip(turn.Status))
		case !*compacted:
			return true, errors.New("codex ended the turn without a compaction.")
		}
		return true, nil
	}
	return false, nil
}

func (r *sessionRepository) answeredSinceCompaction(sessionID string) (bool, error) {
	path, err := r.rollout(sessionID)
	if err != nil {
		return false, err
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	reader := bufio.NewReaderSize(f, 64<<10)
	answered := false
	for {
		raw, err := readLine(reader, recordLineLimit)
		switch {
		case compactedRecord(raw):
			answered = false
		case bytes.Contains(raw, []byte(`"`+itemCoder+`"`)):
			var line recordLine
			var item recordItem
			if json.Unmarshal(raw, &line) == nil && line.Type == recordEvent && line.Payload.Type == eventItem &&
				json.Unmarshal(line.Payload.Item, &item) == nil && item.Type == itemCoder {
				answered = true
			}
		}
		if errors.Is(err, io.EOF) {
			return answered, nil
		}
		if err != nil {
			return false, err
		}
	}
}

// compactedRecord reads the head of the line alone: the record carries the
// history that replaced the turns, hundreds of KB that a reading may cut, and
// codex writes its type right behind the timestamp and the ordinal.
func compactedRecord(raw []byte) bool {
	return bytes.Contains(raw[:min(len(raw), 256)], []byte(`"type":"`+recordCompacted+`"`))
}

func (p *parser) readCompaction(line []byte) error {
	var rec compactionRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		p.skipUnreadable(line, err)
		return nil
	}
	if rec.ThreadID != p.nativeID {
		return errors.New("The coder answered in a different conversation, so this turn was dropped.")
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
	return nil
}
