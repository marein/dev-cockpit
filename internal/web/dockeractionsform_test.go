package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/docker"
)

// actionsForm posts the docker settings form the way the page does.
func actionsForm(t *testing.T, form url.Values) ([]docker.Action, error) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/settings/docker", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request = req
	return composeActionsFromForm(c)
}

// Removing every row is a real answer: the form then carries no row at all
// and the stored list is empty, which is what leaves the menus without any
// compose command.
func TestComposeActionsFormStoresTheEmptyList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	list, err := actionsForm(t, url.Values{"docker_host": {""}})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("a form whose rows are all gone answered %+v", list)
	}
}

func TestComposeActionsFormReadsRows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	form := url.Values{
		"action_id":      {"up", "", ""},
		"action_icon":    {"start", "purge", ""},
		"action_label":   {"Compose up", "Prune", ""},
		"action_command": {"docker compose up -d", `docker system prune -f`, ""},
		"action_timeout": {"10m", "", ""},
		"action_confirm": {"1", "0", "0"},
	}
	list, err := actionsForm(t, form)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("the form answered %+v", list)
	}
	got := list
	if got[0].ID != "up" || !got[0].Confirm || got[0].Timeout != "10m" {
		t.Fatalf("the first row answered %+v", got[0])
	}
	// A row without an id is named after its label, and the empty row at the
	// end is nobody's entry.
	if got[1].ID != "prune" || got[1].Confirm {
		t.Fatalf("the second row answered %+v", got[1])
	}
}

// The confirm travels by row, not by id: two rows may post the same id, the
// server renames the second, and the tick must stay with the row it was set
// on.
func TestComposeActionsFormKeepsConfirmWithItsRow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	form := url.Values{
		"action_id":      {"same", "same"},
		"action_label":   {"First", "Second"},
		"action_command": {"docker ps", "docker ps -a"},
		"action_confirm": {"0", "1"},
	}
	list, err := actionsForm(t, form)
	if err != nil {
		t.Fatal(err)
	}
	got := list
	if len(got) != 2 || got[0].Confirm || !got[1].Confirm {
		t.Fatalf("the confirm left its row: %+v", got)
	}
	if got[0].ID != "same" || got[1].ID == "same" {
		t.Fatalf("the duplicate id was not renamed: %+v", got)
	}
}

func TestComposeActionsFormRefusesWhatCannotRun(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []url.Values{
		{"action_id": {""}, "action_label": {""}, "action_command": {"docker ps"}},
		{"action_id": {""}, "action_label": {"Broken"}, "action_command": {`docker "ps`}},
		{"action_id": {""}, "action_label": {"Empty"}, "action_command": {"   "}},
		{"action_id": {""}, "action_label": {"Slow"}, "action_command": {"docker ps"}, "action_timeout": {"soon"}},
	}
	for _, form := range cases {
		if _, err := actionsForm(t, form); err == nil {
			t.Fatalf("%v was accepted", form)
		}
	}
}

// linkRulesForm posts the same page's link rule rows.
func linkRulesForm(t *testing.T, form url.Values) ([]docker.LinkRule, error) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/settings/docker", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request = req
	return linkRulesFromForm(c)
}

func TestLinkRulesFormReadsRows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	form := url.Values{
		"link_label":   {"traefik.http.routers.*.rule", "com.example.vhost", ""},
		"link_pattern": {`Host\(\s*(?P<host>[^)]+?)\s*\)`, "", ""},
		"link_scheme":  {"", "https", ""},
		"link_unless":  {"traefik.enable=false", "", ""},
	}
	list, err := linkRulesForm(t, form)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("the form answered %+v", list)
	}
	if list[0].Label != "traefik.http.routers.*.rule" || list[0].Scheme != "" || list[0].Unless != "traefik.enable=false" {
		t.Fatalf("the first row answered %+v", list[0])
	}
	// A rule needs no pattern at all, and the empty row at the end is nobody's.
	if list[1].Label != "com.example.vhost" || list[1].Pattern != "" || list[1].Scheme != "https" {
		t.Fatalf("the second row answered %+v", list[1])
	}
}

// Taking every row away is a real answer: the menus then offer the published
// ports and nothing else.
func TestLinkRulesFormStoresTheEmptyList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	list, err := linkRulesForm(t, url.Values{"docker_host": {""}})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("a form whose rows are all gone answered %+v", list)
	}
}

// A pattern nobody can compile is refused where it is typed rather than
// stored and quietly skipped later.
func TestLinkRulesFormRefusesWhatCannotBeRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []url.Values{
		{"link_label": {"com.example.vhost"}, "link_pattern": {"(?P<host>"}},
		{"link_label": {""}, "link_pattern": {"(?P<host>.*)"}},
		{"link_label": {"com.example.vhost"}, "link_scheme": {"ftp"}},
		{"link_label": {"com.example.vhost"}, "link_unless": {"traefik.enable"}},
	}
	for _, form := range cases {
		if _, err := linkRulesForm(t, form); err == nil {
			t.Fatalf("%v was accepted", form)
		}
	}
}
