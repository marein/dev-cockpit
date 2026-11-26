package codex

import (
	"encoding/json"
	"os"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
)

// SessionConversation hands over what was said in a session with its
// structure standing, for the copy view that renders it as bubbles. It walks
// the rollout the activity reading tails, from the top on every call. A turn
// is one coder message from the user's words to the next ones, the answer
// and the tools it ran standing together in the order codex recorded them: a
// command an interrupted turn left running reports its end after the turn's,
// and still belongs to it. A tool call is the line that names the tool and
// what it was about.
func (p *Coder) SessionConversation(sessionID string) ([]coder.Message, error) {
	path, err := p.sessions.rollout(sessionID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var b coder.TurnBuilder
	cwd := ""
	err = eachRecord(file, func(record recordLine, at time.Time) {
		if record.Type == "session_meta" && cwd == "" {
			cwd = record.Payload.CWD
			return
		}
		if record.Type != recordEvent {
			return
		}
		if record.Payload.Type != eventItem {
			return
		}
		var item recordItem
		if json.Unmarshal(record.Payload.Item, &item) != nil {
			return
		}
		switch item.Type {
		case itemUser:
			b.CloseTurn()
			if text := item.words(); text != "" {
				b.Add(coder.Message{Role: coder.RoleUser, Parts: []coder.Part{{Kind: coder.PartText, Text: text}}}, item.ID, at)
			}
		case itemCoder:
			if text := item.words(); text != "" {
				b.AddPart(item.ID, at, coder.Part{Kind: coder.PartText, Text: text})
			}
		default:
			if name, line := item.tool(cwd); name != "" {
				b.AddPart(item.ID, at, coder.Part{Kind: coder.PartTool, Tool: name, Text: line})
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return b.Messages(), nil
}
