package docker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// The compose buttons are configuration, not code. What used to be two wired in
// commands is a list somebody edits: every entry carries an icon, a label, the
// command line it wraps, how long it may take and whether it asks before it
// starts. The list is one JSON value in the flat settings store, and its order
// is the order of the buttons.
//
// That key has three states, not two. Not set at all means the defaults below,
// which is also why nothing writes them at first start: a later version may
// improve the list, and a copy of today's one sitting in everybody's settings
// would freeze it forever. Set and empty means somebody took every button away,
// which is a real answer and stays one. Telling those two apart needs Lookup,
// Get answers "" for both.
const ActionsSettingKey = "docker-compose-actions"

// defaultActionTimeout bounds an entry whose timeout is missing or unreadable.
// A run without any bound is the one thing this must not become: the hold
// process is what enforces it, and a run nobody bounds is a run nobody ends.
const defaultActionTimeout = 10 * time.Minute

// IconNames is the vocabulary an entry's icon comes from. They are our own
// words for what a command does, not the name of a glyph in whatever icon set
// the pages happen to use: which picture a word gets is the render layer's
// table alone, so the set can be swapped without touching a stored setting.
var IconNames = []string{"start", "stop", "restart", "build", "pull", "purge", "command"}

// DefaultIcon is what an entry that names nothing we know gets. A row without a
// picture reads as a broken page, so there is no such thing as no icon.
const DefaultIcon = "command"

// NormalizeIcon answers the name to store for what somebody picked.
func NormalizeIcon(name string) string {
	for _, known := range IconNames {
		if name == known {
			return name
		}
	}
	return DefaultIcon
}

// Action is one configured compose command. Command is a command line, never a
// shell line: it is split into argv here and handed to the program directly,
// see SplitCommand.
type Action struct {
	// ID names the entry in a request. It is stable across edits, so a page
	// rendered before a change still asks for the entry it showed.
	ID string `json:"id"`
	// Icon is one name out of IconNames, what the button and the menu row show.
	Icon string `json:"icon"`
	// Label is the words next to it.
	Label string `json:"label"`
	// Command is the command line, argv separated by spaces, quotes allowed.
	Command string `json:"command"`
	// Timeout is a Go duration ("10m"), enforced by the hold process.
	Timeout string `json:"timeout"`
	// Confirm makes the surfaces ask before the run starts.
	Confirm bool `json:"confirm,omitempty"`
}

// DefaultActions is the list an install that never touched the setting has:
// the two runs the cockpit always had, a build, and the destructive down that
// takes the volumes with it, which is the one that asks first.
func DefaultActions() []Action {
	return []Action{
		{ID: "up", Icon: "start", Label: "Compose up", Command: "docker compose up -d", Timeout: "10m"},
		{ID: "down", Icon: "stop", Label: "Compose down", Command: "docker compose down", Timeout: "5m"},
		{ID: "build", Icon: "build", Label: "Compose build", Command: "docker compose build --pull", Timeout: "30m"},
		{ID: "down-volumes", Icon: "purge", Label: "Compose down with volumes", Command: "docker compose down -v", Timeout: "5m", Confirm: true},
	}
}

// IsDefault reports whether a list is exactly the default one. Saving that is
// the same thing as never having answered, so the caller stores nothing and
// lets the key stay absent, which is what keeps a later version free to
// improve the defaults for this install.
func IsDefault(list []Action) bool {
	return reflect.DeepEqual(list, DefaultActions())
}

// Actions turns what the settings store answered into the list of buttons.
// Pass both return values of Lookup: not set is the default list, set is what
// is stored, empty list included. Something stored that cannot be read at all
// is treated like never set, so a damaged value leaves a working cockpit.
func Actions(raw string, set bool) []Action {
	if !set {
		return DefaultActions()
	}
	list, err := DecodeActions(raw)
	if err != nil {
		return DefaultActions()
	}
	return list
}

// DecodeActions reads the stored JSON. An empty value is an empty list, not an
// error: that is what removing every entry leaves behind.
func DecodeActions(raw string) ([]Action, error) {
	if strings.TrimSpace(raw) == "" {
		return []Action{}, nil
	}
	var list []Action
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []Action{}
	}
	return list, nil
}

// EncodeActions writes the list back the way the store keeps it.
func EncodeActions(list []Action) string {
	if list == nil {
		list = []Action{}
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// ActionByID picks the entry a request names.
func ActionByID(list []Action, id string) (Action, bool) {
	for _, action := range list {
		if action.ID == id {
			return action, true
		}
	}
	return Action{}, false
}

// Duration is how long the entry may run.
func (a Action) Duration() time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(a.Timeout))
	if err != nil || d <= 0 {
		return defaultActionTimeout
	}
	return d
}

// Resolve is the one place a configured entry becomes a run: the argv the
// program is started with and the time it may take. dir is the stack directory
// the command runs in, root the project it belongs to.
//
// A program named relatively (./deploy.sh, ../ops/up.sh) is looked up from the
// stack directory upwards to the project root and handed on absolute, because
// detach.Start resolves the program before it sets the working directory: a
// relative path would be answered against the cockpit's own directory, which is
// somewhere else entirely.
func (a Action) Resolve(dir, root string) ([]string, time.Duration, error) {
	argv, err := SplitCommand(a.Command)
	if err != nil {
		return nil, 0, err
	}
	if len(argv) == 0 {
		return nil, 0, errors.New("the command is empty")
	}
	program, err := programPath(argv[0], dir, root)
	if err != nil {
		return nil, 0, err
	}
	argv[0] = program
	return argv, a.Duration(), nil
}

// SplitCommand splits a configured command line into argv the way a shell
// splits words, and no further: spaces separate, quotes group, a backslash
// takes the next character as it stands. Nothing is expanded, no variable, no
// pattern against the disk, no command inside another, because the line is
// never handed to a shell in the first place. What comes out of here reaches
// the program as arguments and can never become a command of its own.
func SplitCommand(line string) ([]string, error) {
	var (
		argv    []string
		word    strings.Builder
		started bool
		quote   rune
	)
	flush := func() {
		if started {
			argv = append(argv, word.String())
			word.Reset()
			started = false
		}
	}
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
				continue
			}
			word.WriteRune(c)
		case quote == '"':
			if c == '"' {
				quote = 0
				continue
			}
			if c == '\\' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
				i++
				word.WriteRune(runes[i])
				continue
			}
			word.WriteRune(c)
		case c == '\'' || c == '"':
			quote = c
			started = true
		case c == '\\':
			if i+1 >= len(runes) {
				return nil, errors.New("the command ends in a backslash")
			}
			i++
			word.WriteRune(runes[i])
			started = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		default:
			word.WriteRune(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("the command has an unclosed %c quote", quote)
	}
	flush()
	return argv, nil
}

// programPath answers where a command's program really is. Anything the PATH
// or an absolute path can answer is left alone; a relative one is searched for
// from the stack directory up to the project root, so a script at the root
// serves the stacks below it too.
func programPath(program, dir, root string) (string, error) {
	if !strings.HasPrefix(program, "./") && !strings.HasPrefix(program, "../") {
		return program, nil
	}
	if dir == "" {
		return "", fmt.Errorf("%s: no directory to look in", program)
	}
	current := filepath.Clean(dir)
	ceiling := ""
	if root != "" {
		ceiling = filepath.Clean(root)
	}
	for {
		candidate := filepath.Clean(filepath.Join(current, program))
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
		if ceiling == "" || current == ceiling {
			break
		}
		parent := filepath.Dir(current)
		if parent == current || !within(ceiling, parent) {
			break
		}
		current = parent
	}
	return "", fmt.Errorf("%s: no such program between %s and %s", program, dir, root)
}

// within reports whether path lies in root or below it.
func within(root, path string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// PurgeAction is the fixed down a project deletion runs, and the one command
// here that no setting reaches: a deletion takes the volumes with it, and it
// names the compose project explicitly, because compose otherwise derives the
// name from the directory and would quietly clear nothing while reporting
// success.
func PurgeAction(composeProject string) Action {
	command := "docker compose"
	if composeProject != "" {
		command += " -p " + shellQuote(composeProject)
	}
	return Action{
		ID:      "delete-down",
		Icon:    "purge",
		Label:   "Compose down with volumes",
		Command: command + " down -v",
		Timeout: "5m",
	}
}
