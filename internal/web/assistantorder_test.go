package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/eventbus"
)

// The list is sorted by hand, not by the clock, and the order lives on the
// server: it comes back on the next reload and on the next device. The route
// takes the ids top first.
func TestTheAssistantOrderIsPostedAndKept(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	assistants, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	s := &Server{assistants: assistants, workspace: workspace, bus: eventbus.New()}
	r := gin.New()
	r.POST("/assistants/order", s.handleAssistantOrder)

	first, _ := assistants.Create("claude")
	second, _ := assistants.Create("claude")
	third, _ := assistants.Create("claude")

	post := func(body string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/assistants/order", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	order := func() []string {
		t.Helper()
		var out []string
		for _, entry := range assistants.List() {
			out = append(out, entry.ID)
		}
		return out
	}

	if code := post(`{"ids":["` + first.ID + `","` + third.ID + `","` + second.ID + `"]}`); code != http.StatusNoContent {
		t.Fatalf("the order answered %d", code)
	}
	if got := strings.Join(order(), " "); got != first.ID+" "+third.ID+" "+second.ID {
		t.Fatalf("want the posted order kept, got %s", got)
	}
	// The same server reading its state again is what a restart sees.
	again, _, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant again: %v", err)
	}
	var restarted []string
	for _, entry := range again.List() {
		restarted = append(restarted, entry.ID)
	}
	if got := strings.Join(restarted, " "); got != first.ID+" "+third.ID+" "+second.ID {
		t.Fatalf("want the order to survive a restart, got %s", got)
	}

	if code := post(`not json`); code != http.StatusBadRequest {
		t.Fatalf("a broken order answered %d", code)
	}
	long := make([]string, maxAssistantOrderIDs+1)
	for i := range long {
		long[i] = `"` + first.ID + `"`
	}
	if code := post(`{"ids":[` + strings.Join(long, ",") + `]}`); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized order answered %d", code)
	}
	if got := strings.Join(order(), " "); got != first.ID+" "+third.ID+" "+second.ID {
		t.Fatalf("a refused order changed the list: %s", got)
	}
}
