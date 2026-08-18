package statusline

import (
	"reflect"
	"testing"
)

func TestPickTakesTheHighestBoundThatIsReached(t *testing.T) {
	bounds := normalizeThresholds([]Threshold{
		{At: 80, Color: "red"},
		{At: 0, Color: "green"},
		{At: 50, Color: "yellow"},
	})
	cases := []struct {
		value float64
		want  string
	}{
		{value: 0, want: "green"},
		{value: 49.9, want: "green"},
		{value: 50, want: "yellow"},
		{value: 79.99, want: "yellow"},
		{value: 80, want: "red"},
		{value: 100, want: "red"},
	}
	for _, c := range cases {
		if got := Pick(bounds, c.value); got != c.want {
			t.Errorf("Pick(%v) = %q, want %q", c.value, got, c.want)
		}
	}
}

func TestPickWithoutAMatchingBoundAnswersNoColor(t *testing.T) {
	bounds := normalizeThresholds([]Threshold{{At: 50, Color: "yellow"}, {At: 80, Color: "red"}})
	if got := Pick(bounds, 12); got != "" {
		t.Fatalf("a value below every bound wears %q, want the terminal's own color", got)
	}
	if got := Pick(nil, 12); got != "" {
		t.Fatalf("a value without bounds wears %q", got)
	}
}

func TestPickReadsFractionalBounds(t *testing.T) {
	bounds := normalizeThresholds([]Threshold{{At: 0.5, Color: "yellow"}, {At: 1.25, Color: "red"}})
	if got := Pick(bounds, 1.24); got != "yellow" {
		t.Fatalf("1.24 wears %q, want yellow", got)
	}
	if got := Pick(bounds, 1.25); got != "red" {
		t.Fatalf("1.25 wears %q, want red", got)
	}
}

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

// A value is read out of one source with one rendering, and the two have to
// fit: a number needs something to compare, text needs no bounds.
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
		if value.Numeric != (value.format != asText) {
			t.Errorf("%s says numeric=%v with format %v", value.ID, value.Numeric, value.format)
		}
		switch value.source {
		case fromPayload, fromUsage:
			// A difference is the one value the shell works out on its own,
			// out of what the run before wrote down.
			if value.raw == "" && value.format != asDelta {
				t.Errorf("%s reads its document with nothing", value.ID)
			}
		case fromShell:
			if value.shell == "" {
				t.Errorf("%s runs nothing", value.ID)
			}
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
	if entries[0].Label != "abcde" {
		t.Errorf("the label is %q, want the control characters gone", entries[0].Label)
	}
	if entries[1].Text != "on[31meax" {
		t.Errorf("the free text is %q, want the escape gone", entries[1].Text)
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

func TestDefaultsAreTheLineTheyDescribe(t *testing.T) {
	entries := Normalize(Defaults())
	var values []string
	separators := 0
	for _, entry := range entries {
		switch entry.Kind {
		case KindValue:
			values = append(values, entry.Value)
		case KindSeparator:
			separators++
		}
	}
	want := []string{"model", "context", "session", "week", "week_top", "reset"}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("the default line shows %v, want %v", values, want)
	}
	if separators != 4 {
		t.Fatalf("the default line has %d separators, want 4 (none in front of the reset)", separators)
	}
	for _, entry := range entries {
		if entry.Kind != KindValue {
			continue
		}
		value, _ := ValueByID(entry.Value)
		if value.format != asPercent {
			continue
		}
		if got := Pick(entry.Thresholds, 49); got != "green" {
			t.Errorf("%s is %q at 49, want green", entry.Value, got)
		}
		if got := Pick(entry.Thresholds, 50); got != "yellow" {
			t.Errorf("%s is %q at 50, want yellow", entry.Value, got)
		}
		if got := Pick(entry.Thresholds, 80); got != "red" {
			t.Errorf("%s is %q at 80, want red", entry.Value, got)
		}
	}
	// The time to the reset is a length of time and therefore a number, and
	// one bound at zero is how today's line wears blue all the way.
	reset := entries[len(entries)-1]
	if reset.Value != "reset" || Pick(reset.Thresholds, 0) != "blue" || Pick(reset.Thresholds, 99999) != "blue" {
		t.Fatalf("the reset entry is %+v, want blue whatever it says", reset)
	}
}

// The default configuration is the line above and off: taking over a status
// line somebody else set is not a default anybody asked for.
func TestDefaultConfigIsOff(t *testing.T) {
	if DefaultConfig().Enabled {
		t.Fatal("a fresh install sets the status line without being asked")
	}
	if len(DefaultConfig().Entries) == 0 {
		t.Fatal("a fresh install offers no line to switch on")
	}
}

func TestDecodeAndEncodeRoundTrip(t *testing.T) {
	config := Config{Enabled: true, Entries: Normalize(Defaults())}
	back, err := Decode(Encode(config))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	back.Entries = Normalize(back.Entries)
	if !reflect.DeepEqual(back, config) {
		t.Fatalf("the round trip changed the configuration:\n%+v\n%+v", back, config)
	}
	// The switch and the list are apart: off keeps every entry.
	off := Config{Enabled: false, Entries: Normalize(Defaults())}
	kept, err := Decode(Encode(off))
	if err != nil || kept.Enabled || len(kept.Entries) != len(off.Entries) {
		t.Fatalf("switching off lost the list: %+v, %v", kept, err)
	}
	if empty, err := Decode(""); err != nil || empty.Enabled || len(empty.Entries) == 0 {
		t.Fatalf("an empty value decodes to %+v, %v", empty, err)
	}
	if _, err := Decode("{"); err == nil {
		t.Fatal("a damaged value decodes without an error")
	}
}
