package copilot

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"

	"github.com/marein/dev-cockpit/internal/coder"
)

// maxTranscriptLine is the longest single event this reading will take. One
// event carries one message, and a message can be a file somebody pasted.
const maxTranscriptLine = 1 << 20

// SessionConversation hands over what was said in a session with its
// structure standing, for the copy view that renders it as bubbles. It walks
// the same event log the activity reading walks, from the top on every call:
// the messages are the conversation, and the deltas, assets and checkpoints
// around them are bookkeeping. A tool call is a line
// of the turn it ran in that names the tool and what it was about, read from
// the arguments its start event carries.
func (p *Coder) SessionConversation(sessionID string) ([]coder.Message, error) {
	path, err := p.sessions.eventsFile(sessionID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var b conversationBuilder
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxTranscriptLine)
	for scanner.Scan() {
		b.Feed(scanner.Bytes())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return b.Messages(), nil
}

// conversationEvent is what the conversation reading takes out of an event:
// its id and time, the words of a message, and a tool call's name and
// arguments.
type conversationEvent struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Data      struct {
		Content   string          `json:"content"`
		ToolName  string          `json:"toolName"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"data"`
}

// conversationBuilder builds the messages, oldest first, one event at a
// time. A turn is one coder message from the user's words to the next ones,
// so the answer and the tools it ran stand together. copilot ends a model
// round with assistant.turn_end and starts the next one with its own
// turn_start inside the same interaction, one round per tool call, so those
// two say nothing about where the turn ends; the user's next words do, and so
// do an abort and the session's end.
type conversationBuilder struct {
	coder.TurnBuilder
}

func (b *conversationBuilder) Feed(line []byte) {
	var event conversationEvent
	if err := json.Unmarshal(line, &event); err != nil {
		return
	}
	at, _ := coder.ParseTimestamp(event.Timestamp)
	switch event.Type {
	case "user.message":
		b.CloseTurn()
		if text := strings.TrimSpace(event.Data.Content); text != "" {
			b.Add(coder.Message{Role: coder.RoleUser, Parts: []coder.Part{{Kind: coder.PartText, Text: text}}}, event.ID, at)
		}
	case "assistant.message":
		if text := strings.TrimSpace(event.Data.Content); text != "" {
			b.AddPart(event.ID, at, coder.Part{Kind: coder.PartText, Text: text})
		}
	case "tool.user_requested":
		// A `!` shell escape: the user ran the command, the tool is how
		// copilot records it, so it stands as the user's command chip.
		if command := escapeCommand(event.Data.Arguments); command != "" {
			b.Add(coder.Message{Role: coder.RoleUser, Kind: coder.KindCommand, Parts: []coder.Part{{Kind: coder.PartText, Text: "! " + command}}}, event.ID, at)
		}
	case "tool.execution_start":
		if name := strings.TrimSpace(event.Data.ToolName); name != "" {
			b.AddPart(event.ID, at, coder.Part{Kind: coder.PartTool, Tool: name, Text: coder.ToolLine(name, event.Data.Arguments)})
		}
	case "abort", "session.shutdown":
		b.CloseTurn()
	}
}

// escapeCommand reads the command a shell escape ran out of its arguments.
func escapeCommand(arguments json.RawMessage) string {
	var args struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(arguments, &args) != nil {
		return ""
	}
	return strings.TrimSpace(args.Command)
}
