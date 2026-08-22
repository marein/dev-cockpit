package render

import (
	"strings"
	"unicode"

	"github.com/marein/dev-cockpit/internal/docker"
)

// DockerRunData feeds the output page of one compose run. The page is not
// watching a process: it reads the file the detached run writes into, which is
// why it answers the same way while the run goes and long after it ended.
type DockerRunData struct {
	Page
	Project string
	// Stack is the project relative directory the command ran in, empty for
	// the project root.
	Stack  string
	Run    docker.RunView
	Status string
	Output string
	// OutputURL is what the page repaints from, StopURL what calls the run
	// off while it is still going.
	OutputURL string
	StopURL   string
}

// Failed reports whether the run has something to complain about, the one
// thing that colors the status line.
func (d DockerRunData) Failed() bool { return d.Run.Failure != "" }

// DockerRunStatus is the one line that says where a run stands: that it is
// going, the exit code it ended with, or what went wrong instead.
func DockerRunStatus(run docker.RunView) string {
	switch {
	case run.Running:
		return "Running"
	case run.Failure != "":
		return upperFirst(run.Failure)
	default:
		return "Exit status 0"
	}
}

func upperFirst(text string) string {
	if text == "" {
		return text
	}
	runes := []rune(text)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// dockerIconClasses is the one table that turns an action's icon name into a
// picture. The stored setting carries our own word for what a command does
// ("start", "purge"), never the name of a glyph, so this table is the only
// place that knows which icon set the pages use and swapping that set changes
// nothing anybody has saved. Every surface resolves through here, the editor's
// JSON included, so no client carries a second copy of it.
var dockerIconClasses = map[string]string{
	"start":   "ti-player-play",
	"stop":    "ti-player-stop",
	"restart": "ti-refresh",
	"build":   "ti-hammer",
	"pull":    "ti-download",
	"purge":   "ti-trash",
	"command": "ti-command",
}

// DockerIconClass answers the class one icon name renders as. A name nobody
// knows and no name at all both end at the neutral command icon: an empty box
// in a menu reads as a broken page.
func DockerIconClass(name string) string {
	if class, ok := dockerIconClasses[name]; ok {
		return class
	}
	return dockerIconClasses[docker.DefaultIcon]
}

// DockerIcon is one entry of the icon vocabulary as the settings form offers
// it, the name that is stored next to the picture it stands for.
type DockerIcon struct {
	Name  string
	Class string
}

// DockerIcons is the vocabulary in the order it is offered.
func DockerIcons() []DockerIcon {
	out := make([]DockerIcon, 0, len(docker.IconNames))
	for _, name := range docker.IconNames {
		out = append(out, DockerIcon{Name: name, Class: DockerIconClass(name)})
	}
	return out
}

// DockerButton is one configured command as a surface offers it: the icon
// resolved to a class, everything else as it is stored.
type DockerButton struct {
	ID      string
	Icon    string
	Label   string
	Command string
	Confirm bool
}

// DockerButtons resolves the configured commands for the surfaces that render
// them, the projects page and the editor's docker view.
func DockerButtons(actions []docker.Action) []DockerButton {
	out := make([]DockerButton, 0, len(actions))
	for _, action := range actions {
		out = append(out, DockerButton{
			ID:      action.ID,
			Icon:    DockerIconClass(action.Icon),
			Label:   action.Label,
			Command: action.Command,
			Confirm: action.Confirm,
		})
	}
	return out
}

// DockerActionRow is one configured compose command on the settings form,
// together with the argv it splits into, so what a line really becomes is
// visible under the field it is typed in. Error carries what makes the line
// unusable, an unclosed quote for example.
type DockerActionRow struct {
	docker.Action
	Argv  []string
	Error string
}

// IconClass is the picture the row's icon name stands for.
func (r DockerActionRow) IconClass() string { return DockerIconClass(r.Icon) }

// DockerActionRows pairs every configured command with the argv it splits
// into. The split is the same function the run uses, so the preview cannot
// drift away from what actually starts.
func DockerActionRows(actions []docker.Action) []DockerActionRow {
	rows := make([]DockerActionRow, 0, len(actions))
	for _, action := range actions {
		row := DockerActionRow{Action: action}
		argv, err := docker.SplitCommand(action.Command)
		if err != nil {
			row.Error = upperFirst(err.Error()) + "."
		} else {
			row.Argv = argv
		}
		rows = append(rows, row)
	}
	return rows
}

// TimeoutLabel is how long the entry may run, the value as it is stored when
// it can be read at all.
func (r DockerActionRow) TimeoutLabel() string {
	if strings.TrimSpace(r.Timeout) == "" {
		return r.Duration().String()
	}
	return r.Timeout
}

// linkRuleSamples is how many of a rule's current findings a row shows. A
// rule may cover a dozen hosts of one container; the row is there to
// say that it works, not to list a proxy's routing table.
const linkRuleSamples = 4

// DockerLinkRuleRow is one configured link rule on the settings form,
// together with what makes it unusable and what it finds in the containers
// that run right now. A regular expression nobody can try is a field nobody
// can fill in, so the row answers both questions where the pattern is typed.
type DockerLinkRuleRow struct {
	docker.LinkRule
	Error string
	// Sample are the first few addresses the rule currently yields, Found how
	// many there are in total.
	Sample []string
	Found  int
}

// DockerLinkRuleRows pairs every rule with its verdict and its preview. The
// preview runs through the same matcher the pages build their links with, on
// the same cached container list they read, so what a row promises cannot
// drift away from what the menus offer.
func DockerLinkRuleRows(rules []docker.LinkRule, containers []docker.Container) []DockerLinkRuleRow {
	rows := make([]DockerLinkRuleRow, 0, len(rules))
	for _, rule := range rules {
		row := DockerLinkRuleRow{LinkRule: rule}
		if err := rule.Validate(); err != nil {
			row.Error = upperFirst(err.Error()) + "."
			rows = append(rows, row)
			continue
		}
		matcher := docker.NewLinkMatcher([]docker.LinkRule{rule})
		for _, container := range containers {
			for _, link := range matcher.Routes(container.Labels) {
				row.Found++
				if len(row.Sample) < linkRuleSamples {
					row.Sample = append(row.Sample, link.Address())
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// More is how many findings the row does not show.
func (r DockerLinkRuleRow) More() int {
	if r.Found <= len(r.Sample) {
		return 0
	}
	return r.Found - len(r.Sample)
}
