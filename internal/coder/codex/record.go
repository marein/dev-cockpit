package codex

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
)

// A rollout is codex's own account of a session, one JSON line per record
// (verified on 0.172.0). Three kinds of line speak of the conversation:
//
// A turn stands between an event task_started and an event task_complete, or
// turn_aborted once somebody interrupted it, each under the turn's id.
//
// An event item_completed carries what happened inside it, under the same
// id: the user's words as typed (UserMessage), the coder's words
// (AgentMessage) and each tool call once it is over, a command
// (CommandExecution), a patch (FileChange) and the rest. The response items
// beside them repeat the same words wrapped in what the model was sent, the
// instruction files among them, so the words are read from the events alone.
//
// A tool call in flight is the one thing only a response item shows: the
// call stands there from the moment the model made it, its output follows
// when it is over. A call codex asks its person about waits in between.
//
// Everything else, the settings, the token counts, the world the model was
// shown, is bookkeeping.

// recordLineLimit is the longest line a reading takes. A longer one, an
// image somebody pasted, is cut and so dropped as a line that does not parse.
const recordLineLimit = 1 << 20

type recordLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type   string          `json:"type"`
		TurnID string          `json:"turn_id"`
		CWD    string          `json:"cwd"`
		CallID string          `json:"call_id"`
		Item   json.RawMessage `json:"item"`
	} `json:"payload"`
}

type recordItem struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Command []string                   `json:"command"`
	Changes map[string]json.RawMessage `json:"changes"`
	Tool    string                     `json:"tool"`
	Query   string                     `json:"query"`
}

const (
	recordEvent     = "event_msg"
	recordResponse  = "response_item"
	recordCompacted = "compacted"

	eventTurnStarted  = "task_started"
	eventTurnComplete = "task_complete"
	eventTurnAborted  = "turn_aborted"
	eventItem         = "item_completed"

	itemUser    = "UserMessage"
	itemCoder   = "AgentMessage"
	itemCommand = "CommandExecution"
	itemPatch   = "FileChange"
	itemSearch  = "WebSearch"
)

// toolCall reads the kind of a response item: a tool call the model made, or
// the output that closes one. codex writes function_call_output behind
// function_call, and so on for every kind of call.
func toolCall(kind string) (call, output bool) {
	if base, ok := strings.CutSuffix(kind, "_output"); ok {
		return false, strings.HasSuffix(base, "_call")
	}
	return strings.HasSuffix(kind, "_call"), false
}

// words answers the text of a message item, its text parts joined. An image
// or a mention beside them is no word.
func (i recordItem) words() string {
	var parts []string
	for _, c := range i.Content {
		if !strings.EqualFold(c.Type, "text") {
			continue
		}
		if text := strings.TrimSpace(c.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// tool answers the name a tool call item goes by and the line that says what
// it was about, the name empty for an item that is no tool call. codex runs
// a command through a login shell, the command itself is its last argument.
func (i recordItem) tool(cwd string) (name, line string) {
	switch {
	case i.Type == itemCommand:
		command := strings.Join(i.Command, " ")
		if n := len(i.Command); n >= 3 && strings.HasPrefix(i.Command[n-2], "-") {
			command = i.Command[n-1]
		}
		return "shell", coder.FirstLine(command, coder.ToolLineRunes)
	case i.Type == itemPatch:
		file := ""
		for path := range i.Changes {
			if file == "" || path < file {
				file = path
			}
		}
		if rel, err := filepath.Rel(cwd, file); cwd != "" && err == nil && filepath.IsLocal(rel) {
			file = rel
		}
		return "apply_patch", coder.FirstLine(file, coder.ToolLineRunes)
	case i.Type == itemSearch:
		return "web_search", coder.FirstLine(i.Query, coder.ToolLineRunes)
	case strings.HasSuffix(i.Type, "ToolCall"):
		if tool := strings.TrimSpace(i.Tool); tool != "" {
			return coder.FirstLine(tool, coder.ToolLineRunes), ""
		}
		return strings.TrimSuffix(i.Type, "ToolCall"), ""
	}
	return "", ""
}

// eachRecord feeds every line of a rollout that parses to visit.
func eachRecord(source io.Reader, visit func(line recordLine, at time.Time)) error {
	reader := bufio.NewReaderSize(source, 64<<10)
	for {
		raw, err := readLine(reader, recordLineLimit)
		if len(raw) > 0 {
			var line recordLine
			if json.Unmarshal(raw, &line) == nil {
				at, _ := coder.ParseTimestamp(line.Timestamp)
				visit(line, at)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// rollout locates the record of a session. The id comes from outside, so it
// has to be one codex mints before it names a file. A found path is kept: the
// record watcher asks every second for every running session, and the file
// of a session stays where codex put it.
func (r *sessionRepository) rollout(sessionID string) (string, error) {
	id, err := validSessionID(sessionID)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	path, known := r.rollouts[id]
	r.mu.Unlock()
	if known {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	matches, _ := filepath.Glob(filepath.Join(r.home, sessionsDir, "*", "*", "*", "rollout-*-"+r.nativeID(id)+".jsonl"))
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(matches) == 0 {
		delete(r.rollouts, id)
		return "", coder.NoRecordYet("This session has no record yet.")
	}
	if r.rollouts == nil {
		r.rollouts = map[string]string{}
	}
	r.rollouts[id] = matches[0]
	return matches[0], nil
}
