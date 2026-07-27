package copilot

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/filesystem"
)

// copilot records every session as an event log on disk (events.jsonl in the
// session-state directory), so it can say what a session did without anybody
// looking at the screen. The screen carries the CLI's input line, and reading
// that as a message would turn whatever stands in it into words nobody said.
// The event log holds only what really happened, and a turn marks its own end.

// activityEntries is how many recorded messages an activity reading carries by
// default; what the whole reading may cost is the caller's budget
// (coder.ActivityBudget in the default reading, zero lifts the cap), the same
// bounds the claude reader uses so a check pays the same for either coder. The
// newest message gets the whole budget first, only the older lines carry the
// per-line cut, see spendBudget; fewer entries leave more room per older line,
// up to one entry, which is only the newest message.
const (
	activityEntries = 30
	activityLine    = 600
	// activityTailBytes is how much of an event log is read at all. The log
	// carries tool payloads and attachments, so a long session grows far past
	// what the last messages need; the tail is bounded for the same reason the
	// claude reader bounds it.
	activityTailBytes = 512 << 10
)

// activityBounds resolves how many entries a reading keeps and how long one
// line may be. A budget of zero lifts the cap, and a line bound of zero means
// uncut.
func activityBounds(entries, budget int) (keep, line int) {
	keep = activityEntries
	if entries > 0 {
		keep = entries
	}
	if budget <= 0 {
		return keep, 0
	}
	line = activityLine
	if entries > 0 && budget/entries > line {
		line = budget / entries
	}
	return keep, line
}

// eventLine is the part of one recorded event an activity reading needs.
// Everything else copilot writes into the log, deltas, assets, usage
// checkpoints, plan changes, is bookkeeping, not conversation.
type eventLine struct {
	Type string `json:"type"`
	Data struct {
		Content  string `json:"content"`
		ToolName string `json:"toolName"`
	} `json:"data"`
}

// SessionActivity reports what a session last did and whether its turn is over,
// read from that session's event log.
func (p *Coder) SessionActivity(sessionID string, entries, budget int) (coder.Activity, error) {
	return p.sessions.activity(sessionID, entries, budget)
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
// working.
func readActivity(source io.Reader, entries, budget int) (coder.Activity, error) {
	keep, line := activityBounds(entries, budget)
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
	finished := false

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
				lines = append(lines, "user: "+oneLine(text))
				trim()
			}
			finished = false
		case "assistant.message":
			flushTools()
			if text := strings.TrimSpace(event.Data.Content); text != "" {
				lines = append(lines, "coder: "+oneLine(text))
				trim()
			}
		case "tool.execution_start":
			if name := strings.TrimSpace(event.Data.ToolName); name != "" {
				tools = append(tools, name)
			}
		case "assistant.turn_start":
			finished = false
		case "assistant.turn_end":
			finished = true
		}
	}
	if err := scanner.Err(); err != nil {
		return coder.Activity{}, err
	}
	flushTools()
	return coder.Activity{Text: spendBudget(lines, line, budget), Finished: finished}, nil
}

// spendBudget spends the budget asymmetrically on the rendered lines, the
// same rule the claude reader applies: the newest line is the one a check
// judges against, so it gets the whole budget first and is cut only when it
// alone exceeds it; the older lines keep the per-line cut and share what is
// left, newest first, and whatever no longer fits falls off the top, oldest
// first. A budget of zero or less keeps every line whole.
func spendBudget(lines []string, lineRunes, budget int) string {
	if len(lines) == 0 {
		return ""
	}
	if budget <= 0 {
		return strings.Join(lines, "\n")
	}
	newest := truncate(lines[len(lines)-1], budget)
	budget -= len([]rune(newest))
	kept := []string{newest}
	for i := len(lines) - 2; i >= 0; i-- {
		cut := truncate(lines[i], lineRunes)
		cost := len([]rune(cut)) + 1
		if cost > budget {
			break
		}
		kept = append(kept, cut)
		budget -= cost
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return strings.Join(kept, "\n")
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// truncate cuts a line to max runes, visibly: a cut line says how much of the
// message is shown and how the rest is reached, never a bare ellipsis. A max
// of zero or less keeps the line whole.
func truncate(text string, max int) string {
	runes := []rune(text)
	if max <= 0 || len(runes) <= max {
		return text
	}
	return fmt.Sprintf("%s… [cut: %d of %d runes shown, use --full for the whole message]", string(runes[:max]), max, len(runes))
}
