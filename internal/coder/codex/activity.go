package codex

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
)

// activityTailBytes is how much of a rollout an activity reading reads. A
// rollout repeats the model's whole setting with every turn and keeps every
// tool output, so a long session runs to many megabytes; the tail holds the
// last turns of any of them, and a session that ran for days costs a reading
// the same as a fresh one.
const activityTailBytes = 512 << 10

// SessionActivity reports what a session last did and whether its turn is
// over, read from the session's rollout.
func (p *Coder) SessionActivity(sessionID string, entries, budget int) (coder.Activity, error) {
	return p.sessions.activity(sessionID, entries, budget)
}

// SessionActivityStamp answers when a session's rollout last moved, so the
// record watcher only reads one that did.
func (p *Coder) SessionActivityStamp(sessionID string) (time.Time, error) {
	path, err := p.sessions.rollout(sessionID)
	if err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// BellSignal implements coder.BellReader. codex rings for a turn that ended
// and for a question it asks its person, and the rollout tells them apart: a
// question stands behind a tool call that has no output yet, while a turn's
// last record before its end is the answer. The end itself may not be
// written yet when the bell rings, so an open turn alone says nothing.
func (p *Coder) BellSignal(sessionID string) coder.TurnSignal {
	activity, err := p.sessions.activity(sessionID, 1, 1)
	if err == nil && activity.InToolCall {
		return coder.TurnSignalWaiting
	}
	return coder.TurnSignalOver
}

func (r *sessionRepository) activity(sessionID string, entries, budget int) (coder.Activity, error) {
	path, err := r.rollout(sessionID)
	if err != nil {
		return coder.Activity{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return coder.Activity{}, coder.NoRecordYet("This session has no record yet.")
	}
	defer f.Close()

	// Only the end of the rollout is read, and the first line after the seek
	// is dropped, it is a fragment.
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

// readActivity renders the recorded turns, keeping the last entries lines
// and spending at most budget runes, see coder.ActivityBounds.
//
// A turn is open from its start to its end, and what happens inside one
// proves it open where the tail no longer holds its start. An item that
// arrives for a turn that already ended opens nothing: a command an
// interrupted turn left running reports its end long after. A reading in
// which no turn speaks is empty, see coder.Activity.Empty.
func readActivity(source io.Reader, entries, budget int) (coder.Activity, error) {
	keep, line := coder.ActivityBounds(entries, budget)
	var lines, tools []string
	say := func(text string) {
		lines = append(lines, text)
		if len(lines) > keep {
			lines = lines[len(lines)-keep:]
		}
	}
	flushTools := func() {
		if len(tools) > 0 {
			say("coder ran " + strings.Join(tools, ", "))
			tools = nil
		}
	}
	finished, empty := true, true
	var spokeAt time.Time
	ended := map[string]bool{}
	calls := map[string]bool{}

	err := eachRecord(source, func(record recordLine, at time.Time) {
		switch record.Type {
		case recordResponse:
			call, output := toolCall(record.Payload.Type)
			switch {
			case call && !finished:
				calls[record.Payload.CallID] = true
			case output:
				delete(calls, record.Payload.CallID)
			}
			return
		case recordEvent:
		default:
			return
		}
		switch record.Payload.Type {
		case eventTurnStarted:
			finished = false
			clear(calls)
		case eventTurnComplete, eventTurnAborted:
			flushTools()
			finished = true
			ended[record.Payload.TurnID] = true
			clear(calls)
		case eventItem:
			if ended[record.Payload.TurnID] {
				return
			}
			var item recordItem
			if json.Unmarshal(record.Payload.Item, &item) != nil {
				return
			}
			switch item.Type {
			case itemUser:
				flushTools()
				if text := item.words(); text != "" {
					say("user: " + coder.OneLine(text))
				}
			case itemCoder:
				flushTools()
				if text := item.words(); text != "" {
					say("coder: " + coder.OneLine(text))
				}
			default:
				name, _ := item.tool("")
				if name == "" {
					return
				}
				tools = append(tools, name)
			}
			finished = false
		default:
			return
		}
		empty = false
		spokeAt = at
	})
	if err != nil {
		return coder.Activity{}, err
	}
	flushTools()
	return coder.Activity{
		Text:          coder.SpendActivityBudget(lines, line, budget),
		Finished:      finished,
		InToolCall:    !finished && len(calls) > 0,
		LastMessageAt: spokeAt,
		Empty:         empty,
	}, nil
}
