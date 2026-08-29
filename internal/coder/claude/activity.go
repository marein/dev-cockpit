package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
)

// claude keeps every session as a transcript on disk, so it can say what a
// session did without anybody looking at the screen. That matters for the one
// reader that is not a person: the screen carries claude's input line, and
// claude drafts the next prompt for its user into that line. Read as a message
// the draft becomes an instruction nobody gave, and a draft appearing looks
// like the session is working. The transcript has neither: it holds the
// messages that were really exchanged, and it ends where the turn ended.

// activityEntries is how many recorded messages an activity reading carries by
// default; what the whole reading may cost is the caller's budget
// (coder.ActivityBudget in the default reading, zero lifts the cap). The tail
// is what a check judges: the last exchange decides whether a job is done, not
// the beginning, and the newest message above all, so it gets the whole budget
// first and only the older lines carry the per-line cut, see spendBudget. A
// caller that asks for fewer entries gets more room per older line, up to one
// entry, which is only the newest message.
const (
	activityEntries = 30
	activityLine    = 600
	// activityTailBytes is how much of a transcript is read at all. Well beyond
	// the last thirty messages of any normal session, and bounded, so a session
	// that has run for days costs a check the same as a fresh one.
	activityTailBytes = 512 << 10
)

// activityBounds resolves how many entries a reading keeps and how long one
// line may be. The whole reading stays bounded by the budget (only a cut
// notice may stand a few runes past it), so fewer entries simply share the
// same budget among less lines; a budget of zero lifts the cap, and a line
// bound of zero means uncut.
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

// transcriptLine is the part of one recorded line an activity reading needs.
// Everything else claude writes into the transcript, its draft for the next
// prompt (`last-prompt`), queued input, modes, attachments, is not a message
// and is not read here: only entries that carry a message count. The
// timestamp dates the message itself, which matters because claude also
// touches the file with bookkeeping at boot and exit: the file moving
// proves nothing about the conversation moving.
type transcriptLine struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	Timestamp   string `json:"timestamp"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// contentBlock is one part of a message: text, the coder's own thinking, a tool
// call, or the result of one.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
}

// SessionActivity reports what a session last did and whether its turn is over,
// read from that session's transcript.
func (p *Coder) SessionActivity(sessionID string, entries, budget int) (coder.Activity, error) {
	return p.sessions.activity(sessionID, entries, budget)
}

// SessionActivityStamp answers when a session's transcript last moved, so
// the record watcher only re-reads a transcript that did.
func (p *Coder) SessionActivityStamp(sessionID string) (time.Time, error) {
	path, err := p.sessions.transcriptFile(sessionID)
	if err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

func (r *sessionRepository) activity(sessionID string, entries, budget int) (coder.Activity, error) {
	path, err := r.transcriptFile(sessionID)
	if err != nil {
		return coder.Activity{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return coder.Activity{}, fmt.Errorf("This session has no transcript to read.")
	}
	defer f.Close()

	// Only the end of the conversation is read. A transcript grows with every
	// turn, a long session reaches tens of megabytes, and reading all of it to
	// keep the last handful of messages would make every check cost the whole
	// history. The first line after the seek is dropped, it is a fragment.
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

// readActivity parses the recorded messages of a transcript, keeping the last
// entries of them (the default when entries is zero or less), spending at most
// budget runes on the reading (zero or less means unlimited).
func readActivity(source io.Reader, entries, budget int) (coder.Activity, error) {
	keep, line := activityBounds(entries, budget)
	var tail []transcriptLine
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64*1024), maxTranscriptLine)
	scanner.Split(newTranscriptSplit())
	for scanner.Scan() {
		var parsed transcriptLine
		if err := json.Unmarshal(scanner.Bytes(), &parsed); err != nil {
			continue
		}
		if parsed.IsSidechain || (parsed.Type != "user" && parsed.Type != "assistant") {
			// A sidechain belongs to a subagent, and everything that is not a
			// user or an assistant entry is bookkeeping, not conversation.
			continue
		}
		tail = append(tail, parsed)
		if len(tail) > keep {
			tail = tail[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return coder.Activity{}, err
	}
	return coder.Activity{
		Text:          renderTranscript(tail, line, budget),
		Finished:      turnFinished(tail),
		InToolCall:    inToolCall(tail),
		LastMessageAt: lastMessageAt(tail),
	}, nil
}

// lastMessageAt dates the newest recorded message, zero when there is none
// or the entry carries no readable timestamp.
func lastMessageAt(tail []transcriptLine) time.Time {
	if len(tail) == 0 {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339Nano, tail[len(tail)-1].Timestamp)
	if err != nil {
		return time.Time{}
	}
	return at
}

// inToolCall reports whether the record ends inside a tool call: the last
// entry is the coder calling a tool it still owes a result to. The phase
// matters to the interrupt heuristic alone, because an abort in it provably
// writes its own marker while an abort elsewhere may write nothing.
func inToolCall(tail []transcriptLine) bool {
	if len(tail) == 0 {
		return false
	}
	last := tail[len(tail)-1]
	if last.Type != "assistant" {
		return false
	}
	for _, block := range blocks(last) {
		if block.Type == "tool_use" {
			return true
		}
	}
	return false
}

// turnFinished reads the end of the conversation. A turn that is over ends with
// the coder's own answer; anything else, a tool call it still has to run, a tool
// result it has not answered yet, a prompt it just received, means it is
// working. The exceptions are the exchanges that never were a turn: the `!`
// shell escape, recorded as user entries although it is the user's own command,
// and the slash commands claude answers itself. Their output coming back ends
// the exchange, and no answer has to follow it, so a transcript ending on it is
// over, not working.
func turnFinished(tail []transcriptLine) bool {
	if len(tail) == 0 {
		return false
	}
	last := tail[len(tail)-1]
	if last.Type != "assistant" {
		return bashEcho(last) || localCommand(last) || interrupted(last)
	}
	for _, block := range blocks(last) {
		if block.Type == "tool_use" {
			return false
		}
	}
	return true
}

// bashEcho reports whether an entry is the recorded output of a `!` command,
// the <bash-stdout> echo claude writes as a user entry.
func bashEcho(entry transcriptLine) bool {
	return userEntryWithPrefix(entry, "<bash-stdout>")
}

// localCommand reports whether an entry is claude answering a slash command of
// its own, `/model` or `/theme`: the command never reaches a turn, and claude
// records what it printed as a user entry. A command that does start a turn, a
// skill or a project command, records the prompt it expands to instead, so it
// is not caught here and stays a turn like any other.
func localCommand(entry transcriptLine) bool {
	return userEntryWithPrefix(entry, "<local-command-stdout>") ||
		userEntryWithPrefix(entry, "<local-command-stderr>") ||
		userEntryWithPrefix(entry, "<local-command-caveat>")
}

// interrupted reports whether an entry is the marker claude records when the
// user aborts a turn (Escape, Ctrl+C). The stop hook does not fire for an
// abort, so this line is the only written trace that the turn is over.
func interrupted(entry transcriptLine) bool {
	return userEntryWithPrefix(entry, "[Request interrupted by user")
}

func userEntryWithPrefix(entry transcriptLine, prefix string) bool {
	if entry.Type != "user" {
		return false
	}
	for _, block := range blocks(entry) {
		return strings.HasPrefix(strings.TrimSpace(block.Text), prefix)
	}
	return false
}

// renderTranscript turns the recorded messages into the lines a reader judges,
// newest last. The lines are built uncut; how the budget is spent on them is
// spendBudget's rule.
func renderTranscript(tail []transcriptLine, lineRunes, budget int) string {
	var lines []string
	for _, entry := range tail {
		speaker := "user"
		if entry.Type == "assistant" {
			speaker = "coder"
		}
		var tools []string
		for _, block := range blocks(entry) {
			switch block.Type {
			case "text":
				if text := strings.TrimSpace(block.Text); text != "" {
					lines = append(lines, speaker+": "+oneLine(text))
				}
			case "tool_use":
				if name := strings.TrimSpace(block.Name); name != "" {
					tools = append(tools, name)
				}
			}
		}
		if len(tools) > 0 {
			lines = append(lines, "coder ran "+strings.Join(tools, ", "))
		}
	}
	return spendBudget(lines, lineRunes, budget)
}

// spendBudget spends the budget asymmetrically on the rendered lines. The
// newest line is the one a check judges against, so it gets the whole budget
// first and is cut only when it alone exceeds it; the older lines keep the
// per-line cut and share what is left, newest first, and whatever no longer
// fits falls off the top, oldest first. A budget of zero or less keeps every
// line whole.
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
		line := truncate(lines[i], lineRunes)
		cost := len([]rune(line)) + 1
		if cost > budget {
			break
		}
		kept = append(kept, line)
		budget -= cost
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return strings.Join(kept, "\n")
}

// blocks reads a message's content, which is either plain text or a list of
// blocks.
func blocks(entry transcriptLine) []contentBlock {
	raw := entry.Message.Content
	if len(raw) == 0 {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []contentBlock{{Type: "text", Text: text}}
	}
	var list []contentBlock
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil
	}
	return list
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
