package render

import (
	"regexp"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/pluginhost"
	"github.com/marein/dev-cockpit/plugin"
)

// statusPlugin adds one item to the end of the status line.
type statusPlugin struct{}

func (statusPlugin) ConfigureServe(s plugin.Serve) error {
	s.AddSlotHTML(plugin.SlotStatusEnd, "<demo-item></demo-item>")
	return nil
}

// A plugin's item stands where the gap pushes it, right before the version,
// and a build without one renders nothing between the two.
func TestStatusEndSlotStandsBetweenTheGapAndTheVersion(t *testing.T) {
	render := func(serves []*pluginhost.Serve) string {
		tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", serves, nil)
		var b strings.Builder
		if err := tmpl.ExecuteTemplate(&b, "shell_status.gohtml", Page{User: "admin"}); err != nil {
			t.Fatalf("render status line: %v", err)
		}
		return b.String()
	}
	serves, err := pluginhost.ConfigureServe([]plugin.Named[plugin.ServePlugin]{{ID: "demo", Plugin: statusPlugin{}}}, t.TempDir(), t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("configure serve: %v", err)
	}
	with := render(serves)
	if !regexp.MustCompile(`<span class="dc-status-gap"></span>\s*<demo-item></demo-item>\s*<span class="dc-status-item" title="Version">`).MatchString(with) {
		t.Fatalf("the item does not stand between the gap and the version:\n%s", with)
	}
	if n := strings.Count(with, "<demo-item>"); n != 1 {
		t.Fatalf("the item renders %d times", n)
	}
	without := render(nil)
	if !regexp.MustCompile(`<span class="dc-status-gap"></span>\s*<span class="dc-status-item" title="Version">`).MatchString(without) {
		t.Fatalf("without a plugin something stands between the gap and the version:\n%s", without)
	}
}
