package web

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/settings"
)

// The three sources a new schedule takes its zone from, in order: what the
// caller named, then what is stored, then the zone this server runs in. The
// first is the surface's own reading and is asserted where the spec is built;
// the two behind it are this function, and they have to stay apart, because a
// stored zone is an answer somebody gave and the server's own is only where
// the machine happens to stand.
func TestAssistantTimezoneFallsBackInOrder(t *testing.T) {
	t.Setenv("TZ", "Asia/Tokyo")
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))

	name, stored := AssistantTimezone(store)
	if name != "Asia/Tokyo" || stored {
		t.Fatalf("with nothing stored the server's own zone stands: got %q, stored %v", name, stored)
	}

	store.Set(assistantTimezoneKey, "Europe/Berlin")
	if name, stored = AssistantTimezone(store); name != "Europe/Berlin" || !stored {
		t.Fatalf("a stored zone stands in front of the server's: got %q, stored %v", name, stored)
	}

	// A stored name nothing can load would refuse every schedule made after
	// it, so it is read as no answer at all and the server's zone stands.
	store.Set(assistantTimezoneKey, "Mars/Olympus")
	if name, stored = AssistantTimezone(store); name != "Asia/Tokyo" || stored {
		t.Fatalf("a stored zone that does not load has to fall through: got %q, stored %v", name, stored)
	}

	// No store at all is a server built without one, which the handler tests
	// do: it answers the server's zone rather than nothing.
	if name, stored = AssistantTimezone(nil); name != "Asia/Tokyo" || stored {
		t.Fatalf("without a store the server's zone stands: got %q, stored %v", name, stored)
	}
}

// Only an explicit choice moves the stored default: a zone picked in the form,
// which the person saw, and `timezone-set`. What a turn passes with `--tz` is
// that one schedule's zone, and it travels over the local socket, which is
// what tells the two apart here.
func TestOnlyAChoiceMovesTheStoredTimezone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{settings: settings.New(filepath.Join(t.TempDir(), "settings.json"))}

	s.rememberAssistantZone(zoneRequest(false), "Europe/Berlin")
	if got, _ := s.settings.Lookup(assistantTimezoneKey); got != "Europe/Berlin" {
		t.Fatalf("a zone chosen in the form has to be kept, got %q", got)
	}

	s.rememberAssistantZone(zoneRequest(true), "America/New_York")
	if got, _ := s.settings.Lookup(assistantTimezoneKey); got != "Europe/Berlin" {
		t.Fatalf("a zone a turn named for one schedule must not move the default, got %q", got)
	}

	// A name that does not load never lands in the store: the default is read
	// back on every create, and one that refuses would refuse them all.
	s.rememberAssistantZone(zoneRequest(false), "Mars/Olympus")
	if got, _ := s.settings.Lookup(assistantTimezoneKey); got != "Europe/Berlin" {
		t.Fatalf("a zone that does not load must not be stored, got %q", got)
	}
	if _, err := assistant.LoadZone("Europe/Berlin"); err != nil {
		t.Fatal(err)
	}
}

// zoneRequest is a request from the browser or one from a turn on the local
// socket, which is the one thing that tells a choice from a zone somebody
// passed on behalf of the user.
func zoneRequest(local bool) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/assistants/triggers", nil)
	if local {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), localCallKey, true))
	}
	return c
}
