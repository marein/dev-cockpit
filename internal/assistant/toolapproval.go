package assistant

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/approval"
)

// A tool call a coder would stop on with a permission dialog cannot ask in a
// turn: nobody sits at it. A coder whose CLI lets a tool of an MCP server
// answer such a question in a turn starts ToolApprovalServer for it, the
// cockpit's own command behind the turn's wrapper, which puts the question
// before the user as an approval and answers the decision. The wrapper carries
// the cockpit's directories and the assistant's id, so the question is asked
// as the assistant whose turn it is.
const (
	// ToolApprovalServerName and ToolApprovalToolName are the MCP server and
	// its one tool, as a coder's configuration names them.
	ToolApprovalServerName = "dev-cockpit"
	ToolApprovalToolName   = "approve"
	// ToolApprovalCommand is the command of the wrapper that serves it.
	ToolApprovalCommand = "tool-approval-serve"
)

// ToolApprovalWait is how long a turn's tool call waits for the user before it
// is denied. The turn holds its run, and a check its slot and deadline, the
// whole time, so it is far shorter than the wait of an approval that runs
// later on its own.
const ToolApprovalWait = 5 * time.Minute

// ToolApprovalServer is the argv of the MCP server a turn in workdir asks its
// tool approvals through.
func ToolApprovalServer(workdir string) []string {
	return []string{wrapperIn(workdir), ToolApprovalCommand}
}

// ToolField is one value of a tool call's input the way its question shows
// it, under its key, and Cut when that is only its start. A field without a
// key is the whole input in one.
type ToolField struct {
	Key   string `json:"key,omitempty"`
	Value string `json:"value"`
	Cut   bool   `json:"cut,omitempty"`
}

// An input splits into at most maxToolFields keys of the names a schema gives
// them, showing at most ToolInputShown together, room for a few values at the
// dialog's bound.
const (
	maxToolFields  = 32
	maxToolKey     = 64
	ToolInputShown = 4 * approval.MaxDetailBytes
)

var toolKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

func toolKey(key string) bool {
	return len(key) <= maxToolKey && toolKeyPattern.MatchString(key)
}

// ToolApprovalBody bounds the request a tool call is asked with. JSON writes a
// byte of text in at most six (\u001f), so it holds the fields at their bounds
// with room for the tool's name and the call's id.
const ToolApprovalBody = 6*(ToolInputShown+maxToolFields*maxToolKey) + 64<<10

// ToolFields is what the question of a tool call shows of its input: each key
// of it on its own, a string as its text with its own line breaks, any other
// value as indented JSON, each cut where the dialog cuts a value. An input
// that does not split that way shows whole as indented JSON. Nothing of it is
// escaped, the dialog shows it as text.
func ToolFields(input json.RawMessage) []ToolField {
	if fields, ok := toolInputFields(input); ok {
		return fields
	}
	value, cut := shownText(indentedJSON(input))
	return []ToolField{{Value: value, Cut: cut}}
}

func toolInputFields(input json.RawMessage) ([]ToolField, bool) {
	dec := json.NewDecoder(bytes.NewReader(input))
	if open, err := dec.Token(); err != nil || open != json.Delim('{') {
		return nil, false
	}
	fields := []ToolField{}
	shown := 0
	for dec.More() {
		token, err := dec.Token()
		key, _ := token.(string)
		if err != nil || len(fields) == maxToolFields || !toolKey(key) ||
			slices.ContainsFunc(fields, func(f ToolField) bool { return f.Key == key }) {
			return nil, false
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, false
		}
		text, ok := toolValue(raw)
		if !ok {
			return nil, false
		}
		value, cut := shownText(text)
		if shown += len(value); shown > ToolInputShown {
			return nil, false
		}
		fields = append(fields, ToolField{Key: key, Value: value, Cut: cut})
	}
	return fields, true
}

// shownText is text cut the way the dialog cuts a value, in valid UTF-8, so
// its length is the same once the server read it.
func shownText(text string) (string, bool) {
	return approval.ShownValue(strings.ToValidUTF8(text, "\uFFFD"))
}

func toolValue(raw json.RawMessage) (string, bool) {
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) {
		return indentedJSON(raw), true
	}
	var text string
	return text, json.Unmarshal(raw, &text) == nil
}

func indentedJSON(raw json.RawMessage) string {
	var out bytes.Buffer
	if json.Indent(&out, bytes.TrimSpace(raw), "", "  ") != nil {
		return string(raw)
	}
	return out.String()
}

// CheckToolFields answers why fields are not what ToolFields makes of an input.
func CheckToolFields(fields []ToolField) error {
	if len(fields) > maxToolFields {
		return errors.New("The tool call's input has more keys than its question shows.")
	}
	shown := 0
	for _, f := range fields {
		if f.Key == "" && len(fields) > 1 || f.Key != "" && !toolKey(f.Key) {
			return errors.New("A key of the tool call's input is not one a schema gives.")
		}
		if len(f.Value) > approval.MaxDetailBytes {
			return errors.New("A value of the tool call's input is longer than its question shows.")
		}
		shown += len(f.Value)
	}
	if shown > ToolInputShown {
		return errors.New("The tool call's input is longer than its question shows.")
	}
	return nil
}
