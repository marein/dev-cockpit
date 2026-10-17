package web

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/settings"
)

// The links of a guest's status line are the addresses of the running stacks
// of its link's projects alone, the page's project unfolded, pulled under the
// link's address like the owner's, the page named by its path below it.
func TestAGuestsStatusLinksHoldItsLinksProjectsAlone(t *testing.T) {
	f := newGuestFixture(t)
	f.s.settings = settings.New(filepath.Join(t.TempDir(), "settings.json"))
	f.s.docker = docker.NewService(t.TempDir(), func() string { return "" })
	f.s.docker.SetStateForTest(docker.State{Available: true, Host: "tcp://fake:1", Containers: []docker.Container{
		{Name: "web", Service: "web", State: "running", WorkingDir: filepath.Join(f.root, "shop"), Ports: []docker.Port{{Public: 8080, Private: 80}}},
		{Name: "db", Service: "db", State: "running", WorkingDir: filepath.Join(f.root, "other"), Ports: []docker.Port{{Public: 5432, Private: 5432}}},
	}})
	f.r.GET(sharePathPrefix+":token/docker/links", f.s.openShare, f.s.handleStatusLinks)
	page := url.QueryEscape("/projects/shop/editor")
	rec := f.do(http.MethodGet, sharePathPrefix+f.token+"/docker/links?path="+page, "", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `data-links-project="shop" data-links-active="false" data-links-used="0"`) || !strings.Contains(body, ":8080") || !strings.Contains(body, `aria-expanded="true"`) {
		t.Fatalf("the guest's links answered %d: %s", rec.Code, body)
	}
	if strings.Contains(body, "other") || strings.Contains(body, "5432") {
		t.Fatalf("the guest's links name a project off the link: %s", body)
	}
	if links := f.s.guestStatusLinks(&guest{share: f.created}, ""); links.Links != 1 || len(links.Projects) != 1 || links.Projects[0].Name != "shop" {
		t.Fatalf("the guest's page links read %+v", links)
	}
}
