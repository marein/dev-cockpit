package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/project"
)

// searchDraftServer builds a server over a throwaway projects root holding two
// projects and a state directory of its own. The bus comes along: a save is
// announced, and a test that never listens would otherwise publish into a nil
// bus.
func searchDraftServer(t *testing.T) (*gin.Engine, *Server) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	for _, name := range []string{"demo", "other"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{
		projects:     project.NewRepository(root, nil),
		searchDrafts: newSearchDrafts(t.TempDir()),
		bus:          eventbus.New(),
	}
	r := gin.New()
	r.GET("/projects/:name/editor/search-draft", s.handleEditorSearchDraft)
	r.POST("/projects/:name/editor/search-draft", s.handleEditorSearchDraftSave)
	return r, s
}

type searchDraftBody struct {
	Query     string `json:"query"`
	Replace   string `json:"replace"`
	Folder    string `json:"folder"`
	Mask      string `json:"mask"`
	Regex     bool   `json:"regex"`
	Case      bool   `json:"case"`
	UpdatedAt string `json:"updatedAt"`
	Saved     bool   `json:"saved"`
	Error     string `json:"error"`
}

func searchDraftGet(t *testing.T, r *gin.Engine, name string) (int, searchDraftBody) {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/"+name+"/editor/search-draft", nil))
	var got searchDraftBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, got
}

func searchDraftPost(t *testing.T, r *gin.Engine, name, body string) (int, searchDraftBody) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/"+name+"/editor/search-draft", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	var got searchDraftBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, got
}

// TestEditorSearchDraftStartsEmpty is the case a client has to tell apart from
// a stored one: nothing was ever saved, so the palette keeps what it holds.
func TestEditorSearchDraftStartsEmpty(t *testing.T) {
	r, _ := searchDraftServer(t)

	code, got := searchDraftGet(t, r, "demo")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, got.Error)
	}
	if got.Query != "" || got.Replace != "" || got.Folder != "" || got.Mask != "" || got.Regex {
		t.Errorf("an untouched project answers %+v", got)
	}
	if got.UpdatedAt != "" {
		t.Errorf("updatedAt = %q, want none for a project that never had a draft", got.UpdatedAt)
	}
}

// TestEditorSearchDraftReadsBackWhatWasWritten is the whole point: what the
// palette held comes back on the next open, on any device.
func TestEditorSearchDraftReadsBackWhatWasWritten(t *testing.T) {
	r, _ := searchDraftServer(t)

	code, saved := searchDraftPost(t, r, "demo", `{"query":"needle","replace":"pin","folder":"internal/web","mask":"*.go, !*_test.go","regex":true,"case":true}`)
	if code != http.StatusOK || !saved.Saved {
		t.Fatalf("save = %d %+v", code, saved)
	}
	if saved.UpdatedAt == "" {
		t.Error("a save answers no timestamp")
	}

	_, got := searchDraftGet(t, r, "demo")
	if got.Query != "needle" || got.Replace != "pin" || got.Folder != "internal/web" ||
		got.Mask != "*.go, !*_test.go" || !got.Regex || !got.Case {
		t.Errorf("read back %+v", got)
	}
	if got.UpdatedAt != saved.UpdatedAt {
		t.Errorf("updatedAt = %q, want the one the save answered (%q)", got.UpdatedAt, saved.UpdatedAt)
	}

	// An emptied palette is stored as emptied and keeps a timestamp: without
	// one it would look older to another device than what that device holds.
	_, cleared := searchDraftPost(t, r, "demo", `{"query":"","replace":"","folder":"","mask":"","regex":false,"case":false}`)
	if cleared.UpdatedAt == "" {
		t.Error("a cleared draft lost its timestamp")
	}
	_, after := searchDraftGet(t, r, "demo")
	if after.Query != "" || after.Folder != "" || after.Regex || after.Case {
		t.Errorf("the emptied draft still holds %+v", after)
	}
	if after.UpdatedAt == "" || after.UpdatedAt == got.UpdatedAt {
		t.Errorf("the emptied draft carries %q, want a newer stamp than %q", after.UpdatedAt, got.UpdatedAt)
	}
}

// TestEditorSearchDraftKeepsProjectsApart is what makes it per project: two
// palettes never see each other.
func TestEditorSearchDraftKeepsProjectsApart(t *testing.T) {
	r, _ := searchDraftServer(t)

	searchDraftPost(t, r, "demo", `{"query":"one","folder":"src"}`)
	searchDraftPost(t, r, "other", `{"query":"two","mask":"*.php"}`)

	_, demo := searchDraftGet(t, r, "demo")
	_, other := searchDraftGet(t, r, "other")
	if demo.Query != "one" || demo.Folder != "src" || demo.Mask != "" {
		t.Errorf("demo holds %+v", demo)
	}
	if other.Query != "two" || other.Mask != "*.php" || other.Folder != "" {
		t.Errorf("other holds %+v", other)
	}
	if code, _ := searchDraftGet(t, r, "nosuchproject"); code != http.StatusNotFound {
		t.Errorf("an unknown project = %d, want 404", code)
	}
}

// TestEditorSearchDraftRefusesTooMuch keeps a body that cannot be a query out
// of the state file.
func TestEditorSearchDraftRefusesTooMuch(t *testing.T) {
	r, _ := searchDraftServer(t)

	long := strings.Repeat("x", maxSearchDraftField+1)
	if code, _ := searchDraftPost(t, r, "demo", `{"query":"`+long+`"}`); code != http.StatusBadRequest {
		t.Errorf("an oversized query = %d, want 400", code)
	}
	if code, _ := searchDraftPost(t, r, "demo", `not json`); code != http.StatusBadRequest {
		t.Errorf("a body that is not json = %d, want 400", code)
	}
	if _, got := searchDraftGet(t, r, "demo"); got.Query != "" {
		t.Errorf("a refused save landed in the state: %+v", got)
	}
}

// TestEditorSearchDraftSaveIsAnnounced is what makes the palette live: the save
// publishes the movement and never the state, so a palette open on another
// device pulls the draft itself. A save that changed nothing stays quiet, or
// every keystroke of one device would wake all the others for what they
// already hold.
func TestEditorSearchDraftSaveIsAnnounced(t *testing.T) {
	r, s := searchDraftServer(t)
	events, cancel := s.bus.Subscribe()
	defer cancel()

	searchDraftPost(t, r, "demo", `{"query":"needle","folder":"internal"}`)
	select {
	case ev := <-events:
		if ev.Type != "searchdraft" {
			t.Fatalf("event type = %q", ev.Type)
		}
		data, ok := ev.Data.(map[string]string)
		if !ok || data["project"] != "demo" {
			t.Fatalf("want the project named, got %#v", ev.Data)
		}
	default:
		t.Fatal("the save was not announced")
	}

	searchDraftPost(t, r, "demo", `{"query":"needle","folder":"internal"}`)
	select {
	case ev := <-events:
		t.Fatalf("a save that changed nothing published %#v", ev)
	default:
	}
}
