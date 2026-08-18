package statusline

import (
	"reflect"
	"strings"
	"testing"
)

func valuesOf(entries []Entry) []string {
	var values []string
	for _, entry := range entries {
		if entry.Kind == KindValue {
			values = append(values, entry.Value)
		}
	}
	return values
}

func labelsOf(entries []Entry) string {
	var labels strings.Builder
	for _, entry := range entries {
		labels.WriteString(entry.Label)
	}
	return labels.String()
}

// Every preset is a line the list can hold as it stands: nothing in it is
// dropped on the way in, and the ids a link carries are unique.
func TestPresetsAreLinesTheListKeeps(t *testing.T) {
	ids := map[string]bool{}
	for _, preset := range Presets {
		if ids[preset.ID] || preset.Title == "" || preset.Description == "" {
			t.Errorf("preset %q is twice in the list or misses its title or description", preset.ID)
		}
		ids[preset.ID] = true
		raw := preset.entries()
		if len(Normalize(raw)) != len(raw) {
			t.Errorf("%s loses an entry on its way into the list", preset.ID)
		}
		if got, ok := PresetByID(preset.ID); !ok || got.Title != preset.Title {
			t.Errorf("%s is not found by its id", preset.ID)
		}
	}
	if _, ok := PresetByID("nothing"); ok {
		t.Fatal("an unknown preset was found")
	}
}

// The default line is the one for everyone, and it asks no network: that is
// the subscription line's alone.
func TestDefaultsAreTheLineForEveryone(t *testing.T) {
	if !SameLine(Defaults(), Presets[0].Entries()) || Presets[0].ID != "everyone" {
		t.Fatal("the default line is not the one for everyone")
	}
	for _, preset := range Presets {
		asks := false
		for _, value := range valuesOf(preset.Entries()) {
			if v, _ := ValueByID(value); v.source == fromUsage {
				asks = true
			}
		}
		if asks != (preset.ID == "subscription") {
			t.Errorf("%s asks the usage API: %v", preset.ID, asks)
		}
	}
}

// The lines are the user's own: one character per label, the same characters
// for the same values in every preset, and one reset per line. A phone shows
// some forty columns.
func TestPresetsSpeakTheSameShortLabels(t *testing.T) {
	want := map[string][]string{
		"everyone":     {"model", "context", "cache_left", "session", "week"},
		"api":          {"model", "context", "cache_left", "cost", "over_200k"},
		"subscription": {"model", "context", "session", "week", "week_top", "reset"},
	}
	labels := map[string]string{"everyone": "c◔5w", "api": "c◔", "subscription": "c5wF↻"}
	for _, preset := range Presets {
		entries := preset.Entries()
		if got := valuesOf(entries); !reflect.DeepEqual(got, want[preset.ID]) {
			t.Errorf("%s shows %v, want %v", preset.ID, got, want[preset.ID])
		}
		if got := labelsOf(entries); got != labels[preset.ID] {
			t.Errorf("%s labels its values %q, want %q", preset.ID, got, labels[preset.ID])
		}
	}
	// Every number of a line is a share with the same bounds, but the cache
	// and the reset, which are lengths of time.
	for _, preset := range Presets {
		for _, entry := range preset.Entries() {
			value, _ := ValueByID(entry.Value)
			if entry.Kind != KindValue || !value.Numeric || entry.Value == "cache_left" || entry.Value == "reset" || entry.Value == "cost" {
				continue
			}
			if want := []Threshold{{At: 0, Color: "green"}, {At: 50, Color: "yellow"}, {At: 80, Color: "red"}}; !reflect.DeepEqual(entry.Thresholds, want) {
				t.Errorf("%s: %s has bounds %+v", preset.ID, entry.Value, entry.Thresholds)
			}
		}
	}
}

// The default line on a session that has answered once but caches nothing and
// sits on an API key: model and context, nothing that would claim a plan.
func TestTheDefaultLineShrinksToWhatALoginHas(t *testing.T) {
	api := `{"model": {"display_name": "Opus 5.5"}, "context_window": {"used_percentage": 12}}`
	if out := draw(t, Defaults(), api); out != "\x1b[36mOpus 5.5\x1b[0m \x1b[2m·\x1b[0m \x1b[2mc\x1b[0m \x1b[32m12%\x1b[0m\n" {
		t.Fatalf("an API key's default line is %q", out)
	}
}

func TestSameLineIgnoresWhatNormalizeDrops(t *testing.T) {
	saved := append(Presets[1].entries(), Entry{Kind: KindValue, Value: "gone-since"})
	if !SameLine(saved, Presets[1].Entries()) {
		t.Fatal("a saved line with an entry nobody offers anymore is not the preset it was")
	}
	if SameLine(Presets[0].Entries(), Presets[1].Entries()) {
		t.Fatal("two presets read as the same line")
	}
}
