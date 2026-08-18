// Package statusline is the status line the cockpit puts together for claude:
// one ordered list of entries somebody arranges on a settings page, and the
// bash script that renders them.
//
// The list is the line. An entry is a value, a separator or a line break, and
// all three sit in the same list, so a second line costs no setting of its
// own. A value entry says four things about itself and no more: which value,
// whether a label runs in front of it and how it reads, that label's color,
// and, for a number, the bounds its own color follows. A value that is no
// number carries one fixed color instead of the bounds.
//
// The switch and the list are two different answers. Off means the cockpit
// hands claude no statusLine at all, so whatever the user's own settings ask
// for still stands, and the generated script leaves the state directory; the
// list stays stored and comes back exactly as it was when the switch goes on
// again. A fresh install is off. The user's own claude settings files are
// never written.
package statusline

import (
	"encoding/json"
	"sort"
	"strings"
)

// CoderID names the coder these settings belong to. The status line is
// claude's own surface, so the settings page is a section of that coder.
const CoderID = "claude"

// SettingKey is where the switch and the entries live in the shared settings
// store, which is also what carries them into a backup. Both sit in one value
// on purpose: a restored backup cannot bring the list back without the switch
// that says whether it is in effect.
const SettingKey = "claude-statusline"

// Kind tells the three entry shapes apart. A separator and a line break are
// ordinary entries of the same list, which is what lets one list describe a
// line with several parts, and several lines, without a layout section.
type Kind string

const (
	KindValue     Kind = "value"
	KindSeparator Kind = "separator"
	KindBreak     Kind = "break"
)

// DefaultSeparator is what a separator shows when nobody typed anything.
const DefaultSeparator = "·"

// FreeTextValue is the one value whose content is the entry's own text rather
// than something a source answers.
const FreeTextValue = "text"

// Config is the whole stored answer: whether the cockpit sets the line at all,
// and what the line is.
type Config struct {
	Enabled bool    `json:"enabled"`
	Entries []Entry `json:"entries"`
}

// Entry is one item of the list. Which fields mean anything depends on Kind:
// a value entry carries Value, Label, LabelColor and either Thresholds (a
// number) or Color (everything else), a separator carries Text, a line break
// carries nothing at all, and the free text value carries Text as well, which
// is what it shows.
type Entry struct {
	Kind       Kind        `json:"kind"`
	Value      string      `json:"value,omitempty"`
	Label      string      `json:"label,omitempty"`
	LabelColor string      `json:"labelColor,omitempty"`
	Color      string      `json:"color,omitempty"`
	Thresholds []Threshold `json:"thresholds,omitempty"`
	Text       string      `json:"text,omitempty"`
}

// Threshold is a bound plus the color the value wears from there on. Several
// per entry are allowed and the highest one the value reaches wins, so a bound
// at zero is what gives a number its base color.
type Threshold struct {
	At    float64 `json:"at"`
	Color string  `json:"color"`
}

// Color is one name of the palette, the SGR parameter it prints as and the
// color the preview paints it with. The name is what is stored, so the two
// renderings can be changed without touching anybody's settings.
type Color struct {
	Name string
	ANSI string
	CSS  string
}

// DefaultColorName is what an entry that names no color, or one nobody knows,
// falls back to: the terminal's own foreground.
const DefaultColorName = "default"

// Colors is the palette in the order the selects offer it. The CSS values are
// read on the dark block the preview renders in, so they need no light
// variant of their own.
var Colors = []Color{
	{Name: DefaultColorName, ANSI: "", CSS: "#e6e9ef"},
	{Name: "dim", ANSI: "2", CSS: "#8d939c"},
	{Name: "red", ANSI: "31", CSS: "#f0736f"},
	{Name: "green", ANSI: "32", CSS: "#63c384"},
	{Name: "yellow", ANSI: "33", CSS: "#e2b558"},
	{Name: "blue", ANSI: "34", CSS: "#6ea8fe"},
	{Name: "magenta", ANSI: "35", CSS: "#d38ce4"},
	{Name: "cyan", ANSI: "36", CSS: "#54c1d6"},
	{Name: "white", ANSI: "37", CSS: "#f8f9fa"},
}

// NormalizeColor answers the name to store for what somebody picked. The
// selects offer the palette, so anything else is a hand written request and
// takes the terminal's own color rather than an escape nothing can print.
func NormalizeColor(name string) string {
	for _, c := range Colors {
		if c.Name == name {
			return name
		}
	}
	return DefaultColorName
}

// ANSI is the SGR parameter a color name prints as, empty for the terminal's
// own foreground.
func ANSI(name string) string {
	for _, c := range Colors {
		if c.Name == name {
			return c.ANSI
		}
	}
	return ""
}

// source says where a value comes from, which is what decides what the script
// pays for it: the payload on stdin is free and is therefore first choice
// wherever it carries the number at all, git and the shell are processes, and
// the usage API is a network call, which is why only what the payload does not
// know is left on it.
type source int

const (
	fromPayload source = iota
	fromUsage
	fromGit
	fromShell
	fromTranscript
	fromEntry
)

// format is how a raw value becomes the text on the line, and it also says
// what a bound is compared against: a percentage against percent, a counter
// against the count, money against the amount, and a span against minutes.
type format int

const (
	asText format = iota
	asPercent
	asMoney
	asCount
	asSpan
	// asRate is money per hour, asDelta money the shell works out from what
	// the last run wrote down.
	asRate
	asDelta
)

// resolve is the step between a source and a span: a timestamp has to become
// the seconds to or from it before it can be read as a length of time.
type resolve int

const (
	resolveNone resolve = iota
	resolveUntil
	resolveSince
)

// Value is one thing the line can show. Sample and Number are what the preview
// stands in with, so the bounds can be seen working before a save.
type Value struct {
	ID      string
	Group   string
	Label   string
	Hint    string
	Numeric bool
	Sample  string
	Number  float64

	source source
	// raw reads the value out of its own document, as a jq expression.
	raw string
	// shell is what a value outside the payload runs to answer, one line of
	// bash writing the raw value.
	shell string
	// format turns the raw value into the text on the line.
	format format
	// resolve is the step a timestamp takes before the format sees it.
	resolve resolve
}

// Groups is the order the select offers the groups in.
var Groups = []string{"Coder", "Context", "Tokens", "Cost", "Limits", "Git", "Place", "System", "Free"}

// Values is what the settings page offers, grouped and in this order. Every
// one of them is something a source really answers: the payload claude writes
// to the script's stdin wherever it carries it, git for the repository, the
// shell for the machine, and the usage API for the one number the payload does
// not know.
var Values = []Value{
	// The coder itself
	{
		ID: "model", Group: "Coder", Label: "Model", Hint: "The model this coder runs on, without the note in brackets.",
		Sample: "Opus 5",
		source: fromPayload, raw: `((.model.display_name // "") | split(" (")[0])`, format: asText,
	},
	{
		ID: "model_id", Group: "Coder", Label: "Model id", Hint: "The model's full identifier.",
		Sample: "claude-opus-5",
		source: fromPayload, raw: `(.model.id // "")`, format: asText,
	},
	{
		ID: "output_style", Group: "Coder", Label: "Output style", Hint: "The output style this coder runs with.",
		Sample: "default",
		source: fromPayload, raw: `(.output_style.name // "")`, format: asText,
	},
	{
		ID: "version", Group: "Coder", Label: "Claude version", Hint: "The version of the claude CLI.",
		Sample: "2.1.233",
		source: fromPayload, raw: `(.version // "")`, format: asText,
	},
	{
		ID: "session_id", Group: "Coder", Label: "Coder id", Hint: "The first eight characters of the identifier claude knows this coder by.",
		Sample: "450c6a9d",
		source: fromPayload, raw: `((.session_id // "")[0:8])`, format: asText,
	},
	{
		ID: "duration", Group: "Coder", Label: "Running time", Hint: "How long this coder has been running, wall clock. Bounds are in minutes.",
		Numeric: true, Sample: "1h", Number: 65,
		source: fromPayload, raw: `(.cost.total_duration_ms | secs)`, format: asSpan,
	},
	{
		ID: "api_duration", Group: "Coder", Label: "API time", Hint: "How long this coder actually waited for the model, which is a fraction of the time it ran. Bounds are in minutes.",
		Numeric: true, Sample: "45s", Number: 0.75,
		source: fromPayload, raw: `(.cost.total_api_duration_ms | secs)`, format: asSpan,
	},

	// Context
	{
		ID: "context", Group: "Context", Label: "Context used", Hint: "How much of the context window this coder holds.",
		Numeric: true, Sample: "42%", Number: 42,
		source: fromPayload, raw: `.context_window.used_percentage`, format: asPercent,
	},
	{
		ID: "context_tokens", Group: "Context", Label: "Context tokens", Hint: "The tokens in the window right now, fresh input and both cache halves, which is what the percentage above counts.",
		Numeric: true, Sample: "84.2k", Number: 84200,
		source: fromPayload, raw: `.context_window.total_input_tokens`, format: asCount,
	},
	{
		ID: "context_left", Group: "Context", Label: "Context left", Hint: "What the window still takes.",
		Numeric: true, Sample: "115.8k", Number: 115800,
		source: fromPayload, raw: contextLeftExpr, format: asCount,
	},
	{
		ID: "context_size", Group: "Context", Label: "Context size", Hint: "How big the window is.",
		Numeric: true, Sample: "200k", Number: 200000,
		source: fromPayload, raw: `.context_window.context_window_size`, format: asCount,
	},

	// Tokens. The four counts and the hit rate are the last request's, which is
	// what the payload carries; the four sums below them are the whole
	// conversation's and are the only thing here that reads the transcript.
	{
		ID: "tokens_input", Group: "Tokens", Label: "Input", Hint: "Input tokens of the last request, cache not counted.",
		Numeric: true, Sample: "2", Number: 2,
		source: fromPayload, raw: `.context_window.current_usage.input_tokens`, format: asCount,
	},
	{
		ID: "tokens_output", Group: "Tokens", Label: "Output", Hint: "Output tokens of the last request.",
		Numeric: true, Sample: "217", Number: 217,
		source: fromPayload, raw: `.context_window.current_usage.output_tokens`, format: asCount,
	},
	{
		ID: "tokens_cache_read", Group: "Tokens", Label: "Cache read", Hint: "Tokens the last request read out of the cache.",
		Numeric: true, Sample: "22.1k", Number: 22100,
		source: fromPayload, raw: `.context_window.current_usage.cache_read_input_tokens`, format: asCount,
	},
	{
		ID: "tokens_cache_write", Group: "Tokens", Label: "Cache creation", Hint: "Tokens the last request wrote into the cache.",
		Numeric: true, Sample: "19.4k", Number: 19400,
		source: fromPayload, raw: `.context_window.current_usage.cache_creation_input_tokens`, format: asCount,
	},
	{
		ID: "cache_hit", Group: "Tokens", Label: "Cache hit rate", Hint: "The share of the last request's input that came out of the cache.",
		Numeric: true, Sample: "53.3%", Number: 53.3,
		source: fromPayload, raw: cacheHitExpr, format: asPercent,
	},

	{
		ID: "session_input", Group: "Tokens", Label: "Session input", Hint: "Input tokens over every turn of this conversation, cache not counted. Read from the transcript.",
		Numeric: true, Sample: "12.4k", Number: 12400,
		source: fromTranscript, raw: `.i`, format: asCount,
	},
	{
		ID: "session_output", Group: "Tokens", Label: "Session output", Hint: "Output tokens over every turn of this conversation. Read from the transcript.",
		Numeric: true, Sample: "38.2k", Number: 38200,
		source: fromTranscript, raw: `.o`, format: asCount,
	},
	{
		ID: "session_cache_read", Group: "Tokens", Label: "Session cache read", Hint: "Cache reads added up over every turn, which is what a whole conversation really reads. Read from the transcript.",
		Numeric: true, Sample: "1.1M", Number: 1100000,
		source: fromTranscript, raw: `.r`, format: asCount,
	},
	{
		ID: "session_cache_write", Group: "Tokens", Label: "Session cache creation", Hint: "Cache writes added up over every turn. Read from the transcript.",
		Numeric: true, Sample: "420k", Number: 420000,
		source: fromTranscript, raw: `.w`, format: asCount,
	},

	// Cost
	{
		ID: "cost", Group: "Cost", Label: "Cost", Hint: "What this coder has spent since it started, not what the whole conversation cost.",
		Numeric: true, Sample: "$1.24", Number: 1.24,
		source: fromPayload, raw: `.cost.total_cost_usd`, format: asMoney,
	},
	{
		ID: "burn", Group: "Cost", Label: "Burn rate", Hint: "What this coder spends per hour, its cost over the time it ran. Away in the first minute, where the number would be invented.",
		Numeric: true, Sample: "$7.49/h", Number: 7.49,
		source: fromPayload, raw: burnExpr, format: asRate,
	},
	{
		ID: "cost_turn", Group: "Cost", Label: "Cost, last turn", Hint: "What the cost went up by since the line was drawn last. Away until there is a second reading to compare.",
		Numeric: true, Sample: "$0.13", Number: 0.13,
		source: fromPayload, format: asDelta,
	},

	{
		ID: "lines_added", Group: "Cost", Label: "Lines added", Hint: "Lines this coder added since it started.",
		Numeric: true, Sample: "156", Number: 156,
		source: fromPayload, raw: `.cost.total_lines_added`, format: asCount,
	},
	{
		ID: "lines_removed", Group: "Cost", Label: "Lines removed", Hint: "Lines this coder removed since it started.",
		Numeric: true, Sample: "23", Number: 23,
		source: fromPayload, raw: `.cost.total_lines_removed`, format: asCount,
	},

	// Limits
	{
		ID: "session", Group: "Limits", Label: "Five hour limit", Hint: "How much of the rolling five hour limit is spent.",
		Numeric: true, Sample: "16%", Number: 16,
		source: fromPayload, raw: `.rate_limits.five_hour.used_percentage`, format: asPercent,
	},
	{
		ID: "session_reset", Group: "Limits", Label: "Five hour reset", Hint: "How long the five hour limit still runs. Bounds are in minutes.",
		Numeric: true, Sample: "2h", Number: 120,
		source: fromPayload, raw: `(.rate_limits.five_hour.resets_at // "")`, format: asSpan, resolve: resolveUntil,
	},
	{
		ID: "week", Group: "Limits", Label: "Weekly limit", Hint: "The weekly limit over all models.",
		Numeric: true, Sample: "69%", Number: 69,
		source: fromPayload, raw: `.rate_limits.seven_day.used_percentage`, format: asPercent,
	},
	{
		ID: "week_top", Group: "Limits", Label: "Weekly, one model", Hint: "The weekly limit that belongs to one model rather than to all of them, the strongest one on the usual plan. The one value the payload does not carry, so this entry alone asks the usage API; where an account carries several of them, the first one answers.",
		Numeric: true, Sample: "82%", Number: 82,
		source: fromUsage, raw: `$top.percent`, format: asPercent,
	},
	{
		ID: "reset", Group: "Limits", Label: "Weekly reset", Hint: "How long the weekly limits still run. Bounds are in minutes.",
		Numeric: true, Sample: "2d", Number: 2880,
		source: fromPayload, raw: `(.rate_limits.seven_day.resets_at // "")`, format: asSpan, resolve: resolveUntil,
	},

	// Git. Every one of them falls away without git or outside a repository.
	{
		// Asked with the one command that answers the branch itself rather than
		// what HEAD resolves to: a repository without a first commit is on a
		// branch nothing points at yet, and a detached head is on none at all.
		// rev-parse --abbrev-ref writes the word HEAD in both of those cases,
		// which is a branch nobody is on.
		ID: "branch", Group: "Git", Label: "Branch", Hint: "The branch the folder is on. Away on a detached head, where there is none.",
		Sample: "master",
		source: fromGit, shell: `branch --show-current`, format: asText,
	},
	{
		ID: "git_changes", Group: "Git", Label: "Changed files", Hint: "How many files are changed, every new file counted for itself, a clean tree says zero.",
		Numeric: true, Sample: "3", Number: 3,
		source: fromGit, format: asCount,
	},
	{
		ID: "git_ahead_behind", Group: "Git", Label: "Ahead and behind", Hint: "How far the branch is from its upstream. Nothing to say when they agree.",
		Sample: "↑2 ↓1",
		source: fromGit, format: asText,
	},
	{
		ID: "git_stashes", Group: "Git", Label: "Stashes", Hint: "How many stashes the repository holds.",
		Numeric: true, Sample: "1", Number: 1,
		source: fromGit, format: asCount,
	},
	{
		ID: "git_age", Group: "Git", Label: "Last commit age", Hint: "How long ago the last commit was made. Bounds are in minutes.",
		Numeric: true, Sample: "20m", Number: 20,
		source: fromGit, shell: `log -1 --format=%ct`, format: asSpan, resolve: resolveSince,
	},

	// Place
	{
		ID: "dir", Group: "Place", Label: "Folder", Hint: "The name of the folder the coder works in.",
		Sample: "dev-cockpit",
		source: fromPayload, raw: `((.workspace.current_dir // .cwd // "") | split("/") | last)`, format: asText,
	},
	{
		ID: "dir_full", Group: "Place", Label: "Folder path", Hint: "The whole path of that folder.",
		Sample: "/root/projects/dev-cockpit",
		source: fromPayload, raw: `(.workspace.current_dir // .cwd // "")`, format: asText,
	},
	// System
	{
		ID: "time", Group: "System", Label: "Time", Hint: "The time on this machine.",
		Sample: "14:32",
		source: fromShell, shell: `date +%H:%M 2>/dev/null`, format: asText,
	},
	{
		ID: "date", Group: "System", Label: "Date", Hint: "The date on this machine.",
		Sample: "2026-08-16",
		source: fromShell, shell: `date +%Y-%m-%d 2>/dev/null`, format: asText,
	},
	{
		ID: "host", Group: "System", Label: "Host", Hint: "The name of this machine.",
		Sample: "eax",
		source: fromShell, shell: `printf '%s' "${HOSTNAME:-$(hostname 2>/dev/null)}"`, format: asText,
	},
	{
		ID: "user", Group: "System", Label: "User", Hint: "The account the coder runs under.",
		Sample: "root",
		source: fromShell, shell: `printf '%s' "${USER:-$(id -un 2>/dev/null)}"`, format: asText,
	},
	// There is deliberately no terminal width here, and the reason is not that
	// there is none to read: claude runs the script with its streams on pipes,
	// so there is no terminal to ask, but it puts the width and the height of
	// the one it draws into the environment itself, COLUMNS and LINES, and has
	// done since 2.1.153 (measured against 2.1.234 in a 137 column pane: both
	// arrive). Nothing on this line reads a width, every entry renders the same
	// at any size, so the value would be a number to look at and nothing else.

	// Free
	{
		ID: FreeTextValue, Group: "Free", Label: "Text", Hint: "Whatever you type, as it stands.",
		Sample: "text",
		source: fromEntry, format: asText,
	},
}

// The jq expressions long enough to stand on their own. Every one of them
// answers null where the payload says nothing, which is what makes the entry
// fall out of the line instead of showing a zero nobody measured.
const (
	// What is in the window is the input side alone, measured: claude's own
	// used_percentage counts input plus both cache halves and leaves the last
	// answer's output tokens out, and total_input_tokens is exactly that sum,
	// so this is the count the percentage beside it is a percentage of. Both
	// halves have to be numbers, or what is left is the whole window and the
	// entry would claim an empty one nobody measured.
	contextLeftExpr = `(if ((.context_window.context_window_size | type) == "number") and ((.context_window.total_input_tokens | type) == "number")
     then ([.context_window.context_window_size - .context_window.total_input_tokens, 0] | max)
     else null end)`
	burnExpr = `(if ((.cost.total_duration_ms | type) == "number") and (.cost.total_duration_ms >= 60000) and ((.cost.total_cost_usd | type) == "number")
     then (.cost.total_cost_usd / (.cost.total_duration_ms / 3600000))
     else null end)`
	cacheHitExpr = `(if (.context_window.current_usage | type) == "object"
     then (.context_window.current_usage
       | ((.input_tokens // 0) + (.cache_read_input_tokens // 0) + (.cache_creation_input_tokens // 0)) as $in
       | if $in > 0 then (.cache_read_input_tokens // 0) / $in * 100 else null end)
     else null end)`
)

// ValueByID picks a value out of the list.
func ValueByID(id string) (Value, bool) {
	for _, v := range Values {
		if v.ID == id {
			return v, true
		}
	}
	return Value{}, false
}

// IsNumeric reports whether a value carries bounds rather than one color.
func IsNumeric(id string) bool {
	value, ok := ValueByID(id)
	return ok && value.Numeric
}

// ValuesInGroup answers the values of one group, in their order.
func ValuesInGroup(group string) []Value {
	out := []Value{}
	for _, value := range Values {
		if value.Group == group {
			out = append(out, value)
		}
	}
	return out
}

// Defaults is the line an install that never touched the setting is offered:
// the model, then the context, the five hour limit and the two weekly ones
// behind dimmed one letter labels, a middle dot between them, and the time to
// the reset at the end. Green up to 50, yellow from there, red from 80.
func Defaults() []Entry {
	bounds := func() []Threshold {
		return []Threshold{{At: 0, Color: "green"}, {At: 50, Color: "yellow"}, {At: 80, Color: "red"}}
	}
	separator := func() Entry { return Entry{Kind: KindSeparator, Text: DefaultSeparator} }
	limit := func(value, label string) Entry {
		return Entry{Kind: KindValue, Value: value, Label: label, LabelColor: "dim", Thresholds: bounds()}
	}
	return []Entry{
		{Kind: KindValue, Value: "model", Color: "cyan"},
		separator(),
		limit("context", "c"),
		separator(),
		limit("session", "5"),
		separator(),
		limit("week", "w"),
		separator(),
		limit("week_top", "F"),
		// The time to the reset is a length of time and therefore a number,
		// and one bound at zero is how a number wears one color throughout.
		{Kind: KindValue, Value: "reset", Label: "↻", LabelColor: "dim", Thresholds: []Threshold{{At: 0, Color: "blue"}}},
	}
}

// DefaultConfig is what an install that never answered has: the line above,
// and off, because taking over a status line somebody else set is not a
// default anybody asked for.
func DefaultConfig() Config {
	return Config{Enabled: false, Entries: Defaults()}
}

// Normalize is what everything downstream reads: it drops what cannot be
// rendered (an entry of an unknown kind, a value nobody offers), puts the
// colors into the palette, sorts the bounds so the highest match is the last
// one that matched, and leaves every entry with only the fields its kind
// means.
func Normalize(entries []Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		switch entry.Kind {
		case KindBreak:
			out = append(out, Entry{Kind: KindBreak})
		case KindSeparator:
			text := oneLine(entry.Text)
			if strings.TrimSpace(text) == "" {
				text = DefaultSeparator
			}
			out = append(out, Entry{Kind: KindSeparator, Text: text})
		case KindValue:
			value, ok := ValueByID(entry.Value)
			if !ok {
				continue
			}
			clean := Entry{
				Kind:       KindValue,
				Value:      value.ID,
				Label:      oneLine(entry.Label),
				LabelColor: NormalizeColor(entry.LabelColor),
			}
			if value.source == fromEntry {
				clean.Text = oneLine(entry.Text)
			}
			if value.Numeric {
				clean.Thresholds = normalizeThresholds(entry.Thresholds)
			} else {
				clean.Color = NormalizeColor(entry.Color)
			}
			out = append(out, clean)
		}
	}
	return out
}

// oneLine is what a label, a separator and the free text are cut down to. The
// status line is one line and a terminal reads what it is handed: a line break
// in a stored answer would write a second line the list never asked for, and an
// escape would reach the terminal as a command rather than as text. Everything
// printable stays exactly as it was typed, quotes, dollars and backticks
// included, because the script quotes them and never runs them.
func oneLine(text string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, text)
}

func normalizeThresholds(list []Threshold) []Threshold {
	out := make([]Threshold, 0, len(list))
	for _, t := range list {
		out = append(out, Threshold{At: t.At, Color: NormalizeColor(t.Color)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	if len(out) == 0 {
		return nil
	}
	return out
}

// Pick answers the color a number wears: the highest bound it reaches wins,
// and a number below every bound wears none, which is the terminal's own
// color. The list is expected sorted, which is what Normalize leaves behind.
func Pick(thresholds []Threshold, value float64) string {
	color := ""
	for _, t := range thresholds {
		if value >= t.At {
			color = t.Color
		}
	}
	return color
}

// Decode reads the stored JSON. An empty value is the default configuration,
// which is off with the default line offered on the form.
func Decode(raw string) (Config, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultConfig(), nil
	}
	var config Config
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return Config{}, err
	}
	if config.Entries == nil {
		config.Entries = []Entry{}
	}
	return config, nil
}

// Encode writes the configuration back the way the store keeps it.
func Encode(config Config) string {
	if config.Entries == nil {
		config.Entries = []Entry{}
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return ""
	}
	return string(raw)
}
