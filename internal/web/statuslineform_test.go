package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/coder/claude/statusline"
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
// that nothing as the setting, list and switch with it. strconv.ParseFloat
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
	// And what is a number still goes through, fractions included.
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
	if statusline.Encode(statusline.Config{Enabled: true, Entries: entries}) == "" {
		t.Fatal("a configuration the form accepted cannot be stored")
	}
}
