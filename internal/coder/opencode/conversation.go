package opencode

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
)

// SessionConversation hands over what was said in a session with its
// structure standing, for the copy view that renders it as bubbles. It asks
// the session's rows through the CLI's query on every call, projected down to
// what a bubble shows. A tool part is a line of its turn that names the tool
// and what it was about, read from the arguments the projection carries.
func (p *Coder) SessionConversation(sessionID string) ([]coder.Message, error) {
	return p.sessions.conversation(sessionID)
}

// conversationQuery reads one session's whole conversation, every message
// with its parts. The projection is the bound that matters: a tool part
// carries its whole output in the row, and of its arguments only the fields a
// tool line reads travel, each cut to projectedRunes. The session is the
// anchor, so an unknown session answers no row.
var conversationQuery = `SELECT m.id AS message,` +
	` json_extract(m.data,'$.role') AS role,` +
	` json_extract(p.data,'$.type') AS parttype,` +
	` json_extract(p.data,'$.text') AS text,` +
	` CASE WHEN json_extract(p.data,'$.synthetic') THEN 1 ELSE 0 END AS synthetic,` +
	` json_extract(p.data,'$.tool') AS tool,` +
	` ` + toolInputProjection() + ` AS input,` +
	` m.time_created AS created` +
	` FROM session s` +
	` LEFT JOIN message m ON m.session_id = s.id` +
	` LEFT JOIN part p ON p.message_id = m.id` +
	` WHERE s.id = '%s'` +
	` ORDER BY m.time_created ASC, m.id ASC, p.time_created ASC, p.id ASC`

// projectedRunes bounds every argument a row carries: the line the view
// shows is shorter still, and a write's content or a patch never travels
// whole.
const projectedRunes = 400

// toolInputProjection builds the JSON object of a tool part's arguments that
// a tool line may read (coder.ToolLineFields), each cut to projectedRunes,
// so the table of which field says what stays in one place. The todo list is
// the one argument a cut would break, it travels as its length
// (coder.TodosField).
func toolInputProjection() string {
	var pairs []string
	for _, field := range coder.ToolLineFields() {
		pairs = append(pairs, fmt.Sprintf(`'%s', substr(json_extract(p.data,'$.state.input.%s'),1,%d)`, field, field, projectedRunes))
	}
	pairs = append(pairs, fmt.Sprintf(`'%s', json_array_length(json_extract(p.data,'$.state.input.%s'))`, coder.TodosField, coder.TodosField))
	return "CASE WHEN json_extract(p.data,'$.type') = 'tool' THEN json_object(" + strings.Join(pairs, ", ") + ") END"
}

type conversationRow struct {
	Message   string `json:"message"`
	Role      string `json:"role"`
	PartType  string `json:"parttype"`
	Text      string `json:"text"`
	Synthetic int    `json:"synthetic"`
	Tool      string `json:"tool"`
	// Input is a tool part's arguments as far as a tool line reads them,
	// Created the message's time in milliseconds.
	Input   string `json:"input"`
	Created int64  `json:"created"`
}

func (r *sessionRepository) conversation(sessionID string) ([]coder.Message, error) {
	id, err := validSessionID(sessionID)
	if err != nil {
		return nil, err
	}
	if _, ok := r.dbStamp(); !ok {
		return nil, coder.NoRecordYet("This session has no record to read.")
	}
	native, err := validSessionID(r.nativeID(id))
	if err != nil {
		return nil, err
	}
	out, err := r.query(fmt.Sprintf(conversationQuery, native))
	if err != nil {
		return nil, fmt.Errorf("This session's record could not be read.")
	}
	var rows []conversationRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("This session's record could not be read.")
	}
	if len(rows) == 0 {
		return nil, coder.NoRecordYet(fmt.Sprintf(`No session "%s" was found.`, id))
	}
	return renderConversation(rows), nil
}

// renderConversation builds the messages in row order. opencode stores one
// assistant message per step, one step per tool round, so the rows of one
// turn are several messages; coder.TurnBuilder makes them one, from the
// user's words to the next ones, under the id of its first step. A synthetic
// part is injected bookkeeping and stays out, and a message left with no
// parts is no message, a user message without words closes no turn.
func renderConversation(rows []conversationRow) []coder.Message {
	var b coder.TurnBuilder
	var user *coder.Message
	userID, userAt := "", time.Time{}
	flush := func() {
		if user != nil && len(user.Parts) > 0 {
			b.Add(*user, userID, userAt)
		}
		user = nil
	}
	current := ""
	for _, row := range rows {
		if row.Message == "" {
			continue
		}
		if current != row.Message {
			flush()
			current = row.Message
			if row.Role != "assistant" {
				user = &coder.Message{Role: coder.RoleUser}
				userID, userAt = row.Message, rowStamp(row)
			}
		}
		part, ok := rowPart(row)
		if !ok {
			continue
		}
		if user != nil {
			user.Parts = append(user.Parts, part)
			continue
		}
		b.AddPart(row.Message, rowStamp(row), part)
	}
	flush()
	return b.Messages()
}

// rowPart reads a row as a part of its message: a text that is no
// bookkeeping, or a tool call as the line that says what it was about.
func rowPart(row conversationRow) (coder.Part, bool) {
	switch {
	case row.PartType == "text" && row.Synthetic != 1:
		if text := strings.TrimSpace(row.Text); text != "" {
			return coder.Part{Kind: coder.PartText, Text: text}, true
		}
	case row.PartType == "tool":
		if name := strings.TrimSpace(row.Tool); name != "" {
			return coder.Part{Kind: coder.PartTool, Tool: name, Text: coder.ToolLine(name, json.RawMessage(row.Input))}, true
		}
	}
	return coder.Part{}, false
}

func rowStamp(row conversationRow) time.Time {
	if row.Created <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(row.Created)
}
