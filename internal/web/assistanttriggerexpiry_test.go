package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
)

// The form asks for the expiry as a number with a unit beside it, so it can
// say everything `--until` can say: every value those two fields produce is a
// span the command takes, and No expiry stands in that same select as the
// `never` a command writes. A request that names neither leaves the expiry
// that stands, which is what makes one reading serve a create and a change
// alike.
func TestTheFormsExpiryIsANumberWithAUnit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		form  url.Values
		until time.Duration
		never bool
	}{
		{"minutes", url.Values{"until": {"90"}, "untilUnit": {"m"}}, 90 * time.Minute, false},
		{"hours", url.Values{"until": {"8"}, "untilUnit": {"h"}}, 8 * time.Hour, false},
		{"days", url.Values{"until": {"3"}, "untilUnit": {"d"}}, 72 * time.Hour, false},
		// The select says there is none, and it says so over the number: that
		// one is disabled while the entry stands, and a request carrying a
		// number anyway is answered by the select.
		{"the entry", url.Values{"untilUnit": {"never"}}, 0, true},
		{"the entry over a number", url.Values{"untilUnit": {"never"}, "until": {"90"}}, 0, true},
		// What a command writes, one word and no unit beside it.
		{"a command's span", url.Values{"until": {"8h"}}, 8 * time.Hour, false},
		{"a command's never", url.Values{"until": {"never"}}, 0, true},
		// Nobody named one: a create takes no expiry and a change leaves the
		// one that stands.
		{"nobody named one", url.Values{}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := triggerSpecOf(t, tc.form)
			if spec.Until != tc.until || spec.Never != tc.never {
				t.Fatalf("%v reads as until %s, never %v; want %s, %v", tc.form, spec.Until, spec.Never, tc.until, tc.never)
			}
		})
	}
	// A number and a unit that do not make a span are refused where they were
	// typed, never stored as something else.
	if _, err := triggerSpec(url.Values{"until": {"8h"}, "untilUnit": {"m"}}); err == nil {
		t.Fatalf("a span with a unit welded onto it has to be refused")
	}
}

// The form opens on what the trigger has left, in the unit that keeps that
// number whole: what is stored is a moment while the field asks for a span, so
// an edit that moves the task re-posts the expiry instead of dropping it, and
// 90 minutes reads as 90 minutes and never as 1.5 hours.
func TestTheFormsExpiryOpensOnWhatIsLeft(t *testing.T) {
	now := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		left  time.Duration
		count int
		unit  string
	}{

		{90 * time.Minute, 90, "m"},
		{2 * time.Hour, 2, "h"},
		{24 * time.Hour, 1, "d"},
		{7 * 24 * time.Hour, 7, "d"},
		// Seconds are rounded to the minute the field asks for, and what is
		// nearly over still reads as a minute rather than as no expiry.
		{8*time.Hour - 10*time.Second, 8, "h"},
		{20 * time.Second, 1, "m"},
	} {
		count, unit := triggerSpan(now.Add(tc.left), now)
		if count != tc.count || unit != tc.unit {
			t.Fatalf("%s left reads as %d%s, want %d%s", tc.left, count, unit, tc.count, tc.unit)
		}
	}
	// No expiry and one that has run out are the same empty number with the
	// select on No expiry.
	for _, until := range []time.Time{{}, now.Add(-time.Hour)} {
		if count, unit := triggerSpan(until, now); count != 0 || unit != triggerNoExpiry {
			t.Fatalf("%s reads as %d%s, want an empty field on No expiry", until, count, unit)
		}
	}
}

func triggerSpecOf(t *testing.T, form url.Values) assistant.TriggerSpec {
	t.Helper()
	spec, err := triggerSpec(form)
	if err != nil {
		t.Fatalf("the form %v is refused: %v", form, err)
	}
	return spec
}

// triggerSpec reads a posted form the way the route does, on a job event so
// nothing has to be looked up about a terminal.
func triggerSpec(form url.Values) (assistant.TriggerSpec, error) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, assistantTriggersPath, strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return (&Server{}).assistantTriggerSpec(c, assistant.EventJob, nil)
}
