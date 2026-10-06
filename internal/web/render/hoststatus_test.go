package render

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/hostinfo"
)

// Every bar is green while quiet and takes yellow and red at the same
// thresholds the client paints with.
func TestHostBarClassIsGreenBelowWarn(t *testing.T) {
	cases := map[int]string{
		0:                 "bg-green",
		hostinfo.Warn - 1: "bg-green",
		hostinfo.Warn:     "bg-yellow",
		hostinfo.Crit - 1: "bg-yellow",
		hostinfo.Crit:     "bg-red",
		140:               "bg-red",
	}
	for percent, want := range cases {
		if got := HostBarClass(percent); got != want {
			t.Errorf("HostBarClass(%d) = %q, want %q", percent, got, want)
		}
	}
}

// The status line carries one bar per metric, named with its value, a metric
// the machine cannot answer is left out, and a load past the cores keeps its
// number while the bar stops at full.
func TestHostStatusBarRendersThreeNamedBars(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	var out strings.Builder
	err := tmpl.ExecuteTemplate(&out, "host_status_bar.gohtml", map[string]any{
		"Host": hostinfo.Stats{
			HasCPU: true, HasMem: true, HasDisk: false,
			CPUPercent: 140, MemPercent: 84, DiskPercent: -1,
			CPULabel: "Load 11.2 on 8 cores", MemLabel: "13 GB of 16 GB used",
		},
	})
	if err != nil {
		t.Fatalf("render status bar: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		`data-host-meter="cpu"`, `aria-label="CPU 140%"`, `title="CPU 140% · Load 11.2 on 8 cores"`, `style="width: 100%"`,
		`data-host-meter="mem"`, `aria-label="RAM 84%"`, `bg-yellow`,
		`data-host-meter="disk"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("status bar misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ti-server") || strings.Contains(got, "data-host-float") {
		t.Errorf("status bar still carries the server icon or the float:\n%s", got)
	}
	disk := cut(t, got, `data-host-meter="disk"`, ">")
	if !strings.Contains(disk, "hidden") {
		t.Errorf("an unanswered metric keeps its bar: %s", disk)
	}
}

// The phone's work head carries no tools any more, and its Cockpit tab wears
// the gauge, colored by the worst reading, and the dot instead of the
// Settings entry.
func TestThePhoneMovesItsToolsIntoCockpit(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	for _, gone := range []string{"host_status_float.gohtml", "shell_head_tools.gohtml"} {
		if tmpl.Lookup(gone) != nil {
			t.Fatalf("the template %s still exists", gone)
		}
	}
	var out strings.Builder
	err := tmpl.ExecuteTemplate(&out, "shell_tabbar.gohtml", Page{
		User: "admin",
		Host: hostinfo.Stats{HasCPU: true, HasMem: true, HasDisk: true, CPUPercent: 10, MemPercent: 84, DiskPercent: 97},
	})
	if err != nil {
		t.Fatalf("render tab bar: %v", err)
	}
	got := out.String()
	if strings.Contains(got, `data-ctx-area="settings"`) || strings.Contains(got, "dc-notifications") {
		t.Fatalf("the tab bar still carries Settings or a bell:\n%s", got)
	}
	cockpit := cut(t, got, `data-ctx-area="cockpit"`, "</button>")
	for _, want := range []string{
		"ti-dashboard", "text-red", `title="Server critical, Disk 97%"`, `aria-label="Cockpit, server critical"`, `data-cockpit-dot`, ">Cockpit<",
	} {
		if !strings.Contains(cockpit, want) {
			t.Errorf("the Cockpit tab misses %q:\n%s", want, cockpit)
		}
	}
	if strings.Contains(cockpit, "data-host-meter") {
		t.Errorf("the Cockpit tab still carries the bars:\n%s", cockpit)
	}
}

// The gauge keeps the tab's color while every reading is quiet and takes
// yellow and red by the worst one, named in its label.
func TestHostTabNamesTheWorstReading(t *testing.T) {
	cases := []struct {
		stats        hostinfo.Stats
		class, label string
	}{
		{hostinfo.Stats{}, "", ""},
		{hostinfo.Stats{HasCPU: true, HasMem: true, HasDisk: true, CPUPercent: 12, MemPercent: 79, DiskPercent: 40}, "", ""},
		{hostinfo.Stats{HasCPU: true, HasMem: true, CPUPercent: 12, MemPercent: 84}, "text-yellow", "Server busy, RAM 84%"},
		{hostinfo.Stats{HasCPU: true, HasDisk: true, CPUPercent: 140, DiskPercent: 97}, "text-red", "Server critical, CPU 140%"},
		{hostinfo.Stats{HasMem: false, MemPercent: 99, HasDisk: true, DiskPercent: 50}, "", ""},
	}
	for _, tc := range cases {
		if got := HostTabClass(tc.stats); got != tc.class {
			t.Errorf("HostTabClass(%+v) = %q, want %q", tc.stats, got, tc.class)
		}
		if got := HostTabLabel(tc.stats); got != tc.label {
			t.Errorf("HostTabLabel(%+v) = %q, want %q", tc.stats, got, tc.label)
		}
	}
}

// The Cockpit sheet stands in one order, server, news, the three quick actions,
// the settings, and the server rows are its read only ones.
func TestTheCockpitSheetOrder(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	var out strings.Builder
	err := tmpl.ExecuteTemplate(&out, "ctx_cockpit.gohtml", SettingsGeneralData{Page: Page{
		User:      "admin",
		CSRFToken: "t",
		Host:      hostinfo.Stats{HasCPU: true, CPUPercent: 23, CPULabel: "Usage across 8 cores"},
	}})
	if err != nil {
		t.Fatalf("render Cockpit sheet: %v", err)
	}
	got := out.String()
	last := -1
	for _, section := range []string{"server", "news", "actions", "settings"} {
		at := strings.Index(got, `data-cockpit-section="`+section+`"`)
		if at < 0 || at < last {
			t.Fatalf("section %s out of order (%d after %d):\n%s", section, at, last, got)
		}
		last = at
	}
	for _, want := range []string{`limit="3"`, `data-ctx-area="news"`, "data-theme-cycle", "data-cockpit-update", `action="/logout"`, `href="/settings/general"`, "Usage across 8 cores"} {
		if !strings.Contains(got, want) {
			t.Errorf("the Cockpit sheet misses %q", want)
		}
	}
	server := cut(t, got, `data-cockpit-section="server"`, `data-cockpit-section="news"`)
	for _, control := range []string{"<button", "<a ", "data-bs-toggle"} {
		if strings.Contains(server, control) {
			t.Errorf("the server rows carry a control %q", control)
		}
	}
}

// The update tile renders Up to date and pressable, like the footer's version
// link, so a tap runs the same forced check in every state.
func TestTheCockpitSheetUpdateTileStartsUpToDate(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "ctx_cockpit.gohtml", SettingsGeneralData{Page: Page{User: "admin"}}); err != nil {
		t.Fatalf("render Cockpit sheet: %v", err)
	}
	tile := cut(t, out.String(), "data-cockpit-update", "</button>")
	if strings.Contains(tile, "disabled") || !strings.Contains(tile, "Up to date") || !strings.Contains(tile, "data-update-open") {
		t.Fatalf("the update tile does not start as a pressable Up to date:\n%s", tile)
	}
}

// The Cockpit tab wears one plain dot, no number, for an unread notification or
// an open backup review, and its label says so; with neither it stands bare.
// The sheet's Settings head counts the reviews the way the old tab did.
func TestTheCockpitDotFollowsNewsAndReviews(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	for _, tc := range []struct {
		name            string
		unread, reviews int
		dot             bool
	}{
		{"none", 0, 0, false},
		{"unread only", 2, 0, true},
		{"reviews only", 0, 3, true},
		{"both", 1, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			page := Page{User: "admin", UnreadNews: tc.unread, BackupReviewCount: tc.reviews}
			if err := tmpl.ExecuteTemplate(&out, "shell_tabbar.gohtml", page); err != nil {
				t.Fatalf("render tab bar: %v", err)
			}
			tab := cut(t, out.String(), `data-ctx-area="cockpit"`, "</button>")
			dot := cut(t, tab, "status-dot", ">")
			if hidden := strings.Contains(dot, "d-none"); hidden == tc.dot {
				t.Errorf("dot shown %v, want %v: %s", !hidden, tc.dot, dot)
			}
			if labelled := strings.Contains(tab, `aria-label="Cockpit, news"`); labelled != tc.dot {
				t.Errorf("tab labelled %v, want %v: %s", labelled, tc.dot, tab)
			}
			if strings.Contains(tab, "data-backup-reviews") || strings.Contains(tab, "dc-tabbar-count") {
				t.Errorf("the tab carries a number: %s", tab)
			}
		})
	}
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "ctx_cockpit.gohtml", SettingsGeneralData{Page: Page{User: "admin", BackupReviewCount: 2}, SettingsNav: SettingsNav{Reviews: 2}}); err != nil {
		t.Fatalf("render Cockpit sheet: %v", err)
	}
	head := cut(t, out.String(), `data-cockpit-section="settings"`, "</div>")
	if !strings.Contains(head, `aria-label="2 backup files to resolve"`) || !strings.Contains(head, ">2<") || strings.Contains(head, "d-none") {
		t.Errorf("the Settings head misses the review count:\n%s", head)
	}
}

// cut returns got from the first from up to the first to after it, and fails
// with the text it searched when either is missing.
func cut(t *testing.T, got, from, to string) string {
	t.Helper()
	start := strings.Index(got, from)
	if start < 0 {
		t.Fatalf("%q not found in:\n%s", from, got)
	}
	rest := got[start:]
	end := strings.Index(rest, to)
	if end < 0 {
		t.Fatalf("%q not found after %q in:\n%s", to, from, rest)
	}
	return rest[:end]
}

// The tab's name carries the gauge's state and the dot's news together, so
// the news never takes the server's state out of it, and stays unset while
// there is neither.
func TestCockpitLabelNamesTheServerAndTheNews(t *testing.T) {
	busy := hostinfo.Stats{HasMem: true, MemPercent: 84}
	crit := hostinfo.Stats{HasDisk: true, DiskPercent: 97}
	cases := []struct {
		page Page
		want string
	}{
		{Page{}, ""},
		{Page{UnreadNews: 1}, "Cockpit, news"},
		{Page{Host: busy}, "Cockpit, server busy"},
		{Page{Host: crit, BackupReviewCount: 2}, "Cockpit, server critical, news"},
	}
	for _, tc := range cases {
		if got := tc.page.CockpitLabel(); got != tc.want {
			t.Errorf("CockpitLabel() = %q, want %q", got, tc.want)
		}
	}
}
