package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/docker"
	"github.com/local/dev-cockpit/internal/eventbus"
	"github.com/local/dev-cockpit/internal/settings"
)

func restoreServer(t *testing.T) (*Server, *settings.Store) {
	t.Helper()
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	return &Server{settings: store, bus: eventbus.New()}, store
}

func restoreActions(t *testing.T, s *Server) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/docker/actions/restore", nil)
	s.handleDockerActionsRestore(c)
	return rec
}

// Restoring the defaults takes the key out of the store rather than writing
// today's list into it: a stored copy would read as answered and freeze this
// install on the defaults of the version that restored them, which is what the
// absent state exists to prevent.
func TestRestoringTheActionsClearsTheSetting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, store := restoreServer(t)
	store.Set(jingleSettingKey, "calm")
	store.Set(docker.ActionsSettingKey, "[]")
	if len(s.composeActions()) != 0 {
		t.Fatal("an emptied list did not read as empty")
	}

	if rec := restoreActions(t, s); rec.Code != http.StatusOK {
		t.Fatalf("restore answered %d", rec.Code)
	}
	if value, ok := store.Lookup(docker.ActionsSettingKey); ok {
		t.Fatalf("the key is still set to %q", value)
	}
	if got, want := len(s.composeActions()), len(docker.DefaultActions()); got != want {
		t.Fatalf("the surfaces see %d commands, want %d", got, want)
	}
	// Only that key goes.
	if store.Get(jingleSettingKey) != "calm" {
		t.Fatal("restoring took another setting with it")
	}
}

// Saving the list is the same decision from the other side: the defaults
// unchanged take the key out, anything else is stored.
func TestSavingTheDefaultsLeavesTheKeyAbsent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	store.Set(docker.ActionsSettingKey, "[]")

	storeComposeActions(store, docker.DefaultActions())
	if value, ok := store.Lookup(docker.ActionsSettingKey); ok {
		t.Fatalf("saving the defaults stored %q", value)
	}

	own := docker.DefaultActions()
	own[0].Timeout = "20m"
	storeComposeActions(store, own)
	value, ok := store.Lookup(docker.ActionsSettingKey)
	if !ok {
		t.Fatal("an edited list was not stored")
	}
	list, err := docker.DecodeActions(value)
	if err != nil || len(list) != len(own) || list[0].Timeout != "20m" {
		t.Fatalf("the stored list reads back as %+v, %v", list, err)
	}

	// And an emptied list is stored as such, that is what leaves no buttons.
	storeComposeActions(store, []docker.Action{})
	if value, ok := store.Lookup(docker.ActionsSettingKey); !ok || value != "[]" {
		t.Fatalf("the emptied list stored %q, %v", value, ok)
	}
}

// The link rules are the same three states and the same way back, one key
// further: restoring clears it, saving the defaults leaves it absent, and an
// emptied list is stored, which is what leaves the menus with the ports alone.
func TestRestoringTheLinkRulesClearsTheSetting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, store := restoreServer(t)
	store.Set(docker.LinkRulesSettingKey, "[]")
	if len(s.linkRules()) != 0 {
		t.Fatal("an emptied list did not read as empty")
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/docker/link-rules/restore", nil)
	s.handleDockerLinkRulesRestore(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore answered %d", rec.Code)
	}
	if value, ok := store.Lookup(docker.LinkRulesSettingKey); ok {
		t.Fatalf("the key is still set to %q", value)
	}
	if !docker.IsDefaultLinkRules(s.linkRules()) {
		t.Fatal("the surfaces do not see the default rules again")
	}
	// The commands next to them are a key of their own and stay untouched.
	if _, ok := store.Lookup(docker.ActionsSettingKey); ok {
		t.Fatal("restoring the rules touched the compose actions")
	}
}

func TestSavingTheDefaultLinkRulesLeavesTheKeyAbsent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	store.Set(docker.LinkRulesSettingKey, "[]")

	storeLinkRules(store, docker.DefaultLinkRules())
	if value, ok := store.Lookup(docker.LinkRulesSettingKey); ok {
		t.Fatalf("saving the defaults stored %q", value)
	}

	own := docker.DefaultLinkRules()
	own[0].Scheme = "https"
	storeLinkRules(store, own)
	value, ok := store.Lookup(docker.LinkRulesSettingKey)
	if !ok {
		t.Fatal("an edited list was not stored")
	}
	list, err := docker.DecodeLinkRules(value)
	if err != nil || len(list) != 1 || list[0].Scheme != "https" {
		t.Fatalf("the stored list reads back as %+v, %v", list, err)
	}

	storeLinkRules(store, []docker.LinkRule{})
	if value, ok := store.Lookup(docker.LinkRulesSettingKey); !ok || value != "[]" {
		t.Fatalf("the emptied list stored %q, %v", value, ok)
	}
}

// The same request on a store that never carried the key changes nothing and
// still answers: the way back is one button, not a state machine.
func TestRestoringTheActionsWithoutTheKeyIsFine(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, store := restoreServer(t)
	if rec := restoreActions(t, s); rec.Code != http.StatusOK {
		t.Fatalf("restore answered %d", rec.Code)
	}
	if _, ok := store.Lookup(docker.ActionsSettingKey); ok {
		t.Fatal("restoring created the key")
	}
	if len(s.composeActions()) != len(docker.DefaultActions()) {
		t.Fatal("the defaults are not what the surfaces see")
	}
}
