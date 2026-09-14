package claude

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/marein/dev-cockpit/internal/coder"
)

// SessionTranscript hands over what was said in a session, whole, for somebody
// to read and copy. It reads the same record the activity reading reads and
// keeps the same two rules about what counts as conversation: a sidechain
// belongs to a subagent, and an entry that is not a user or an assistant entry
// is bookkeeping. What it does differently is that it flattens nothing: a
// message arrives with its own line breaks, its own code blocks and its own
// table, because that is the whole point of copying it.
func (p *Coder) SessionTranscript(sessionID string, messages, cap int) (coder.Recording, error) {
	return p.sessions.transcript(sessionID, messages, cap)
}

func (r *sessionRepository) transcript(sessionID string, messages, cap int) (coder.Recording, error) {
	path, err := r.transcriptFile(sessionID)
	if err != nil {
		return coder.Recording{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return coder.Recording{}, err
	}
	defer func() { _ = file.Close() }()
	return readTranscript(file, messages, cap)
}

func readTranscript(source io.Reader, messages, cap int) (coder.Recording, error) {
	var entries []transcriptLine
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64*1024), maxTranscriptLine)
	scanner.Split(newTranscriptSplit())
	for scanner.Scan() {
		var parsed transcriptLine
		if err := json.Unmarshal(scanner.Bytes(), &parsed); err != nil {
			continue
		}
		if parsed.IsSidechain || (parsed.Type != "user" && parsed.Type != "assistant") {
			continue
		}
		entries = append(entries, parsed)
	}
	if err := scanner.Err(); err != nil {
		return coder.Recording{}, err
	}
	return renderFullTranscript(entries, messages, cap), nil
}

// renderFullTranscript writes one block per message, oldest first, the way the
// conversation happened. A tool call leaves nothing behind: this reading is
// what somebody copies an answer out of, and that a tool ran is the coder's
// bookkeeping, not a sentence anybody came here to take with them. The activity
// reading names the tools, it answers the other question.
func renderFullTranscript(entries []transcriptLine, messages, cap int) coder.Recording {
	var blocksOut []string
	for _, entry := range entries {
		speaker := "user"
		if entry.Type == "assistant" {
			speaker = "coder"
		}
		for _, block := range blocks(entry) {
			if block.Type != "text" {
				continue
			}
			if text := strings.TrimSpace(block.Text); text != "" {
				blocksOut = append(blocksOut, speaker+":\n"+text)
			}
		}
	}
	return keepNewest(blocksOut, messages, cap)
}

// keepNewest keeps the newest messages whole and drops the oldest ones off the
// top, because a message cut in the middle is not worth copying. What fell off
// is said in a line of its own: a reader has to know the conversation starts
// earlier than what they see, and a bare ellipsis says nothing.
func keepNewest(blocksOut []string, messages, cap int) coder.Recording {
	if len(blocksOut) == 0 {
		return coder.Recording{}
	}
	var kept []string
	for i := len(blocksOut) - 1; i >= 0; i-- {
		if messages > 0 && len(kept) >= messages {
			break
		}
		cost := len([]rune(blocksOut[i])) + 2
		if cap > 0 && cost > cap && len(kept) > 0 {
			break
		}
		kept = append(kept, blocksOut[i])
		cap -= cost
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	dropped := len(blocksOut) - len(kept)
	if dropped > 0 {
		head := strconv.Itoa(dropped) + " earlier messages are not shown"
		kept = append([]string{head}, kept...)
	}
	return coder.Recording{Text: strings.Join(kept, "\n\n"), Dropped: dropped}
}
