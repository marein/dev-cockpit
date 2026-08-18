package statusline

import "reflect"

// Preset is a whole line somebody can start from instead of an empty list. The
// lines are short on purpose, one character per label and one reset per line:
// a phone shows the status line in a terminal some forty characters wide, and
// claude cuts what does not fit.
type Preset struct {
	ID          string
	Title       string
	Description string
	entries     func() []Entry
}

// Entries is the preset's line, normalized like a saved one.
func (p Preset) Entries() []Entry { return Normalize(p.entries()) }

// Presets is the order the settings page offers them in. The first one is the
// default line, the one that fits every login: the values a plan or a price
// does not answer leave it, so an API key or a local model gets a shorter line
// rather than a wrong one. None of them asks the network but the subscription
// line, whose one model limit only the usage API knows.
var Presets = []Preset{
	{
		ID: "everyone", Title: "Everyone",
		Description: "Model, context and how long the prompt cache stays warm, plus the plan's limits where there are some.",
		entries: func() []Entry {
			return []Entry{
				{Kind: KindValue, Value: "model", Color: "cyan"},
				separator(), percentage("context", "c"),
				separator(), cacheWarm(),
				separator(), percentage("session", "5"),
				separator(), percentage("week", "w"),
			}
		},
	},
	{
		ID: "api", Title: "API key",
		Description: "What it costs so far, and a mark once the long context price applies.",
		entries: func() []Entry {
			return []Entry{
				{Kind: KindValue, Value: "model", Color: "cyan"},
				separator(), percentage("context", "c"),
				separator(), cacheWarm(),
				separator(), {Kind: KindValue, Value: "cost"},
				separator(), {Kind: KindValue, Value: "over_200k", Color: "yellow"},
			}
		},
	},
	{
		ID: "subscription", Title: "Subscription",
		Description: "The plan's limits: five hours, the week, the week of one model and the time to its reset.",
		entries: func() []Entry {
			return []Entry{
				{Kind: KindValue, Value: "model", Color: "cyan"},
				separator(), percentage("context", "c"),
				separator(), percentage("session", "5"),
				separator(), percentage("week", "w"),
				separator(), percentage("week_top", "F"),
				// The time to the reset is a length of time and therefore a
				// number, and one bound at zero is how a number wears one
				// color throughout.
				{Kind: KindValue, Value: "reset", Label: "↻", LabelColor: "dim", Thresholds: []Threshold{{At: 0, Color: "blue"}}},
			}
		},
	},
}

func separator() Entry { return Entry{Kind: KindSeparator, Text: DefaultSeparator} }

// percentage is a share behind a dimmed label: green up to 50, yellow from
// there, red from 80.
func percentage(value, label string) Entry {
	return Entry{Kind: KindValue, Value: value, Label: label, LabelColor: "dim", Thresholds: []Threshold{
		{At: 0, Color: "green"}, {At: 50, Color: "yellow"}, {At: 80, Color: "red"},
	}}
}

// cacheWarm is red once the cache is cold and green while it holds a minute or
// more.
func cacheWarm() Entry {
	return Entry{Kind: KindValue, Value: "cache_left", Label: "◔", LabelColor: "dim", Thresholds: []Threshold{
		{At: 0, Color: "red"}, {At: 1, Color: "green"},
	}}
}

// PresetByID picks a preset out of the list.
func PresetByID(id string) (Preset, bool) {
	for _, p := range Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// Defaults is the line an install that never touched the setting gets.
func Defaults() []Entry { return Presets[0].Entries() }

// SameLine reports whether two lists draw the same line, which is what tells
// the page which preset is in use.
func SameLine(a, b []Entry) bool {
	return reflect.DeepEqual(Normalize(a), Normalize(b))
}
