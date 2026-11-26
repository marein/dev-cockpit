package copilot

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/filesystem"
)

// copilot records every session as an event log on disk (events.jsonl in the
// session-state directory), so it can say what a session did without anybody
// looking at the screen. The screen carries the CLI's input line, and reading
// that as a message would turn whatever stands in it into words nobody said.
// The event log holds only what really happened, and a turn marks its own end.

// activityTailBytes is how much of an event log is read at all. The log
// carries tool payloads and attachments, so a long session grows far past
// what the last messages need; the tail is bounded for the same reason the
// claude reader bounds it. What a reading keeps of it and what it may cost is
// the rule every record reader shares, see coder.ActivityBounds and
// coder.SpendActivityBudget.
const activityTailBytes = 512 << 10

// eventLine is the part of one recorded event an activity reading needs.
// Everything else copilot writes into the log, deltas, assets, usage
// checkpoints, plan changes, is bookkeeping, not conversation.
type eventLine struct {
	Type string `json:"type"`
	Data struct {
		Content         string `json:"content"`
		ToolName        string `json:"toolName"`
		IsUserRequested bool   `json:"isUserRequested"`
	} `json:"data"`
}

// SessionActivity reports what a session last did and whether its turn is over,
// read from that session's event log.
func (p *Coder) SessionActivity(sessionID string, entries, budget int) (coder.Activity, error) {
	return p.sessions.activity(sessionID, entries, budget)
}

// SessionActivityStamp answers when a session's event log last moved, so the
// record watcher only re-reads a log that did.
func (p *Coder) SessionActivityStamp(sessionID string) (time.Time, error) {
	path, err := p.sessions.eventsFile(sessionID)
	if err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// BellSignal implements coder.BellReader. copilot's beep rings when a turn
// finishes and when a dialog waits for input; either way the coder has news,
// and it is read as the former.
func (p *Coder) BellSignal(string) coder.TurnSignal {
	return coder.TurnSignalOver
}

func (r *sessionRepository) activity(sessionID string, entries, budget int) (coder.Activity, error) {
	path, err := r.eventsFile(sessionID)
	if err != nil {
		return coder.Activity{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return coder.Activity{}, fmt.Errorf("This session has no event log to read.")
	}
	defer f.Close()

	// Only the end of the log is read, and the first line after the seek is
	// dropped, it is a fragment.
	if info, err := f.Stat(); err == nil && info.Size() > activityTailBytes {
		if _, err := f.Seek(info.Size()-activityTailBytes, io.SeekStart); err == nil {
			reader := bufio.NewReader(f)
			if _, err := reader.ReadString('\n'); err == nil {
				return readActivity(reader, entries, budget)
			}
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return coder.Activity{}, err
		}
	}
	return readActivity(f, entries, budget)
}

// eventsFile locates the event log of a session in the store this coder owns.
// The id comes from outside, so it is held inside the state root the same way
// filesDir holds the file uploads there.
func (r *sessionRepository) eventsFile(sessionID string) (string, error) {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return "", fmt.Errorf("Session identifier is required.")
	}
	file := filepath.Join(r.stateRoot, id, "events.jsonl")
	absRoot, _ := filepath.Abs(r.stateRoot)
	absFile, _ := filepath.Abs(file)
	if !filesystem.IsUnder(absFile, absRoot) {
		return "", fmt.Errorf("Invalid session identifier.")
	}
	return absFile, nil
}

// readActivity parses the recorded events, keeping the last entries rendered
// lines (the default when entries is zero or less), spending at most budget
// runes on the reading (zero or less means unlimited). A turn's end is
// copilot's own record: assistant.turn_end closes it, and anything that opens
// or feeds a turn afterwards, a user message, a turn start, means the coder is
// working. The log also marks the two states that look like work but are
// waiting: an unanswered permission ask (permission.requested with no answer
// after it), and a turn cut off by the process ending, which a later
// session.shutdown or session.resume line proves.
func readActivity(source io.Reader, entries, budget int) (coder.Activity, error) {
	keep, line := coder.ActivityBounds(entries, budget)
	var lines []string
	var tools []string
	trim := func() {
		if len(lines) > keep {
			lines = lines[len(lines)-keep:]
		}
	}
	flushTools := func() {
		if len(tools) == 0 {
			return
		}
		lines = append(lines, "coder ran "+strings.Join(tools, ", "))
		tools = nil
		trim()
	}
	// A log holds session bookkeeping before any turn, session.start and the
	// boot's permission and model changes, and a turn that was never recorded
	// is not open: the reading starts finished and empty, and only what
	// speaks of a turn below takes both back, see coder.Activity.Empty. The
	// claude reader marks the note its coder writes at boot the same way.
	finished := true
	awaiting := false
	empty := true

	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64*1024), activityTailBytes+4096)
	for scanner.Scan() {
		var event eventLine
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		switch event.Type {
		case "user.message":
			flushTools()
			if text := strings.TrimSpace(event.Data.Content); text != "" {
				lines = append(lines, "user: "+coder.OneLine(text))
				trim()
			}
			finished = false
		case "assistant.message":
			flushTools()
			if text := strings.TrimSpace(event.Data.Content); text != "" {
				lines = append(lines, "coder: "+coder.OneLine(text))
				trim()
			}
		case "tool.execution_start":
			if name := strings.TrimSpace(event.Data.ToolName); name != "" {
				tools = append(tools, name)
			}
		case "assistant.turn_start":
			finished = false
			awaiting = false
		case "assistant.turn_end":
			finished = true
			awaiting = false
		case "tool.user_requested":
			// A `!` shell escape: the user's own command, run outside any
			// turn, so no turn_end will ever close it. It opens here and its
			// completion below closes it, or a session whose last action was
			// an escape would read as working forever.
			finished = false
		case "tool.execution_complete":
			awaiting = false
			if event.Data.IsUserRequested {
				finished = true
			}
		case "permission.requested":
			// The coder asked its person and stands still until the answer:
			// an unanswered ask at the end of the log is a session waiting,
			// not working.
			awaiting = true
		case "permission.completed":
			awaiting = false
		case "session.shutdown", "session.resume":
			// A turn does not survive its process: whatever the log left
			// open before a shutdown, and whatever a resumed session boots
			// on top of, ended with the process that ran it. It speaks of
			// no turn of its own, so it leaves the reading empty.
			finished = true
			awaiting = false
			continue
		default:
			continue
		}
		empty = false
	}
	if err := scanner.Err(); err != nil {
		return coder.Activity{}, err
	}
	flushTools()
	return coder.Activity{Text: coder.SpendActivityBudget(lines, line, budget), Finished: finished, AwaitingApproval: awaiting, Empty: empty}, nil
}
