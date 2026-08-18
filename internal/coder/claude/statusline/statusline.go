// Package statusline is the status line the cockpit puts together for claude:
// one ordered list of entries somebody arranges on a settings page, and the
// renderer claude runs through the cockpit's own binary on every redraw.
//
// The list is the line. An entry is a value, a separator or a line break, and
// all three sit in the same list, so a second line costs no setting of its
// own. A value entry says four things about itself and no more: which value,
// whether a label runs in front of it and how it reads, that label's color,
// and, for a number, the bounds its own color follows. A value that is no
// number carries one fixed color instead of the bounds.
//
// The mode and the list are two different answers. The mode says when the
// cockpit hands claude its line: always, only while claude's global settings
// set none of their own, or never. Off leaves the user's own settings standing
// and takes the rendered configuration out of the state directory; the list stays
// stored and comes back exactly as it was. A fresh install stands on the
// fallback, so somebody without a line of their own gets one. The user's own
// claude settings files are never written.
package statusline

import (
	"encoding/json"
	"sort"
	"strings"
)

// CoderID names the coder these settings belong to. The status line is
// claude's own surface, so the settings page is a section of that coder.
const CoderID = "claude"

// SettingKey is where the mode and the entries live in the shared settings
// store, which is also what carries them into a backup. Both sit in one value
// on purpose: a restored backup cannot bring the list back without the mode
// that says when it is in effect.
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

// FreeTextValue and CommandValue are the two values an entry's own text
// configures: the free text shows it, the command runs it.
const (
	FreeTextValue = "text"
	CommandValue  = "command"
)

// Mode says when the cockpit hands claude its line.
type Mode string

const (
	// ModeAlways sets the line even over one claude's global settings set.
	ModeAlways Mode = "always"
	// ModeFallback sets the line only while claude's global settings set none.
	ModeFallback Mode = "fallback"
	// ModeOff never sets it.
	ModeOff Mode = "off"
)

// ParseMode answers the mode to store for what somebody picked. Anything the
// select does not offer is a hand written request and takes the default.
func ParseMode(name string) Mode {
	switch mode := Mode(name); mode {
	case ModeAlways, ModeFallback, ModeOff:
		return mode
	}
	return ModeFallback
}

// Config is the whole stored answer: when the cockpit sets the line, and what
// the line is.
type Config struct {
	Mode    Mode    `json:"mode"`
	Entries []Entry `json:"entries"`
}

// Entry is one item of the list. Which fields mean anything depends on Kind:
// a value entry carries Value, Label, LabelColor and either Thresholds (a
// number) or Color (everything else), a separator carries Text, a line break
// carries nothing at all, and the free text and the command value carry Text as
// well, which is what the one shows and the other runs.
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

// DefaultConfig is what an install that never answered has: the default line
// as the fallback, so it shows where claude has no line and a line somebody
// set themselves stays.
func DefaultConfig() Config {
	return Config{Mode: ModeFallback, Entries: Defaults()}
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
			if value.TextLabel != "" {
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

// oneLine is what a label, a separator, an entry's own text and every value read
// from somewhere else are cut down to: the visible text and nothing else. The
// status line is one line and a terminal reads what it is handed: a line break
// would write a second line the list never asked for, and an escape sequence
// would reach the terminal as a command, a color a command printed would fight
// the color of its entry. So a whole escape sequence goes, not only the escape
// that starts it, every other control character goes, and a tab, which is
// space on the screen, becomes one. Everything printable stays exactly as it
// was typed, quotes, dollars and backticks included, because nothing on the
// way to the terminal is a shell.
func oneLine(text string) string {
	var b strings.Builder
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		switch r := runes[i]; {
		case r == 0x1b:
			i = escapeEnd(runes, i)
		case r == '\t':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// escapeEnd answers where the escape sequence starting at i ends: a control
// sequence (CSI, colors among them) at its final byte, a string (OSC, a link
// or a title among them, and DCS, SOS, PM, APC) at the BEL or ST that ends it,
// and any other at its final character. One that never ends takes the rest of
// the text with it, none of which was meant to be seen.
func escapeEnd(runes []rune, i int) int {
	last := len(runes) - 1
	if i >= last {
		return last
	}
	switch runes[i+1] {
	case '[':
		for j := i + 2; j <= last; j++ {
			if runes[j] >= 0x40 && runes[j] <= 0x7e {
				return j
			}
		}
	case ']', 'P', 'X', '^', '_':
		for j := i + 2; j <= last; j++ {
			if runes[j] == 0x07 {
				return j
			}
			if runes[j] == 0x1b && j < last && runes[j+1] == '\\' {
				return j + 1
			}
		}
	default:
		j := i + 1
		for j < last && runes[j] >= 0x20 && runes[j] <= 0x2f {
			j++
		}
		return j
	}
	return last
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

// Decode reads the stored JSON. An empty value is the default configuration.
func Decode(raw string) (Config, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultConfig(), nil
	}
	var config Config
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return Config{}, err
	}
	config.Mode = ParseMode(string(config.Mode))
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
