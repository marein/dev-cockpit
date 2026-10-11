package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func stateOf(t *testing.T, r *Repository, id string) TagState {
	t.Helper()
	states, err := r.TagStates()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		if state.ID == id {
			return state
		}
	}
	t.Fatalf("no tag %q in %+v", id, states)
	return TagState{}
}

// keep is the change a page sends for a tag it shows as it loaded it.
func keep(t *testing.T, r *Repository, id string) TagChange {
	t.Helper()
	state := stateOf(t, r, id)
	return TagChange{ID: id, Version: state.Version, LoadedName: state.Name, LoadedColor: state.Color, Name: state.Name, Color: state.Color}
}

func saveTags(t *testing.T, r *Repository, changes ...TagChange) TagsSaved {
	t.Helper()
	saved, err := r.SaveTags(changes)
	if err != nil {
		t.Fatalf("save %+v: %v", changes, err)
	}
	return saved
}

func recolor(t *testing.T, r *Repository, id, color string) {
	t.Helper()
	change := keep(t, r, id)
	change.Color = color
	saveTags(t, r, change)
}

// The settings list every tag with the main projects carrying it, a project
// gone from disk left out, and a version that moves with the name, the color
// and the projects carrying it.
func TestTagStatesListEveryTag(t *testing.T) {
	r, _, stateDir := tagsRepo(t)
	setTags(t, r, "main", map[string]string{"api": "red"}, "api", "web")
	setTags(t, r, "other", nil, "api")
	path := filepath.Join(stateDir, "project-tags.json")
	stored := storedTags(t, stateDir)
	stored.Projects["vanished"] = slices.Clone(stored.Projects["other"])
	if err := os.WriteFile(path, []byte(mustJSON(t, stored)), 0o644); err != nil {
		t.Fatal(err)
	}
	states, err := r.TagStates()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 || states[0].Name != "api" || states[0].Color != "red" || !slices.Equal(states[0].Projects, []string{"main", "other"}) || !slices.Equal(states[1].Projects, []string{"main"}) {
		t.Fatalf("the states are %+v", states)
	}
	api := states[0]
	setTags(t, r, "third", nil, "api")
	if moved := stateOf(t, r, api.ID); moved.Version == api.Version {
		t.Fatal("a project taking the tag on kept its version")
	}
	before := stateOf(t, r, api.ID).Version
	recolor(t, r, api.ID, "teal")
	if stateOf(t, r, api.ID).Version == before {
		t.Fatal("a recolor kept the version")
	}
}

// One save renames, recolors and removes at once: a rename keeps the id, a
// removed tag comes off every project, and the tags it leaves alone stay as
// they are.
func TestAStagedSaveAppliesEveryChangeAtOnce(t *testing.T) {
	r, _, stateDir := tagsRepo(t)
	setTags(t, r, "main", map[string]string{"api": "red", "web": "blue", "old": "lime", "kept": "cyan"}, "api", "web", "old", "kept")
	setTags(t, r, "other", nil, "old", "api")
	api, web, old, kept := tagOf(t, r, "main", "api"), tagOf(t, r, "main", "web"), tagOf(t, r, "main", "old"), tagOf(t, r, "main", "kept")
	rename, paint, gone := keep(t, r, api.ID), keep(t, r, web.ID), keep(t, r, old.ID)
	rename.Name = " gate-way "
	paint.Color = "orange"
	gone.Remove = true
	saved := saveTags(t, r, rename, paint, gone, keep(t, r, kept.ID))
	if saved != (TagsSaved{Renamed: 1, Recolored: 1, Removed: 1}) {
		t.Fatalf("the save answered %+v", saved)
	}
	if got := findProject(t, r, "main").Tags; !slices.Equal(got, []Tag{{ID: api.ID, Name: "gate-way", Color: "red"}, kept, {ID: web.ID, Name: "web", Color: "orange"}}) {
		t.Fatalf("main carries %v", got)
	}
	if got := findProject(t, r, "other").Tags; !slices.Equal(got, []Tag{{ID: api.ID, Name: "gate-way", Color: "red"}}) {
		t.Fatalf("other carries %v", got)
	}
	if _, ok := storedTags(t, stateDir).Tags[old.ID]; ok {
		t.Fatal("the removed tag lives on")
	}
	if got := findProject(t, r, "feature").SearchTags; !slices.Equal(got, []string{"gate-way", "kept", "web"}) {
		t.Fatalf("a search finds the worktree by %q", got)
	}
}

// Selected tags merge into the one whose name was kept: it keeps its id, the
// color picked for it and every project carrying any of them, each once.
func TestABulkMergeKeepsTheNameAndTheColorPicked(t *testing.T) {
	r, _, stateDir := tagsRepo(t)
	setTags(t, r, "main", map[string]string{"go": "azure", "golang": "lime", "gopher": "pink"}, "go", "golang", "gopher")
	setTags(t, r, "other", nil, "golang")
	setTags(t, r, "third", nil, "gopher", "web")
	goTag, golang, gopher := tagOf(t, r, "main", "go"), tagOf(t, r, "main", "golang"), tagOf(t, r, "main", "gopher")
	survivor, first, second := keep(t, r, goTag.ID), keep(t, r, golang.ID), keep(t, r, gopher.ID)
	survivor.Color = "green"
	first.Name, first.Color = "go", "green"
	second.Name, second.Color = "go", "green"
	if saved := saveTags(t, r, first, survivor, second); saved != (TagsSaved{Recolored: 1, Merged: 2}) {
		t.Fatalf("the merge answered %+v", saved)
	}
	merged := Tag{ID: goTag.ID, Name: "go", Color: "green"}
	for name, want := range map[string][]Tag{"main": {merged}, "other": {merged}, "third": {merged, tagOf(t, r, "third", "web")}} {
		if got := findProject(t, r, name).Tags; !slices.Equal(got, want) {
			t.Fatalf("%s carries %v, want %v", name, got, want)
		}
	}
	stored := storedTags(t, stateDir)
	for _, id := range []string{golang.ID, gopher.ID} {
		if _, ok := stored.Tags[id]; ok {
			t.Fatalf("the merged tag %s lives on", id)
		}
	}
	if uses := TagsInUse(r.List()); len(uses) != 2 || uses[0].ID != goTag.ID || uses[0].Projects != 3 {
		t.Fatalf("the tags in use are %+v", uses)
	}
}

// A rename onto another tag's name in any case merges into that tag: its id
// and its color live on under the spelling typed. Two tags trading names
// merge nothing, a tag removed in the same save frees its name, and tags
// renamed onto one new name merge into the first of them.
func TestARenameOntoAnotherNameMerges(t *testing.T) {
	r, _, _ := tagsRepo(t)
	setTags(t, r, "main", map[string]string{"api": "red", "gateway": "lime", "js": "pink", "javascript": "yellow"}, "api", "gateway", "js", "javascript")
	setTags(t, r, "other", nil, "api")
	api, gateway := tagOf(t, r, "main", "api"), tagOf(t, r, "main", "gateway")
	rename := keep(t, r, api.ID)
	rename.Name = "GateWay"
	if saved := saveTags(t, r, rename, keep(t, r, gateway.ID)); saved != (TagsSaved{Merged: 1}) {
		t.Fatalf("the merge answered %+v", saved)
	}
	want := Tag{ID: gateway.ID, Name: "GateWay", Color: "lime"}
	if got := tagOf(t, r, "other", "GateWay"); got != want {
		t.Fatalf("the merged tag is %+v, want %+v", got, want)
	}

	js, javascript := tagOf(t, r, "main", "js"), tagOf(t, r, "main", "javascript")
	a, b := keep(t, r, js.ID), keep(t, r, javascript.ID)
	a.Name, b.Name = "javascript", "js"
	if saved := saveTags(t, r, a, b); saved != (TagsSaved{Renamed: 2}) {
		t.Fatalf("the swap answered %+v", saved)
	}
	if got := tagOf(t, r, "main", "javascript"); got.ID != js.ID || got.Color != "pink" {
		t.Fatalf("the swap made %+v", got)
	}

	gone, takes := keep(t, r, js.ID), keep(t, r, javascript.ID)
	gone.Remove = true
	takes.Name = "javascript"
	if saved := saveTags(t, r, gone, takes); saved != (TagsSaved{Renamed: 1, Removed: 1}) {
		t.Fatalf("the removal and rename answered %+v", saved)
	}
	if got := tagOf(t, r, "main", "javascript"); got.ID != javascript.ID {
		t.Fatalf("the freed name went to %+v", got)
	}

	setTags(t, r, "third", nil, "alpha", "beta")
	alpha, beta := tagOf(t, r, "third", "alpha"), tagOf(t, r, "third", "beta")
	one, two := keep(t, r, alpha.ID), keep(t, r, beta.ID)
	one.Name, two.Name = "Greek", "greek"
	if saved := saveTags(t, r, two, one); saved.Merged != 1 {
		t.Fatalf("the shared new name answered %+v", saved)
	}
	if got := findProject(t, r, "third").Tags; len(got) != 1 || got[0].ID != alpha.ID || got[0].Name != "Greek" || got[0].Color != alpha.Color {
		t.Fatalf("the shared new name made %v", got)
	}
}

// A page that loaded the tags long ago changes nothing that moved meanwhile:
// a tag renamed, recolored, carried anew or gone elsewhere refuses the whole
// save, so does a merge into a tag that changed or one the page never showed.
// A tag the page leaves as it is may have moved meanwhile.
func TestASaveNeverWritesOverChangesMadeElsewhere(t *testing.T) {
	r, _, stateDir := tagsRepo(t)
	setTags(t, r, "main", map[string]string{"api": "red", "web": "blue", "go": "azure", "golang": "lime"}, "api", "web", "go", "golang")
	api, web, goTag, golang := tagOf(t, r, "main", "api"), tagOf(t, r, "main", "web"), tagOf(t, r, "main", "go"), tagOf(t, r, "main", "golang")
	loaded := map[string]TagChange{}
	for _, id := range []string{api.ID, web.ID, goTag.ID, golang.ID} {
		loaded[id] = keep(t, r, id)
	}
	setTags(t, r, "other", nil, "web")
	recolor(t, r, goTag.ID, "pink")
	before := storedTags(t, stateDir)
	refused := func(what string, want []string, changes ...TagChange) {
		t.Helper()
		_, err := r.SaveTags(changes)
		var conflict *TagsConflict
		if !errors.As(err, &conflict) || !slices.Equal(conflict.IDs, want) {
			t.Fatalf("%s answered %v", what, err)
		}
		if after := storedTags(t, stateDir); !reflect.DeepEqual(after, before) {
			t.Fatalf("%s changed the file to %+v", what, after)
		}
	}
	paint := loaded[web.ID]
	paint.Color = "orange"
	refused("a recolor of a tag carried anew elsewhere", []string{web.ID}, loaded[api.ID], paint)
	merge := loaded[golang.ID]
	merge.Name = "go"
	refused("a merge into a tag recolored elsewhere", []string{goTag.ID}, merge, loaded[goTag.ID])
	refused("a merge into a tag the page never sent", []string{goTag.ID}, merge)
	gone := TagChange{ID: "nope", Version: "x", Name: "nope", Color: "red", Remove: true}
	refused("a removal of a tag gone elsewhere", []string{"nope"}, gone)
	if err := (&TagsConflict{Names: []string{"web"}}).Error(); !strings.Contains(err, "web") {
		t.Fatalf("the conflict reads %q", err)
	}

	rename := loaded[api.ID]
	rename.Name = "gateway"
	if saved := saveTags(t, r, rename, loaded[web.ID], loaded[goTag.ID]); saved != (TagsSaved{Renamed: 1}) {
		t.Fatalf("a save beside tags moved elsewhere answered %+v", saved)
	}
	if got := tagOf(t, r, "other", "web"); got != web {
		t.Fatalf("a tag the page left alone came back as %+v", got)
	}
	if got := tagOf(t, r, "main", "go").Color; got != "pink" {
		t.Fatalf("a color changed elsewhere went back to %q", got)
	}
}

// A refusal names every tag as the page loaded it: one deleted elsewhere by
// its old name, not the name typed over it here, one renamed elsewhere by the
// name the page showed, and so the tag a merge would have gone into.
func TestAConflictNamesTheTagAsThePageLoadedIt(t *testing.T) {
	r, _, _ := tagsRepo(t)
	setTags(t, r, "main", map[string]string{"zeta": "red", "api": "blue", "go": "azure", "golang": "lime"}, "zeta", "api", "go", "golang")
	zeta, api, goTag, golang := tagOf(t, r, "main", "zeta"), tagOf(t, r, "main", "api"), tagOf(t, r, "main", "go"), tagOf(t, r, "main", "golang")
	loaded := map[string]TagChange{}
	for _, id := range []string{zeta.ID, api.ID, goTag.ID, golang.ID} {
		loaded[id] = keep(t, r, id)
	}
	gone, renamed, recased := keep(t, r, zeta.ID), keep(t, r, api.ID), keep(t, r, goTag.ID)
	gone.Remove = true
	renamed.Name = "gateway"
	recased.Name = "GO"
	saveTags(t, r, gone, renamed, recased)
	named := func(what string, want []string, changes ...TagChange) {
		t.Helper()
		_, err := r.SaveTags(changes)
		var conflict *TagsConflict
		if !errors.As(err, &conflict) || !slices.Equal(conflict.Names, want) {
			t.Fatalf("%s answered %v", what, err)
		}
		if !strings.HasPrefix(err.Error(), "Changed elsewhere since this page loaded: "+strings.Join(want, ", ")+".") {
			t.Fatalf("%s says %q", what, err)
		}
	}
	omega := loaded[zeta.ID]
	omega.Name = "omega"
	named("a rename of a tag deleted elsewhere", []string{"zeta"}, omega)
	paint := loaded[api.ID]
	paint.Color = "teal"
	named("a recolor of a tag renamed elsewhere", []string{"api"}, paint)
	merge := loaded[golang.ID]
	merge.Name = "go"
	named("a merge into a tag renamed elsewhere", []string{"go"}, merge, loaded[goTag.ID])
}

// When another page removed the last tags, the file is read fine and holds
// none: a page that loaded them earlier is refused as stale, naming the tags
// it changed as it loaded them, and writes nothing. A page that changed
// nothing saves nothing. Only a read that fails says the tags could not be
// read.
func TestASaveOverAnEmptiedFileIsStale(t *testing.T) {
	r, _, stateDir := tagsRepo(t)
	path := filepath.Join(stateDir, "project-tags.json")
	setTags(t, r, "main", map[string]string{"api": "red", "web": "blue"}, "api", "web")
	api, web := keep(t, r, tagOf(t, r, "main", "api").ID), keep(t, r, tagOf(t, r, "main", "web").ID)
	removeAPI, removeWeb := api, web
	removeAPI.Remove, removeWeb.Remove = true, true
	saveTags(t, r, removeAPI, removeWeb)
	emptied, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(emptied)) != "{}" {
		t.Fatalf("the last removal left %q, %v", emptied, err)
	}

	renamed, painted := api, web
	renamed.Name = "gateway"
	painted.Color = "teal"
	_, err = r.SaveTags([]TagChange{renamed, painted})
	var conflict *TagsConflict
	if !errors.As(err, &conflict) || !slices.Equal(conflict.Names, []string{"api", "web"}) || err.Error() != "Changed elsewhere since this page loaded: api, web. Nothing was saved, the page shows them as they are now." {
		t.Fatalf("a stale save over the emptied file answered %v", err)
	}
	if saved, err := r.SaveTags([]TagChange{api, web}); err != nil || saved != (TagsSaved{}) {
		t.Fatalf("a save that changed nothing answered %+v, %v", saved, err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(emptied) {
		t.Fatalf("the emptied file changed to %s", got)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SaveTags([]TagChange{renamed}); err != errTagsUnread {
		t.Fatalf("a save over an unreadable file answered %v", err)
	}
}

// A tag lives on its own: taken off its last project it stays, listed with
// no project and offered to every project, and a project naming it again in
// any case gets the same tag. TagsInUse, what the filters offer, holds only
// the tags projects carry.
func TestATagLivesWithoutProjects(t *testing.T) {
	r, _, _ := tagsRepo(t)
	setTags(t, r, "main", map[string]string{"solo": "teal"}, "solo", "api")
	solo := tagOf(t, r, "main", "solo")
	if err := r.SetTags(findProject(t, r, "main"), TagPick{IDs: []string{tagOf(t, r, "main", "api").ID}}); err != nil {
		t.Fatal(err)
	}
	if state := stateOf(t, r, solo.ID); state.Tag != solo || len(state.Projects) != 0 {
		t.Fatalf("the tag off its last project stands as %+v", state)
	}
	known := r.KnownTags(r.List())
	if len(known) != 2 || known[1].Tag != solo || known[1].Projects != 0 {
		t.Fatalf("the known tags are %+v", known)
	}
	if uses := TagsInUse(r.List()); len(uses) != 1 || uses[0].Name != "api" {
		t.Fatalf("the tags in use are %+v", uses)
	}
	setTags(t, r, "other", nil, "SOLO")
	if got := tagOf(t, r, "other", "solo"); got != solo {
		t.Fatalf("naming the tag again made %+v, want %+v", got, solo)
	}
}

// New tags come from the settings by name and color, the next free color
// where none was picked. A name a tag has in any case makes none, unless the
// same save renames that tag away, and two new rows of one name make one tag.
// A color outside the palette refuses the save, an empty name is no tag. New
// tags need no tags before them.
func TestNewTagsFromTheSettings(t *testing.T) {
	r, _, stateDir := tagsRepo(t)
	saved := saveTags(t, r, TagChange{Name: " fresh-one ", Color: "teal"}, TagChange{Name: "FRESH-ONE", Color: "red"}, TagChange{Name: ""}, TagChange{Name: "plain"})
	if saved != (TagsSaved{Created: 2}) {
		t.Fatalf("the creation answered %+v", saved)
	}
	states, err := r.TagStates()
	if err != nil || len(states) != 2 || states[0].Name != "fresh-one" || states[0].Color != "teal" || states[1].Name != "plain" || !slices.Contains(TagColors, states[1].Color) || len(states[1].Projects) != 0 {
		t.Fatalf("the new tags are %+v, %v", states, err)
	}

	setTags(t, r, "main", nil, "api")
	api := tagOf(t, r, "main", "api")
	if saved := saveTags(t, r, TagChange{Name: "API", Color: "pink"}); saved != (TagsSaved{}) {
		t.Fatalf("a new tag of a name in use answered %+v", saved)
	}
	if got := tagOf(t, r, "main", "api"); got != api {
		t.Fatalf("a new tag of its name changed the tag in use to %+v", got)
	}
	away := keep(t, r, api.ID)
	away.Name = "gateway"
	if saved := saveTags(t, r, away, TagChange{Name: "api", Color: "lime"}); saved != (TagsSaved{Created: 1, Renamed: 1}) {
		t.Fatalf("a rename and a new tag of the old name answered %+v", saved)
	}
	if got := tagOf(t, r, "main", "gateway"); got.ID != api.ID {
		t.Fatalf("the renamed tag is %+v", got)
	}

	before := storedTags(t, stateDir)
	if _, err := r.SaveTags([]TagChange{{Name: "bad", Color: "teal;}"}}); err == nil {
		t.Fatal("a new tag in a color outside the palette was taken")
	}
	if after := storedTags(t, stateDir); !reflect.DeepEqual(after, before) {
		t.Fatalf("a refused creation changed the file to %+v", after)
	}
}

// What the settings send is checked before anything is written: no name, a
// color outside the palette, an overlong name or a control character refuse
// the save and change nothing. A removed tag needs neither name nor color.
func TestATagsSaveRefusesWhatItCannotTake(t *testing.T) {
	r, _, stateDir := tagsRepo(t)
	setTags(t, r, "main", map[string]string{"api": "red"}, "api", "web")
	api := keep(t, r, tagOf(t, r, "main", "api").ID)
	before := storedTags(t, stateDir)
	for _, bad := range []TagChange{
		{Name: "  ", Color: "red"},
		{Name: "web2", Color: "teal;}"},
		{Name: "web2", Color: ""},
		{Name: strings.Repeat("x", maxTagRunes+1), Color: "red"},
		{Name: "bell\x07", Color: "red"},
	} {
		bad.ID, bad.Version, bad.LoadedName, bad.LoadedColor = api.ID, api.Version, api.LoadedName, api.LoadedColor
		if _, err := r.SaveTags([]TagChange{bad}); err == nil {
			t.Fatalf("the change %+v was taken", bad)
		}
	}
	if after := storedTags(t, stateDir); !reflect.DeepEqual(after, before) {
		t.Fatalf("a refused save changed the file to %+v", after)
	}
	api.Name, api.Color, api.Remove = "", "", true
	saveTags(t, r, api)
	if got := tagNames(findProject(t, r, "main").Tags); !slices.Equal(got, []string{"web"}) {
		t.Fatalf("main carries %q after the removal", got)
	}
}

// A save reads before it writes: a file that cannot be read is unknown and
// refuses it as unreadable, a missing or empty file holds none of the tags
// the page changed and refuses it as stale. Neither writes anything.
func TestATagsSaveNeverWritesOverAFailedOrEmptyRead(t *testing.T) {
	r, _, stateDir := tagsRepo(t)
	path := filepath.Join(stateDir, "project-tags.json")
	gone := TagChange{ID: "a1", Version: "v", LoadedName: "web", LoadedColor: "red", Name: "web", Color: "red", Remove: true}
	stale := func(what string) {
		t.Helper()
		_, err := r.SaveTags([]TagChange{gone})
		var conflict *TagsConflict
		if !errors.As(err, &conflict) || !slices.Equal(conflict.Names, []string{"web"}) {
			t.Fatalf("a save %s answered %v", what, err)
		}
	}
	stale("without a file")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a save without a file wrote one: %v", err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stale("over an empty file")
	if got, _ := os.ReadFile(path); len(got) != 0 {
		t.Fatalf("an empty read wrote %s", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	setTags(t, r, "main", nil, "api")
	change := keep(t, r, tagOf(t, r, "main", "api").ID)
	change.Remove = true
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".away"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TagStates(); err != errTagsUnread {
		t.Fatalf("the states of an unreadable file answered %v", err)
	}
	if _, err := r.SaveTags([]TagChange{change}); err != errTagsUnread {
		t.Fatalf("a save over an unreadable file answered %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("a write was tried over the unreadable file: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("the unreadable file was written over: %v", err)
	}
	if err := os.Rename(path+".away", path); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(saved) {
		t.Fatalf("the tags file changed to %s", got)
	}
}

// A conflict names the tags as a request sent them, so it cuts each name and
// names no more of them than a project carries, counting the rest.
func TestAConflictStaysOneSentence(t *testing.T) {
	names := make([]string, 5000)
	for i := range names {
		names[i] = strings.Repeat("y", 100)
	}
	message := (&TagsConflict{Names: names}).Error()
	if strings.Contains(message, strings.Repeat("y", maxTagRunes+1)) || strings.Count(message, "…") != maxTags || !strings.Contains(message, ", 4980 more.") {
		t.Fatalf("the conflict says %q", message)
	}
	if got := (&TagsConflict{Names: []string{"api", "web"}}).Error(); got != "Changed elsewhere since this page loaded: api, web. Nothing was saved, the page shows them as they are now." {
		t.Fatalf("a short conflict says %q", got)
	}
}

// A tag the file holds under a name no longer taken blocks no save that
// leaves its name alone and can be renamed or removed, while a save that
// sets such a name, as the name the page claims it loaded or as a new one,
// is refused and writes nothing.
func TestASaveChecksANameWhereItChangesIt(t *testing.T) {
	r, _, stateDir := tagsRepo(t)
	path := filepath.Join(stateDir, "project-tags.json")
	if err := os.WriteFile(path, []byte(`{"tags":{"b1":{"name":"zw\u200bsp","color":"red"},"o1":{"name":"ok","color":"blue"}},"projects":{"main":["b1","o1"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := keep(t, r, "o1")
	ok.Color = "teal"
	saveTags(t, r, keep(t, r, "b1"), ok)

	before := storedTags(t, stateDir)
	crafted := keep(t, r, "o1")
	crafted.LoadedName, crafted.Name, crafted.Color = "rlo\u202eevil", "rlo\u202eevil", "lime"
	renamed := keep(t, r, "o1")
	renamed.Name = strings.Repeat("x", 5000)
	for _, bad := range []TagChange{crafted, renamed, {Name: "iso\u2066x"}} {
		if _, err := r.SaveTags([]TagChange{bad}); err == nil {
			t.Fatalf("the change %+v was taken", bad)
		}
	}
	if after := storedTags(t, stateDir); !reflect.DeepEqual(after, before) {
		t.Fatalf("a refused save changed the file to %+v", after)
	}

	fixed := keep(t, r, "b1")
	fixed.Name = "zwsp"
	saveTags(t, r, fixed)
	if got := stateOf(t, r, "b1").Name; got != "zwsp" {
		t.Fatalf("the rename made %q", got)
	}
	if err := os.WriteFile(path, []byte(`{"tags":{"b1":{"name":"bom\ufeff","color":"red"}},"projects":{"main":["b1"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	gone := keep(t, r, "b1")
	gone.Remove = true
	saveTags(t, r, gone)
	if states, err := r.TagStates(); err != nil || len(states) != 0 {
		t.Fatalf("the removal left %+v, %v", states, err)
	}
}

// One save makes at most as many new tags as a project carries, before
// anything is read, and says so in a sentence of its own.
func TestASaveMakesAtMostSoManyNewTags(t *testing.T) {
	r, _, _ := tagsRepo(t)
	changes := make([]TagChange, maxTags+1)
	for i := range changes {
		changes[i] = TagChange{Name: fmt.Sprintf("t%02d", i)}
	}
	if _, err := r.SaveTags(changes); !errors.Is(err, errTooManyNewTags) {
		t.Fatalf("a save of %d new tags answered %v", len(changes), err)
	}
	if saved := saveTags(t, r, changes[:maxTags]...); saved.Created != maxTags {
		t.Fatalf("a save at the bound made %+v", saved)
	}
}
