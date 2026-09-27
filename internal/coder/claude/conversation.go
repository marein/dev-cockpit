package claude

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
)

// SessionConversation hands over what was said in a session with its
// structure standing, for the copy view that renders it as bubbles. It reads
// the same record the activity reading reads, from the top on every call, and
// keeps its rules about what counts as conversation: a sidechain belongs to a
// subagent, and an entry that is not a user or an assistant entry is
// bookkeeping. A coder's turn is one message: claude records a turn as a run of
// assistant entries, one per text or tool call, with the tool results between
// them in the user's role, and a reader wants the turn, not the run. A tool
// call stays as one line that names the tool and what it was about.
func (p *Coder) SessionConversation(sessionID string) ([]coder.Message, error) {
	path, err := p.sessions.transcriptFile(sessionID)
	var missing sessionNotFound
	if errors.As(err, &missing) {
		return nil, coder.NoRecordYet(err.Error())
	}
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
	scanner.Split(newTranscriptSplit())
	for scanner.Scan() {
		b.Feed(scanner.Bytes())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return b.Messages(), nil
}

// askTool is the tool claude asks its user through. Its line is the question
// it asked.
const askTool = "AskUserQuestion"

// conversationBuilder turns recorded lines into messages, one line at a
// time, oldest first. A user entry that holds nothing but tool results
// answers calls made earlier and is no message of its own; the coder's turn
// stays open across it, so the entries after it join the same message. A
// user entry with words closes the turn.
type conversationBuilder struct {
	coder.TurnBuilder
}

func (b *conversationBuilder) Feed(line []byte) {
	var entry transcriptLine
	if err := json.Unmarshal(line, &entry); err != nil {
		return
	}
	if entry.IsSidechain {
		return
	}
	switch entry.Type {
	case "user":
		b.feedUser(entry)
	case "assistant":
		b.feedAssistant(entry)
	}
}

func (b *conversationBuilder) feedUser(entry transcriptLine) {
	content := blocks(entry)
	if results(content) {
		return
	}
	if message, ok := userMessage(entry, joinText(content)); ok {
		b.Add(message, entry.UUID, timeOf(entry))
	}
}

func (b *conversationBuilder) feedAssistant(entry transcriptLine) {
	for _, block := range blocks(entry) {
		switch block.Type {
		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				b.AddPart(entry.UUID, timeOf(entry), coder.Part{Kind: coder.PartText, Text: text})
			}
		case "tool_use":
			if name := strings.TrimSpace(block.Name); name != "" {
				b.AddPart(entry.UUID, timeOf(entry), coder.Part{Kind: coder.PartTool, Tool: name, Text: toolLine(name, block.Input)})
			}
		}
	}
}

// userMessage decides what a user entry is, if anything. The record keeps
// a lot in the user's role that nobody typed: the CLI's own notes, the
// expansion of a command, the output of a local one, the markers of an
// attachment. Only the words the person typed become a message; a command
// becomes a chip; the rest, the interrupt marker and the compaction summary
// included, becomes nothing.
func userMessage(entry transcriptLine, text string) (coder.Message, bool) {
	trimmed := strings.TrimSpace(text)
	switch {
	case entry.IsCompactSummary, strings.HasPrefix(trimmed, "[Request interrupted by user"):
		return coder.Message{}, false
	case entry.IsMeta:
		// The continuation after a compaction, an expanded skill prompt,
		// the size marker of an attached image: written by the CLI.
		return coder.Message{}, false
	}
	if name, args, ok := commandLine(trimmed); ok {
		return coder.Message{Role: coder.RoleUser, Kind: coder.KindCommand, Parts: []coder.Part{{Kind: coder.PartText, Text: strings.TrimSpace(name + " " + args)}}}, true
	}
	if command := betweenTags(trimmed, "bash-input"); command != "" {
		return coder.Message{Role: coder.RoleUser, Kind: coder.KindCommand, Parts: []coder.Part{{Kind: coder.PartText, Text: "! " + command}}}, true
	}
	words := cleanUserText(trimmed)
	if words == "" {
		return coder.Message{}, false
	}
	return coder.Message{Role: coder.RoleUser, Parts: []coder.Part{{Kind: coder.PartText, Text: words}}}, true
}

// The tags the CLI writes into the user's role. A block is stripped wherever
// it stands, an unclosed one to the end of the text.
var (
	strippedBlocks = blockPatterns("system-reminder", "ide_selection", "ide_opened_file", "task-notification", "local-command-caveat", "local-command-stdout", "local-command-stderr", "bash-stdout", "bash-stderr")
	pastedTags     = regexp.MustCompile(`</?pasted_content\b[^>]*>`)
	imageMarkers   = regexp.MustCompile(`[ \t]*\[Image(?: #\d+|: [^\]]*)\][ \t]*`)
	blankRuns      = regexp.MustCompile(`\n{3,}`)
	commandTag     = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	commandArgsTag = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
)

// cleanUserText keeps what the person typed: the CLI's blocks go wherever
// they stand, the wrapper around a paste goes and the paste stays, the
// marker of an attached image goes.
func cleanUserText(text string) string {
	for _, block := range strippedBlocks {
		text = block.ReplaceAllString(text, "")
	}
	text = pastedTags.ReplaceAllString(text, "")
	text = imageMarkers.ReplaceAllString(text, " ")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	text = strings.Join(lines, "\n")
	text = blankRuns.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// blockPatterns compiles one pattern per tag, each closed by its own end
// tag, so a tag standing inside a block never ends the block early.
func blockPatterns(tags ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(tags))
	for _, tag := range tags {
		out = append(out, regexp.MustCompile(`(?s)<`+tag+`\b[^>]*>.*?(</`+tag+`>|\z)`))
	}
	return out
}

// commandLine reads a slash command entry, the tags claude writes when the
// user runs one: the name and, when given, the arguments.
func commandLine(text string) (name, args string, ok bool) {
	m := commandTag.FindStringSubmatch(text)
	if m == nil {
		return "", "", false
	}
	name = strings.TrimSpace(m[1])
	if a := commandArgsTag.FindStringSubmatch(text); a != nil {
		args = strings.TrimSpace(a[1])
	}
	return name, args, name != ""
}

// results says an entry holds tool results and nothing else.
func results(content []contentBlock) bool {
	if len(content) == 0 {
		return false
	}
	for _, block := range content {
		if block.Type != "tool_result" {
			return false
		}
	}
	return true
}

func joinText(content []contentBlock) string {
	var parts []string
	for _, block := range content {
		if block.Type != "text" {
			continue
		}
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// timeOf dates an entry, zero when it carries no readable timestamp.
func timeOf(entry transcriptLine) time.Time {
	at, _ := coder.ParseTimestamp(entry.Timestamp)
	return at
}

// toolLine says what a call was about: the question for the question tool,
// the table's line for every other one.
func toolLine(name string, input json.RawMessage) string {
	if name != askTool {
		return coder.ToolLine(name, input)
	}
	var asked struct {
		Questions []struct {
			Question string `json:"question"`
		} `json:"questions"`
	}
	if json.Unmarshal(input, &asked) != nil || len(asked.Questions) == 0 {
		return ""
	}
	return coder.FirstLine(asked.Questions[0].Question, coder.ToolLineRunes)
}
