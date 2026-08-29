package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/coder"
	codercopilot "github.com/marein/dev-cockpit/internal/coder/copilot"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/recent"
	"github.com/marein/dev-cockpit/internal/restore"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// A rename inside a coder is a change the cockpit did not make, but it is not a
// kind of its own: it travels the one terminals event every start, stop and
// reorder travels, named by its project. A surface can therefore follow a
// rename without knowing that renames exist, which is the whole reason nothing
// polls for one.
func TestPublishTerminalsFromOutsideIsTheOrdinaryTerminalsEvent(t *testing.T) {
	projects := project.NewRepository(t.TempDir(), nil)
	shells := shell.NewShells(config.Config{}, tmux.New(), projects, nil)
	s := &Server{
		bus: eventbus.New(),
		restorer: restore.New(filepath.Join(t.TempDir(), "terminals.json"),
			func() bool { return false }, nil, shells, tmux.New(), nil, nil, nil),
	}
	events, cancel := s.bus.Subscribe()
	defer cancel()

	s.PublishTerminals("app")

	select {
	case ev := <-events:
		if ev.Type != "terminals" {
			t.Fatalf("a rename got an event type of its own: %q", ev.Type)
		}
		data, ok := ev.Data.(map[string]string)
		if !ok || data["project"] != "app" {
			t.Fatalf("want the affected project named, got %#v", ev.Data)
		}
	default:
		t.Fatal("the rename was not announced")
	}
}

// The attach header asks for one name on every terminals event, so it also has
// to hear when there is none any more. A terminal that is not running answers
// Gone and nothing else: the heading then keeps the last name it knows instead
// of putting a refusal where the name was.
func TestCoderNameRefusesATerminalThatIsNotRunning(t *testing.T) {
	gin.SetMode(gin.TestMode)
	projects := project.NewRepository(t.TempDir(), recent.New(filepath.Join(t.TempDir(), "recent.json")))
	s := &Server{
		coders:   []*coder.Manager{coder.NewManager(config.Config{}, tmux.New(), codercopilot.New(), projects)},
		projects: projects,
	}

	const id = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/coders/"+id+"/name", nil)
	c.Params = gin.Params{{Key: "id", Value: id}}
	s.handleCoderName(c)

	if rec.Code != http.StatusGone {
		t.Fatalf("want a terminal that is not running refused, got %d: %s", rec.Code, rec.Body.String())
	}
}
