package copilot

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
// to read and copy. It reads the same event log the activity reading reads, but
// takes only the messages out of it, and it flattens nothing: a message arrives
// with its own line breaks, its own code blocks and its own table, because that
// is the point of copying it. That a tool ran is the coder's bookkeeping and
// stays out; the activity reading names the tools, it answers the other
// question.
//
// Unlike the activity reading this one starts at the beginning of the log. The
// activity reading only ever wants the end and seeks there; here the reader
// asked for a number of messages, and which ones those are is only known after
// the whole log has been walked.
func (p *Coder) SessionTranscript(sessionID string, messages, cap int) (coder.Recording, error) {
	return p.sessions.transcript(sessionID, messages, cap)
}

func (r *sessionRepository) transcript(sessionID string, messages, cap int) (coder.Recording, error) {
	path, err := r.eventsFile(sessionID)
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
	var blocksOut []string
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64*1024), maxTranscriptLine)
	for scanner.Scan() {
		var event eventLine
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		switch event.Type {
		case "user.message":
			if text := strings.TrimSpace(event.Data.Content); text != "" {
				blocksOut = append(blocksOut, "user:\n"+text)
			}
		case "assistant.message":
			if text := strings.TrimSpace(event.Data.Content); text != "" {
				blocksOut = append(blocksOut, "coder:\n"+text)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return coder.Recording{}, err
	}
	return keepNewest(blocksOut, messages, cap), nil
}

// maxTranscriptLine is the longest single event this reading will take. One
// event carries one message, and a message can be a file somebody pasted.
const maxTranscriptLine = 1 << 20

// keepNewest keeps the newest messages whole and drops the oldest ones off the
// top, because a message cut in the middle is not worth copying. What fell off
// is said in a line of its own: a reader has to know the conversation starts
// earlier than what they see, and a bare ellipsis says nothing. It is the same
// rule the claude reader applies, for the same reason.
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
