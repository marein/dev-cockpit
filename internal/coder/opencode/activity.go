package opencode

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marein/dev-cockpit/internal/coder"
)

// opencode records every session as rows in its database, so it can say what
// a session did without anybody looking at the screen. The screen carries the
// TUI's input line, and reading that as a message would turn whatever stands
// in it into words nobody said. The rows hold only what really happened, and
// the assistant message marks its own end: its completion time is written
// when the step is over, and its finish reason says whether the turn went on
// into a tool call.

// activityEntries is how many recorded messages an activity reading carries
// by default; what the whole reading may cost is the caller's budget
// (coder.ActivityBudget in the default reading, zero lifts the cap), the same
// bounds the claude and copilot readers use so a check pays the same for
// every coder. The newest message gets the whole budget first, only the older
// lines carry the per-line cut, see spendBudget; fewer entries leave more
// room per older line, up to one entry, which is only the newest message.
const (
	activityEntries = 30
	activityLine    = 600
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

// activityQuery reads the tail of one session's conversation: the newest
// messages, each with its parts, projected down to what a reading needs. The
// projection is the bound that matters, a tool part carries its whole output
// in the row and none of that ever travels. The session is the anchor, so a
// session without messages still answers one row and an unknown session
// answers none.
const activityQuery = `SELECT m.id AS message,` +
	` json_extract(m.data,'$.role') AS role,` +
	` CASE WHEN json_extract(m.data,'$.time.completed') IS NULL THEN 0 ELSE 1 END AS completed,` +
	` CASE WHEN json_extract(m.data,'$.error') IS NULL THEN 0 ELSE 1 END AS failed,` +
	` json_extract(m.data,'$.finish') AS finish,` +
	` json_extract(p.data,'$.type') AS parttype,` +
	` json_extract(p.data,'$.text') AS text,` +
	` CASE WHEN json_extract(p.data,'$.synthetic') THEN 1 ELSE 0 END AS synthetic,` +
	` json_extract(p.data,'$.tool') AS tool` +
	` FROM session s` +
	` LEFT JOIN (SELECT id, session_id, data, time_created FROM message WHERE session_id = '%s'` +
	` ORDER BY time_created DESC, id DESC LIMIT %d) m ON m.session_id = s.id` +
	` LEFT JOIN part p ON p.message_id = m.id` +
	` WHERE s.id = '%s'` +
	` ORDER BY m.time_created ASC, m.id ASC, p.time_created ASC, p.id ASC`

type activityRow struct {
	Message   string `json:"message"`
	Role      string `json:"role"`
	Completed int    `json:"completed"`
	Failed    int    `json:"failed"`
	Finish    string `json:"finish"`
	PartType  string `json:"parttype"`
	Text      string `json:"text"`
	Synthetic int    `json:"synthetic"`
	Tool      string `json:"tool"`
}

// SessionActivity reports what a session last did and whether its turn is
// over, read from that session's rows.
func (p *Coder) SessionActivity(sessionID string, entries, budget int) (coder.Activity, error) {
	return p.sessions.activity(sessionID, entries, budget)
}

func (r *sessionRepository) activity(sessionID string, entries, budget int) (coder.Activity, error) {
	id, err := validSessionID(sessionID)
	if err != nil {
		return coder.Activity{}, err
	}
	if _, ok := r.dbStamp(); !ok {
		return coder.Activity{}, fmt.Errorf("This session has no record to read.")
	}
	native, err := validSessionID(r.nativeID(id))
	if err != nil {
		return coder.Activity{}, err
	}
	keep, line := activityBounds(entries, budget)
	out, err := r.query(fmt.Sprintf(activityQuery, native, keep, native))
	if err != nil {
		return coder.Activity{}, fmt.Errorf("This session's record could not be read.")
	}
	var rows []activityRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return coder.Activity{}, fmt.Errorf("This session's record could not be read.")
	}
	if len(rows) == 0 {
		return coder.Activity{}, fmt.Errorf(`No session "%s" was found.`, id)
	}
	return renderActivity(rows, keep, line, budget), nil
}

// renderActivity turns the rows into the lines a reader judges, newest last.
// A turn is over when the newest message is the coder's own answer, its end
// is written down (or it died with an error, which writes no end), and its
// finish reason does not hand on into a tool call.
func renderActivity(rows []activityRow, keep, line, budget int) coder.Activity {
	type message struct {
		id    string
		lines []string
		tools []string
	}
	var messages []message
	finished := false
	current := -1
	for _, row := range rows {
		if row.Message == "" {
			// The session's own row without a message: recorded, but nothing
			// said yet.
			continue
		}
		if current < 0 || messages[current].id != row.Message {
			messages = append(messages, message{id: row.Message})
			current = len(messages) - 1
			finished = row.Role == "assistant" && (row.Completed == 1 || row.Failed == 1) && row.Finish != "tool-calls"
		}
		speaker := "user"
		if row.Role == "assistant" {
			speaker = "coder"
		}
		switch row.PartType {
		case "text":
			if row.Synthetic == 1 {
				// A synthetic part is injected bookkeeping, not the user's
				// words.
				continue
			}
			if text := strings.TrimSpace(row.Text); text != "" {
				messages[current].lines = append(messages[current].lines, speaker+": "+oneLine(text))
			}
		case "tool":
			if name := strings.TrimSpace(row.Tool); name != "" {
				messages[current].tools = append(messages[current].tools, name)
			}
		}
	}
	if len(messages) > keep {
		messages = messages[len(messages)-keep:]
	}
	var lines []string
	for _, m := range messages {
		lines = append(lines, m.lines...)
		if len(m.tools) > 0 {
			lines = append(lines, "coder ran "+strings.Join(m.tools, ", "))
		}
	}
	return coder.Activity{Text: spendBudget(lines, line, budget), Finished: finished}
}

// spendBudget spends the budget asymmetrically on the rendered lines, the
// same rule the claude and copilot readers apply: the newest line is the one
// a check judges against, so it gets the whole budget first and is cut only
// when it alone exceeds it; the older lines keep the per-line cut and share
// what is left, newest first, and whatever no longer fits falls off the top,
// oldest first. A budget of zero or less keeps every line whole.
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
