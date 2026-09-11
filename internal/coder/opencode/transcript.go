package opencode

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/marein/dev-cockpit/internal/coder"
)

// SessionTranscript hands over what was said in a session, whole, for somebody
// to read and copy. It asks the same rows the activity reading asks for and
// takes the same parts out of them, the text of a message and the names of the
// tools that ran, but it flattens nothing: a message arrives with its own line
// breaks, its own code blocks and its own table, because that is the point of
// copying it. A synthetic part stays out here too, it is injected bookkeeping
// and not anybody's words.
//
// The query asks for the newest messages, so the bound travels into the
// database rather than being applied after everything came back: a tool part
// carries its whole output in the row, and none of that has any business
// crossing the process boundary.
func (p *Coder) SessionTranscript(sessionID string, messages, cap int) (coder.Recording, error) {
	return p.sessions.transcript(sessionID, messages, cap)
}

func (r *sessionRepository) transcript(sessionID string, messages, cap int) (coder.Recording, error) {
	id, err := validSessionID(sessionID)
	if err != nil {
		return coder.Recording{}, err
	}
	if _, ok := r.dbStamp(); !ok {
		return coder.Recording{}, fmt.Errorf("This session has no record to read.")
	}
	native, err := validSessionID(r.nativeID(id))
	if err != nil {
		return coder.Recording{}, err
	}
	// One more than asked for, so the reading can tell whether anything older
	// exists without walking the whole conversation.
	limit := messages
	if limit > 0 {
		limit++
	} else {
		limit = -1
	}
	out, err := r.query(fmt.Sprintf(activityQuery, native, limit, native))
	if err != nil {
		return coder.Recording{}, fmt.Errorf("This session's record could not be read.")
	}
	var rows []activityRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return coder.Recording{}, fmt.Errorf("This session's record could not be read.")
	}
	if len(rows) == 0 {
		return coder.Recording{}, fmt.Errorf(`No session "%s" was found.`, id)
	}
	return renderFullTranscript(rows, messages, cap), nil
}

func renderFullTranscript(rows []activityRow, messages, cap int) coder.Recording {
	type message struct {
		id     string
		blocks []string
		tools  []string
	}
	var recorded []message
	current := -1
	for _, row := range rows {
		if row.Message == "" {
			continue
		}
		if current < 0 || recorded[current].id != row.Message {
			recorded = append(recorded, message{id: row.Message})
			current = len(recorded) - 1
		}
		speaker := "user"
		if row.Role == "assistant" {
			speaker = "coder"
		}
		switch row.PartType {
		case "text":
			if row.Synthetic == 1 {
				continue
			}
			if text := strings.TrimSpace(row.Text); text != "" {
				recorded[current].blocks = append(recorded[current].blocks, speaker+":\n"+text)
			}
		case "tool":
			if name := strings.TrimSpace(row.Tool); name != "" {
				recorded[current].tools = append(recorded[current].tools, name)
			}
		}
	}
	var blocksOut []string
	for _, m := range recorded {
		blocksOut = append(blocksOut, m.blocks...)
		if len(m.tools) > 0 {
			blocksOut = append(blocksOut, "coder ran "+strings.Join(m.tools, ", "))
		}
	}
	return keepNewest(blocksOut, len(recorded) > messages && messages > 0, cap)
}

// keepNewest keeps the newest messages whole and drops the oldest ones off the
// top when the cap runs out, because a message cut in the middle is not worth
// copying. What fell off is said in a line of its own, the same rule the claude
// and copilot readers apply. older says the database held more than was asked
// for, which is the only way this reader can know: it never saw them.
func keepNewest(blocksOut []string, older bool, cap int) coder.Recording {
	if len(blocksOut) == 0 {
		return coder.Recording{}
	}
	var kept []string
	for i := len(blocksOut) - 1; i >= 0; i-- {
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
	if dropped > 0 || older {
		head := "earlier messages are not shown"
		if dropped > 0 {
			head = strconv.Itoa(dropped) + " " + head
		}
		kept = append([]string{head}, kept...)
	}
	if older && dropped == 0 {
		dropped = 1
	}
	return coder.Recording{Text: strings.Join(kept, "\n\n"), Dropped: dropped}
}
