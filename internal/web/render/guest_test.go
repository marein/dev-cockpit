package render

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/hostinfo"
	"github.com/marein/dev-cockpit/internal/share"
)

func renderPart(t *testing.T, name string, data any) string {
	t.Helper()
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, name, data); err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return out.String()
}

func guestOf(kinds ...share.Kind) Access {
	sh := share.Share{}
	for _, kind := range kinds {
		sh.Resources = append(sh.Resources, share.Resource{Kind: kind, Target: "t-" + string(kind)})
	}
	return GuestAccess("/share/tok", sh, "")
}

// A guest's status line is the owner's server reading, the links of its
// link's stacks and the version; the spend, the songs and the update check
// stay the owner's.
func TestAGuestsStatusLineIsTheServerTheLinksAndTheVersion(t *testing.T) {
	links := StatusLinks{Links: 1, Projects: []StatusLinksProject{{Name: "shop", Links: 1, Open: true, Stacks: []StatusLinksStack{{Links: []docker.Link{{Scheme: "http", Port: 8080}}}}}}}
	host := hostinfo.Stats{HasMem: true, MemPercent: 40, HasDisk: true, DiskPercent: 60, HasCPU: true, CPUPercent: 10}
	owner := renderPart(t, "shell_status.gohtml", Page{Host: host, Links: links})
	guest := renderPart(t, "shell_status.gohtml", Page{Host: host, Links: links, Access: guestOf(share.KindProject)})
	for _, part := range []string{"<dc-host-status", `data-host-meter="mem"`, `data-host-meter="disk"`, `data-host-meter="cpu"`, `data-links-project="shop"`, "Version test"} {
		if !strings.Contains(guest, part) || !strings.Contains(owner, part) {
			t.Fatalf("%s is not on both status lines:\nowner %s\nguest %s", part, owner, guest)
		}
	}
	for _, part := range []string{"<dc-cost-status", "<dc-song-player", "<dc-update-check"} {
		if !strings.Contains(owner, part) || strings.Contains(guest, part) {
			t.Fatalf("%s belongs to the owner's status line alone:\nowner %s\nguest %s", part, owner, guest)
		}
	}
}

// A guest's list of the link's assistants is the owner's column under the
// link's address, refreshing alike and wearing the working ring, without the
// row menu, the grip, the memory and the control that makes one.
func TestAGuestsAssistantColumnIsTheOwnersWithoutItsControls(t *testing.T) {
	data := assistantCtxData(AssistantCard{ID: "c1", Title: "Live one", URL: "/share/tok/assistants/c1", Running: 1760443200123})
	data.Page = Page{Access: guestOf(share.KindAssistant)}
	data.Path = "/share/tok/assistants/c1"
	data.Available = false
	out := renderAssistantCtx(t, data)
	for _, part := range []string{`list-url="/share/tok/ctx/assistants?path=%2Fshare%2Ftok%2Fassistants%2Fc1"`, `dc-term-icon assistant working" data-working-since="1760443200123"`, `href="/share/tok/assistants/c1"`, "data-assistant-body"} {
		if !strings.Contains(out, part) {
			t.Fatalf("the guest's column lacks %s: %s", part, out)
		}
	}
	for _, part := range []string{" history", "data-assistant-menu", "data-assistant-grip", "data-assistant-memory-button", "New assistant", "No installed coder"} {
		if strings.Contains(out, part) {
			t.Fatalf("the guest's column carries %s: %s", part, out)
		}
	}
}
