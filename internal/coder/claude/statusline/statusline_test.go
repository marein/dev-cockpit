package statusline

import (
	"reflect"
	"testing"
)

// Every value the page offers has to be in a group the select renders, or it
// is offered nowhere at all.
func TestEveryValueStandsInAGroup(t *testing.T) {
	known := map[string]bool{}
	for _, group := range Groups {
		known[group] = true
	}
	seen := 0
	for _, value := range Values {
		if !known[value.Group] {
			t.Errorf("%s stands in group %q, which the select does not render", value.ID, value.Group)
		}
	}
	for _, group := range Groups {
		in := ValuesInGroup(group)
		if len(in) == 0 {
			t.Errorf("group %q holds no value", group)
		}
		seen += len(in)
	}
	if seen != len(Values) {
		t.Errorf("the groups hold %d values, the table has %d", seen, len(Values))
	}
}

// Every value reads something, and a value that reads the entry's own text
// offers a field for it.
func TestEveryValueKnowsWhereItComesFrom(t *testing.T) {
	ids := map[string]bool{}
	for _, value := range Values {
		if ids[value.ID] {
			t.Errorf("%s is in the table twice", value.ID)
		}
		ids[value.ID] = true
		if value.Label == "" || value.Hint == "" || value.Sample == "" {
			t.Errorf("%s is missing its label, hint or sample", value.ID)
		}
		if value.read == nil {
			t.Errorf("%s reads nothing", value.ID)
		}
		ownText := value.source == fromEntry || value.source == fromCommand
		if ownText && value.TextLabel == "" {
			t.Errorf("%s reads the entry's own text and offers no field for it", value.ID)
		}
	}
}

func TestNormalizeDropsWhatCannotBeRendered(t *testing.T) {
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "nothing-of-the-sort"},
		{Kind: "wobble"},
		{Kind: KindValue, Value: "model", Color: "cyan"},
	})
	if len(entries) != 1 || entries[0].Value != "model" {
		t.Fatalf("normalize kept %+v", entries)
	}
}

func TestNormalizeSeparatesTheKindsFields(t *testing.T) {
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "context", Color: "cyan", Thresholds: []Threshold{{At: 80, Color: "red"}, {At: 0, Color: "green"}}},
		{Kind: KindValue, Value: "model", Color: "cyan", Thresholds: []Threshold{{At: 0, Color: "red"}}},
		{Kind: KindSeparator},
		{Kind: KindBreak, Value: "model", Text: "x"},
	})
	number := entries[0]
	if number.Color != "" {
		t.Errorf("a number keeps a fixed color %q", number.Color)
	}
	if want := []Threshold{{At: 0, Color: "green"}, {At: 80, Color: "red"}}; !reflect.DeepEqual(number.Thresholds, want) {
		t.Errorf("bounds are %+v, want them sorted %+v", number.Thresholds, want)
	}
	text := entries[1]
	if text.Color != "cyan" || text.Thresholds != nil {
		t.Errorf("a text value is %+v, want the fixed color and no bounds", text)
	}
	if entries[2].Text != DefaultSeparator {
		t.Errorf("an empty separator is %q, want %q", entries[2].Text, DefaultSeparator)
	}
	if want := (Entry{Kind: KindBreak}); !reflect.DeepEqual(entries[3], want) {
		t.Errorf("a line break is %+v, want it empty", entries[3])
	}
}

func TestNormalizeKeepsColorsInThePalette(t *testing.T) {
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "model", Color: "chartreuse", LabelColor: "puce"},
		{Kind: KindValue, Value: "context", Thresholds: []Threshold{{At: 0, Color: "ultraviolet"}}},
	})
	if entries[0].Color != DefaultColorName || entries[0].LabelColor != DefaultColorName {
		t.Errorf("unknown colors survived: %+v", entries[0])
	}
	if entries[1].Thresholds[0].Color != DefaultColorName {
		t.Errorf("an unknown bound color survived: %+v", entries[1].Thresholds)
	}
}

// Only the visible text of what a command printed, a payload carried or
// somebody pasted reaches the line: whole escape sequences go, not just their
// first byte, and a tab is a space.
func TestOneLineKeepsTheVisibleTextAlone(t *testing.T) {
	cases := map[string]string{
		"\x1b[31mred\x1b[0m":                              "red",
		"\x1b[31mred\x1b[0m\tx":                           "red x",
		"\x1b[1;38;5;208mbold\x1b[m":                      "bold",
		"\x1b]8;;https://example.com\x07link\x1b]8;;\x07": "link",
		"\x1b]0;title\x1b\\text":                          "text",
		"\x1b(Bplain":                                     "plain",
		"a\tb":                                            "a b",
		"x\u009b31my":                                     "x31my",
		"cut \x1b[31":                                     "cut ",
		"ünï $(id) `id`":                                  "ünï $(id) `id`",
	}
	for in, want := range cases {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// A label, a separator and the free text are one line of text. A control
// character in one of them would either write a second line the list never
// asked for or reach the terminal as a command, so it is cut out on the way
// into the store; everything printable stays exactly as it was typed.
func TestNormalizeCutsAControlCharacterOutOfWhatIsTyped(t *testing.T) {
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "model", Label: "a\nb\x1bc\x1fd\te", Color: "cyan"},
		{Kind: KindValue, Value: FreeTextValue, Text: "on\n\x1b[31meax", Color: "cyan"},
		{Kind: KindSeparator, Text: "\n\x1b"},
		{Kind: KindValue, Value: "model", Label: `it's $(id) "one" ` + "`id`" + ` ünïcode`, Color: "cyan"},
	})
	// ESC c is a whole sequence, the reset, so the c goes with it, and the
	// tab is a space.
	if entries[0].Label != "abd e" {
		t.Errorf("the label is %q, want the control characters gone", entries[0].Label)
	}
	if entries[1].Text != "oneax" {
		t.Errorf("the free text is %q, want the escape sequence gone", entries[1].Text)
	}
	// A separator that is nothing but control characters is no separator, so it
	// falls back the way an empty one does.
	if entries[2].Text != DefaultSeparator {
		t.Errorf("the separator is %q, want %q", entries[2].Text, DefaultSeparator)
	}
	if want := `it's $(id) "one" ` + "`id`" + ` ünïcode`; entries[3].Label != want {
		t.Errorf("the label is %q, want %q untouched", entries[3].Label, want)
	}
}

// The default configuration is the default line as the fallback: somebody
// without a line of their own gets one, a line somebody set stays.
func TestDefaultConfigIsTheFallback(t *testing.T) {
	if DefaultConfig().Mode != ModeFallback {
		t.Fatalf("a fresh install stands on %q, want the fallback", DefaultConfig().Mode)
	}
	if len(DefaultConfig().Entries) == 0 {
		t.Fatal("a fresh install offers no line")
	}
}

// A mode the select does not offer is a hand written request, and so is one a
// stored answer does not carry at all.
func TestParseModeFallsBackOnAnUnknownName(t *testing.T) {
	for name, want := range map[string]Mode{"always": ModeAlways, "fallback": ModeFallback, "off": ModeOff, "": ModeFallback, "on": ModeFallback} {
		if got := ParseMode(name); got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", name, got, want)
		}
	}
	config, err := Decode(`{"entries":[]}`)
	if err != nil || config.Mode != ModeFallback {
		t.Fatalf("a stored answer without a mode decodes to %+v, %v", config, err)
	}
}

func TestDecodeAndEncodeRoundTrip(t *testing.T) {
	config := Config{Mode: ModeAlways, Entries: Normalize(Defaults())}
	back, err := Decode(Encode(config))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	back.Entries = Normalize(back.Entries)
	if !reflect.DeepEqual(back, config) {
		t.Fatalf("the round trip changed the configuration:\n%+v\n%+v", back, config)
	}
	// The mode and the list are apart: off keeps every entry.
	off := Config{Mode: ModeOff, Entries: Normalize(Defaults())}
	kept, err := Decode(Encode(off))
	if err != nil || kept.Mode != ModeOff || len(kept.Entries) != len(off.Entries) {
		t.Fatalf("switching off lost the list: %+v, %v", kept, err)
	}
	if empty, err := Decode(""); err != nil || empty.Mode != ModeFallback || len(empty.Entries) == 0 {
		t.Fatalf("an empty value decodes to %+v, %v", empty, err)
	}
	if _, err := Decode("{"); err == nil {
		t.Fatal("a damaged value decodes without an error")
	}
}
