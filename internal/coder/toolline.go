package coder

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// A tool call in the copy view is one line that names the tool and what it was
// about. The three coders call their tools by different names and spell the
// arguments differently, claude's Bash takes a command, copilot's view a
// path, opencode's read a filePath, so the one table below knows every
// spelling and each coder hands over the name and the arguments as its
// record has them.

// toolLineFields is the input field that says what a call of the tool is
// about, keyed by the tool's name in lower case. An empty field stands for
// the file's path in whichever spelling the record has. A tool that is not
// listed is read as a patch where it carries one, else by its name alone.
var toolLineFields = map[string]string{
	"bash":          "command",
	"shell":         "command",
	"read":          "",
	"view":          "path",
	"edit":          "",
	"multiedit":     "",
	"write":         "",
	"create":        "path",
	"str_replace":   "path",
	"notebookedit":  "notebook_path",
	"grep":          "pattern",
	"rg":            "pattern",
	"glob":          "pattern",
	"agent":         "description",
	"task":          "description",
	"webfetch":      "url",
	"web_fetch":     "url",
	"fetch":         "url",
	"websearch":     "query",
	"web_search":    "query",
	"skill":         "skill",
	"report_intent": "intent",
}

// filePathFields are the spellings of a file's path, claude's and
// opencode's, read in that order for the tools that take a file.
var filePathFields = []string{"file_path", "filePath", "path"}

// patchFields carry a patch whose first file line names what it touches:
// opencode's patchText, and copilot's apply_patch, whose whole argument is
// the patch as a string.
var patchFields = []string{"patchText", "patch", "input"}

// ToolLineFields answers every argument field a tool line may read, for a
// reader that has to name the fields it takes out of its record.
func ToolLineFields() []string {
	seen := map[string]bool{}
	var out []string
	add := func(field string) {
		if field != "" && !seen[field] {
			seen[field] = true
			out = append(out, field)
		}
	}
	for _, field := range toolLineFields {
		add(field)
	}
	for _, field := range filePathFields {
		add(field)
	}
	for _, field := range patchFields {
		add(field)
	}
	sort.Strings(out)
	return out
}

// TodosField is the argument of the todo tool, the list it writes. A reader
// that cannot hand the list over whole, a projection that cuts every field,
// hands over its length instead, a number where the list stood.
const TodosField = "todos"

// todoLine counts the items the todo tool wrote, from the list or from its
// length.
func todoLine(todos json.RawMessage) string {
	var length *int
	var list []json.RawMessage
	count := 0
	switch {
	case json.Unmarshal(todos, &length) == nil && length != nil:
		count = *length
	case json.Unmarshal(todos, &list) == nil && list != nil:
		count = len(list)
	default:
		return ""
	}
	if count == 1 {
		return "1 item"
	}
	return fmt.Sprintf("%d items", count)
}

// ToolLineRunes bounds the line, a command can be a whole script.
const ToolLineRunes = 200

var patchFile = regexp.MustCompile(`(?m)^\*\*\* (?:Add|Update|Delete) File: (.+)$`)

// ToolLine answers the line that says what a call was about, from the
// tool's name and its arguments as the record holds them: an object of
// fields, or for a patch tool the patch itself. Empty when nothing says more
// than the name.
func ToolLine(name string, input json.RawMessage) string {
	key := strings.ToLower(name)
	var patch string
	if json.Unmarshal(input, &patch) == nil {
		return patchLine(patch)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return ""
	}
	if key == "todowrite" {
		return todoLine(fields[TodosField])
	}
	field, listed := toolLineFields[key]
	candidates := []string{field}
	switch {
	case listed && field == "":
		candidates = filePathFields
	case !listed:
		candidates = patchFields
	}
	for _, candidate := range candidates {
		var value string
		if json.Unmarshal(fields[candidate], &value) != nil || strings.TrimSpace(value) == "" {
			continue
		}
		if !listed {
			return patchLine(value)
		}
		return FirstLine(value, ToolLineRunes)
	}
	return ""
}

// patchLine names the first file a patch touches. It names no count: a
// reader may hand over the head of a long patch only.
func patchLine(patch string) string {
	file := patchFile.FindStringSubmatch(patch)
	if file == nil {
		return ""
	}
	return FirstLine(file[1], ToolLineRunes)
}

// FirstLine answers the first line of a text, trimmed and cut to max runes.
func FirstLine(text string, max int) string {
	line := strings.TrimSpace(text)
	if cut := strings.IndexByte(line, '\n'); cut >= 0 {
		line = strings.TrimSpace(line[:cut])
	}
	runes := []rune(line)
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return line
}
