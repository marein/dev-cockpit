package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/coder/claude/statusline"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// statusLineForm posts the status line page's form the way the page does.
func statusLineForm(t *testing.T, form url.Values) ([]statusline.Entry, error) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/settings/coders/claude/statusline", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request = req
	return statusLineEntriesFromForm(c)
}

func TestStatusLineFormReadsARowWithItsBounds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	entries, err := statusLineForm(t, url.Values{
		"entry_kind":        {"value", "separator"},
		"entry_value":       {"context", "model"},
		"entry_label":       {"c", ""},
		"entry_label_color": {"dim", "default"},
		"entry_color":       {"", ""},
		"entry_text":        {"", "|"},
		"entry_thresholds":  {"2", "0"},
		"threshold_at":      {"0", "80"},
		"threshold_color":   {"green", "red"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Value != "context" || entries[1].Kind != statusline.KindSeparator {
		t.Fatalf("the form answered %+v", entries)
	}
	if len(entries[0].Thresholds) != 2 || entries[0].Thresholds[1].Color != "red" {
		t.Fatalf("the bounds are %+v", entries[0].Thresholds)
	}
	if entries[1].Text != "|" {
		t.Fatalf("the separator is %q", entries[1].Text)
	}
}

// A bound that is not a finite number is refused, and the reason is the whole
// stored answer: json.Marshal refuses NaN and both infinities outright, so a
// configuration carrying one encodes to nothing at all and the store would take
// that nothing as the setting, list and mode with it. strconv.ParseFloat
// takes all three without complaining, so the check has to be here.
func TestStatusLineFormRefusesABoundThatIsNoNumber(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, raw := range []string{"NaN", "nan", "Inf", "-Inf", "infinity", "twelve"} {
		_, err := statusLineForm(t, url.Values{
			"entry_kind":       {"value"},
			"entry_value":      {"context"},
			"entry_thresholds": {"1"},
			"threshold_at":     {raw},
			"threshold_color":  {"green"},
		})
		if err == nil {
			t.Errorf("a bound of %q was taken", raw)
		}
	}
	entries, err := statusLineForm(t, url.Values{
		"entry_kind":       {"value"},
		"entry_value":      {"context"},
		"entry_thresholds": {"1"},
		"threshold_at":     {"49.5"},
		"threshold_color":  {"green"},
	})
	if err != nil || len(entries) != 1 || entries[0].Thresholds[0].At != 49.5 {
		t.Fatalf("a fractional bound answered %+v, %v", entries, err)
	}
	// The whole point of the refusal: everything that survives it encodes.
	if statusline.Encode(statusline.Config{Mode: statusline.ModeAlways, Entries: entries}) == "" {
		t.Fatal("a configuration the form accepted cannot be stored")
	}
}

// Every count and length the form carries is bounded, and a refusal is short
// enough for the flash, which rides the session cookie: a bound quoted uncut
// once overflowed it and ended the save in a 500.
func TestStatusLineFormRefusesWhatIsPastItsBounds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	form := func(edit func(url.Values)) url.Values {
		values := url.Values{
			"entry_kind":       {"value"},
			"entry_value":      {"context"},
			"entry_label":      {"c"},
			"entry_text":       {""},
			"entry_thresholds": {"1"},
			"threshold_at":     {"0"},
			"threshold_color":  {"green"},
		}
		edit(values)
		return values
	}
	past := map[string]url.Values{
		"entries": form(func(f url.Values) { f["entry_kind"] = slices.Repeat([]string{"break"}, statusline.MaxEntries+1) }),
		"label":   form(func(f url.Values) { f.Set("entry_label", strings.Repeat("ä", statusline.MaxLabelRunes+1)) }),
		"text":    form(func(f url.Values) { f.Set("entry_text", strings.Repeat("c", statusline.MaxTextRunes+1)) }),
		"bounds": form(func(f url.Values) {
			f.Set("entry_thresholds", strconv.Itoa(statusline.MaxBounds+1))
			f["threshold_at"] = slices.Repeat([]string{"1"}, statusline.MaxBounds+1)
			f["threshold_color"] = slices.Repeat([]string{"green"}, statusline.MaxBounds+1)
		}),
		"bound length": form(func(f url.Values) { f.Set("threshold_at", "1"+strings.Repeat("0", statusline.MaxBoundLength)) }),
		"long word":    form(func(f url.Values) { f.Set("threshold_at", strings.Repeat("x", 2000)) }),
	}
	for name, values := range past {
		_, err := statusLineForm(t, values)
		if err == nil {
			t.Errorf("%s: the form took it", name)
			continue
		}
		if len(err.Error()) > 120 {
			t.Errorf("%s: the refusal is %d bytes long", name, len(err.Error()))
		}
	}

	at := form(func(f url.Values) {
		f["entry_kind"] = slices.Repeat([]string{"value"}, statusline.MaxEntries)
		f["entry_value"] = slices.Repeat([]string{"context"}, statusline.MaxEntries)
		f["entry_label"] = slices.Repeat([]string{strings.Repeat("ä", statusline.MaxLabelRunes)}, statusline.MaxEntries)
		f["entry_text"] = slices.Repeat([]string{strings.Repeat("c", statusline.MaxTextRunes)}, statusline.MaxEntries)
		f["entry_thresholds"] = append([]string{strconv.Itoa(statusline.MaxBounds)}, slices.Repeat([]string{"0"}, statusline.MaxEntries-1)...)
		f["threshold_at"] = slices.Repeat([]string{"1" + strings.Repeat("0", statusline.MaxBoundLength-1)}, statusline.MaxBounds)
		f["threshold_color"] = slices.Repeat([]string{"green"}, statusline.MaxBounds)
	})
	entries, err := statusLineForm(t, at)
	if err != nil {
		t.Fatalf("a list right at every bound was refused: %v", err)
	}
	if len(entries) != statusline.MaxEntries || len(entries[0].Thresholds) != statusline.MaxBounds {
		t.Fatalf("a list right at every bound came back with %d entries, %d bounds", len(entries), len(entries[0].Thresholds))
	}
}

// A separator and the free text stand on the line as typed, so the save holds
// them to a part of one line and says so at their row, which is where the page
// shows it even on a folded entry. A command keeps the whole length, and a
// text a hidden field of another kind still posts is not judged by it.
func TestStatusLineFormHoldsShownTextToAPartOfTheLine(t *testing.T) {
	gin.SetMode(gin.TestMode)
	form := func(kind, value, text string) url.Values {
		return url.Values{
			"entry_kind":       {"break", kind},
			"entry_value":      {"", value},
			"entry_text":       {"", text},
			"entry_thresholds": {"0", "0"},
		}
	}
	at := strings.Repeat("ä", statusline.MaxShownTextRunes)
	past := at + "ä"
	for name, kind := range map[string][2]string{
		"separator": {string(statusline.KindSeparator), ""},
		"free text": {string(statusline.KindValue), statusline.FreeTextValue},
	} {
		_, err := statusLineForm(t, form(kind[0], kind[1], past))
		if state := render.NewStatusLineState("/s", nil, nil, err); err == nil || state.ErrorRow != 1 {
			t.Errorf("%s past the length: the refusal %v stands at row %d, want 1", name, err, state.ErrorRow)
		}
		if entries, err := statusLineForm(t, form(kind[0], kind[1], at)); err != nil || len(entries) != 2 || entries[1].Text != at {
			t.Errorf("%s at the length answered %+v, %v", name, entries, err)
		}
	}
	for name, kind := range map[string][2]string{
		"command":          {string(statusline.KindValue), statusline.CommandValue},
		"break":            {string(statusline.KindBreak), statusline.FreeTextValue},
		"value of no text": {string(statusline.KindValue), "context"},
	} {
		if _, err := statusLineForm(t, form(kind[0], kind[1], past)); err != nil {
			t.Errorf("%s with a text past a part of the line was refused: %v", name, err)
		}
	}
}

// A refusal one row caused names that row, counted the way the rows stand on
// the page, so the page says it there; one about the whole list names none.
func TestStatusLineFormRefusalNamesItsRow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, err := statusLineForm(t, url.Values{
		"entry_kind":       {"separator", "value"},
		"entry_value":      {"", "context"},
		"entry_text":       {"|", ""},
		"entry_thresholds": {"0", "1"},
		"threshold_at":     {"x"},
		"threshold_color":  {"green"},
	})
	if state := render.NewStatusLineState("/s", nil, nil, err); err == nil || state.ErrorRow != 1 {
		t.Fatalf("the refusal %v stands at row %d, want 1", err, state.ErrorRow)
	}
	_, err = statusLineForm(t, url.Values{"entry_kind": slices.Repeat([]string{"break"}, statusline.MaxEntries+1)})
	if state := render.NewStatusLineState("/s", nil, nil, err); err == nil || state.ErrorRow != -1 {
		t.Fatalf("the refusal %v stands at row %d, want the list", err, state.ErrorRow)
	}
}
