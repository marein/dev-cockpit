package codex

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
)

// The shape of `codex debug models` on 0.172.0, cut to what is read: the
// slug a session starts on and whether codex's own picker lists it.
const catalogFixture = `{"models":[
{"slug":"gpt-6.2-sol","display_name":"GPT-6.2-Sol","visibility":"list","context_window":500000,"priority":1},
{"slug":"gpt-reserve","display_name":"GPT-Reserve","visibility":"hide","priority":4},
{"slug":"gpt-6-luna","display_name":"GPT-6-Luna","visibility":"list","priority":8},
{"slug":"gpt-6-luna","visibility":"list"},
{"slug":"two words","visibility":"list"},
{"slug":"-flag","visibility":"list"},
{"slug":"","visibility":"list"},
{"slug":"codex-auto-review","display_name":"Codex Auto Review","visibility":"hide","priority":12},
{"slug":"gpt-next","visibility":"none"}
]}`

// The catalog's listed models are offered in codex's order, once each; a
// hidden one is not, and neither is a slug no session could carry.
func TestTheCatalogOffersWhatCodexsPickerLists(t *testing.T) {
	names, err := parseCatalog([]byte(catalogFixture))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names, ","); got != "gpt-6.2-sol,gpt-6-luna" {
		t.Fatalf("names = %s", got)
	}
	if _, err := parseCatalog([]byte("not json")); err == nil {
		t.Fatal("want an answer that is no catalog refused")
	}
}

// The repository lists the catalog as the CLI's own names, after its first
// fetch, and names that somebody added after them.
func TestTheModelsAreTheCatalogsAndTheAddedOnes(t *testing.T) {
	c := &Coder{modelCache: coder.NewModelList(modelListTTL, func(context.Context) ([]string, error) {
		return parseCatalog([]byte(catalogFixture))
	})}
	c.models = coder.NewModelRepository(nil, "codex", codexModelsNote, c.cliModels)
	coder.WarmModels(c)
	deadline := time.Now().Add(5 * time.Second)
	for len(c.models.List()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := c.models.Add("gpt-later"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range c.models.List() {
		got = append(got, m.Source+":"+m.Name)
	}
	if strings.Join(got, ",") != "cli:gpt-6.2-sol,cli:gpt-6-luna,added:gpt-later" {
		t.Fatalf("models = %v", got)
	}
	if c.models.Exists("gpt-reserve") {
		t.Fatal("a hidden model is offered")
	}
	if c.models.Note() != codexModelsNote {
		t.Fatalf("note = %q", c.models.Note())
	}
}
