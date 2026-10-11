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

func tagsRepo(t *testing.T) (*Repository, string, string) {
	t.Helper()
	root := t.TempDir()
	stateDir := t.TempDir()
	r := NewRepository(root, nil).UseTags(NewTags(stateDir))
	writeMainRepo(t, filepath.Join(root, "main"))
	writeLinkedWorktree(t, filepath.Join(root, "main"), filepath.Join(root, "feature"), "feature")
	for _, name := range []string{"other", "third"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return r, root, stateDir
}

func findProject(t *testing.T, r *Repository, name string) Project {
	t.Helper()
	p, err := r.FindByName(name)
	if err != nil {
		t.Fatalf("find %s: %v", name, err)
	}
	return p
}

// setTags gives a project new tags by name, the way a form without scripts
// sends them, with colors keyed by name.
func setTags(t *testing.T, r *Repository, name string, colors map[string]string, tags ...string) {
	t.Helper()
	if err := r.SetTags(findProject(t, r, name), TagPick{Names: tags, NewColors: colors}); err != nil {
		t.Fatalf("set the tags of %s: %v", name, err)
	}
}

func tagNames(tags []Tag) []string {
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Name)
	}
	return names
}

func tagOf(t *testing.T, r *Repository, name, tag string) Tag {
	t.Helper()
	for _, each := range findProject(t, r, name).Tags {
		if each.Name == tag {
			return each
		}
	}
	t.Fatalf("%s carries no %q", name, tag)
	return Tag{}
}

func storedTags(t *testing.T, stateDir string) tagFile {
	t.Helper()
	var f tagFile
	data, err := os.ReadFile(filepath.Join(stateDir, "project-tags.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestParsePick(t *testing.T) {
	got, err := parsePick(TagPick{IDs: []string{"a1", "", "a1"}, Names: []string{" backend ", "Client-A", "", "client-a", " api "}, NewColors: map[string]string{" Client-A ": "teal", "api": ""}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.IDs, []string{"a1"}) || !slices.Equal(got.Names, []string{"backend", "Client-A", "api"}) {
		t.Fatalf("parsed %+v", got)
	}
	if len(got.NewColors) != 1 || got.NewColors["client-a"] != "teal" {
		t.Fatalf("parsed the colors %v", got.NewColors)
	}
	for _, color := range []string{"Teal", "teal;}", "url(x)", "#ff0000", "secondary"} {
		if _, err := parsePick(TagPick{Names: []string{"api"}, NewColors: map[string]string{"api": color}}); err == nil {
			t.Fatalf("the color %q was taken for a new tag", color)
		}
	}
}

func TestAProjectCarriesAtMostSoManyTags(t *testing.T) {
	r, _, _ := tagsRepo(t)
	many := make([]string, maxTags+1)
	for i := range many {
		many[i] = fmt.Sprintf("t%02d", i)
	}
	if err := r.SetTags(findProject(t, r, "main"), TagPick{Names: many}); err == nil {
		t.Fatal("more tags than the bound were taken")
	}
	setTags(t, r, "main", nil, many[:maxTags]...)
}

// A worktree carries no tags: setting them is refused and no listing shows
// any on it, while a text search still finds it by its main's tag names.
func TestAWorktreeCarriesNoTags(t *testing.T) {
	r, _, _ := tagsRepo(t)
	setTags(t, r, "main", nil, "backend", "api")
	if err := r.SetTags(findProject(t, r, "feature"), TagPick{Names: []string{"own"}}); err == nil || !strings.Contains(err.Error(), `"main"`) {
		t.Fatalf("a worktree took tags: %v", err)
	}
	listed := listByName(t, r)
	if got := tagNames(listed["main"].Tags); !slices.Equal(got, []string{"api", "backend"}) {
		t.Fatalf("main lists %q", got)
	}
	for _, p := range []Project{listed["feature"], findProject(t, r, "feature")} {
		if len(p.Tags) != 0 {
			t.Fatalf("the worktree carries %v", p.Tags)
		}
		if !slices.Equal(p.SearchTags, []string{"api", "backend"}) {
			t.Fatalf("a search finds the worktree by %q", p.SearchTags)
		}
	}
	if !slices.Equal(listed["main"].SearchTags, []string{"api", "backend"}) || len(listed["other"].SearchTags) != 0 {
		t.Fatalf("a search finds main by %q and other by %q", listed["main"].SearchTags, listed["other"].SearchTags)
	}
	uses := TagsInUse(r.List())
	if len(uses) != 2 || uses[0].Name != "api" || uses[0].Projects != 1 {
		t.Fatalf("the tags in use are %+v", uses)
	}

	setTags(t, r, "main", nil)
	if listed := listByName(t, r); len(listed["main"].Tags) != 0 || len(listed["feature"].SearchTags) != 0 {
		t.Fatalf("an emptied list left %v, %v", listed["main"].Tags, listed["feature"].SearchTags)
	}
}

// New tags take the colors the fewest tags wear, so the first dozen never
// look alike, a chosen color stays, and a name in use in any case is that
// tag, with one id and one color on every project.
func TestATagHasOneColorEverywhere(t *testing.T) {
	r, _, _ := tagsRepo(t)
	setTags(t, r, "main", nil, "api", "backend", "client")
	seen := map[string]bool{}
	for _, tag := range findProject(t, r, "main").Tags {
		if seen[tag.Color] || !slices.Contains(TagColors, tag.Color) || tag.ID == "" {
			t.Fatalf("the new tags are %v", findProject(t, r, "main").Tags)
		}
		seen[tag.Color] = true
	}
	if next := NextTagColors(TagsInUse(r.List())); seen[next[0]] || len(next) != len(TagColors) {
		t.Fatalf("the next color %q is one in use", next[0])
	}

	backend := tagOf(t, r, "main", "backend")
	setTags(t, r, "other", map[string]string{"BACKEND": "pink"}, "BACKEND", "web")
	if got := tagNames(findProject(t, r, "other").Tags); !slices.Equal(got, []string{"backend", "web"}) {
		t.Fatalf("a tag in use took a second spelling: %q", got)
	}
	if got := tagOf(t, r, "other", "backend"); got != backend {
		t.Fatalf("a new name of a tag in use changed it to %+v, was %+v", got, backend)
	}
	recolor(t, r, backend.ID, "pink")
	if got := tagOf(t, r, "main", "backend"); got.ID != backend.ID || got.Color != "pink" {
		t.Fatalf("a recolor made %+v", got)
	}
	if err := r.SetTags(findProject(t, r, "main"), TagPick{IDs: idsOf(findProject(t, r, "main").Tags)}); err != nil {
		t.Fatal(err)
	}
	if got := tagOf(t, r, "other", "backend").Color; got != "pink" {
		t.Fatalf("a project's save changed the chosen color to %q", got)
	}
}

func idsOf(tags []Tag) []string {
	ids := make([]string, 0, len(tags))
	for _, tag := range tags {
		ids = append(ids, tag.ID)
	}
	return ids
}

// With every color free, new tags take them in the order of freshColors,
// which holds every color of the palette once.
func TestNewTagsTakeTheFreshColorsInOrder(t *testing.T) {
	if sorted, palette := slices.Sorted(slices.Values(freshColors)), slices.Sorted(slices.Values(TagColors)); !slices.Equal(sorted, palette) {
		t.Fatalf("freshColors %q is no order of the palette", freshColors)
	}
	r, _, _ := tagsRepo(t)
	var tags []string
	for i := range TagColors {
		tags = append(tags, fmt.Sprintf("t%02d", i))
	}
	setTags(t, r, "main", nil, tags...)
	var got []string
	for _, tag := range findProject(t, r, "main").Tags {
		got = append(got, tag.Color)
	}
	if !slices.Equal(got, freshColors) {
		t.Fatalf("the new tags wear %q, want %q", got, freshColors)
	}
}

// A project's form posts its tags in use by id, with a color for any of
// them, and an id that names no tag any more refuses the save.
func TestASaveKeepsTagsByID(t *testing.T) {
	r, _, stateDir := tagsRepo(t)
	setTags(t, r, "main", nil, "api", "web")
	api, web := tagOf(t, r, "main", "api"), tagOf(t, r, "main", "web")
	if err := r.SetTags(findProject(t, r, "other"), TagPick{IDs: []string{api.ID}, Names: []string{"tools"}}); err != nil {
		t.Fatal(err)
	}
	if got := tagOf(t, r, "other", "api"); got != api {
		t.Fatalf("the pick by id made %+v, want %+v", got, api)
	}
	if got := tagNames(findProject(t, r, "other").Tags); !slices.Equal(got, []string{"api", "tools"}) {
		t.Fatalf("other carries %q", got)
	}
	gone := keep(t, r, web.ID)
	gone.Remove = true
	saveTags(t, r, gone)
	before := storedTags(t, stateDir)
	if err := r.SetTags(findProject(t, r, "third"), TagPick{IDs: []string{api.ID, web.ID}}); err != errTagGone {
		t.Fatalf("a save with a deleted tag answered %v", err)
	}
	if after := storedTags(t, stateDir); !reflect.DeepEqual(after, before) {
		t.Fatalf("a refused save changed the file to %+v", after)
	}
}

// A project's save changes no color: a form left open never sets back a
// color changed in the settings meanwhile, and a new tag starts in the color
// the form showed.
func TestAProjectSaveChangesNoColor(t *testing.T) {
	r, _, _ := tagsRepo(t)
	setTags(t, r, "main", map[string]string{"backend": "lime"}, "backend")
	setTags(t, r, "other", nil, "backend")
	backend := tagOf(t, r, "main", "backend")
	recolor(t, r, backend.ID, "teal")
	if err := r.SetTags(findProject(t, r, "other"), TagPick{IDs: []string{backend.ID}, Names: []string{"fresh"}, NewColors: map[string]string{"fresh": "cyan"}}); err != nil {
		t.Fatal(err)
	}
	if got := tagOf(t, r, "main", "backend").Color; got != "teal" {
		t.Fatalf("a project's save set the color back to %q", got)
	}
	if got := tagOf(t, r, "other", "fresh").Color; got != "cyan" {
		t.Fatalf("a new tag starts in %q", got)
	}
}

// Removing a worktree leaves its main's tags, removing the main takes its
// entry while its tags live on, carried by no project.
func TestRemoveTakesTheProjectsTags(t *testing.T) {
	r, root, stateDir := tagsRepo(t)
	setTags(t, r, "main", map[string]string{"backend": "lime", "shared": "cyan"}, "backend", "shared")
	setTags(t, r, "other", nil, "tools", "shared")
	shared := tagOf(t, r, "other", "shared")
	if err := r.Remove(findProject(t, r, "feature")); err != nil {
		t.Fatal(err)
	}
	if got := tagNames(findProject(t, r, "main").Tags); !slices.Equal(got, []string{"backend", "shared"}) {
		t.Fatalf("removing the worktree left the main with %q", got)
	}
	if err := r.Remove(findProject(t, r, "main")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findProject(t, r, "main").Tags; len(got) != 0 {
		t.Fatalf("a project made under the removed name inherited %v", got)
	}
	if got := findProject(t, r, "other").Tags; !slices.Equal(got, []Tag{shared, tagOf(t, r, "other", "tools")}) || shared.Color != "cyan" {
		t.Fatalf("removing one project changed another's tags to %v", got)
	}
	if stored := storedTags(t, stateDir); len(stored.Tags) != 3 || len(stored.Projects) != 1 {
		t.Fatalf("the tags after the removal are %+v", stored)
	}
}

// A file of the shape before tags had ids shows no tags and breaks nothing,
// and the next save writes the new shape. A file written by hand shows what
// makes sense of it: references to no tag and nameless tags are dropped, a
// tag no project carries stays, a color outside the palette is replaced and
// every tag shows apart.
func TestAFileOfAnotherShapeOrByHand(t *testing.T) {
	r, _, stateDir := tagsRepo(t)
	path := filepath.Join(stateDir, "project-tags.json")
	if err := os.WriteFile(path, []byte(`{"projects":{"main":["Backend","client-x"]},"colors":{"backend":"teal"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := findProject(t, r, "main").Tags; len(got) != 0 {
		t.Fatalf("the old shape shows %v", got)
	}
	if uses := TagsInUse(r.List()); len(uses) != 0 {
		t.Fatalf("the old shape offers %v", uses)
	}
	setTags(t, r, "other", nil, "Backend")
	stored := storedTags(t, stateDir)
	if len(stored.Tags) != 1 || len(stored.Projects) != 1 || len(stored.Projects["other"]) != 1 || stored.Tags[stored.Projects["other"][0]].Name != "Backend" {
		t.Fatalf("the save wrote %+v", stored)
	}

	file := `{"tags":{"a":{"name":"a","color":"red"},"b":{"name":"b","color":"x;}"},"c":{"name":"c"},"d":{"name":"  ","color":"blue"},"gone":{"name":"gone","color":"blue"}},"projects":{"main":["a","b","nope","d"],"other":["c","c"]}}`
	if err := os.WriteFile(path, []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, name := range []string{"main", "other"} {
		for _, tag := range findProject(t, r, name).Tags {
			if seen[tag.Color] || !slices.Contains(TagColors, tag.Color) {
				t.Fatalf("%s shows %v", name, findProject(t, r, name).Tags)
			}
			seen[tag.Color] = true
		}
	}
	if got := findProject(t, r, "main").Tags; len(got) != 2 || got[0] != (Tag{ID: "a", Name: "a", Color: "red"}) || got[1].Name != "b" {
		t.Fatalf("main shows %v", got)
	}
	if got := tagNames(findProject(t, r, "other").Tags); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("other shows %q", got)
	}
	setTags(t, r, "third", nil, "d")
	stored = storedTags(t, stateDir)
	if _, ok := stored.Tags["gone"]; !ok || stored.Tags["b"].Color == "x;}" || len(stored.Tags) != 5 || !slices.Equal(stored.Projects["main"], []string{"a", "b"}) {
		t.Fatalf("the next save kept %+v", stored)
	}
}

// A tag without a stored color keeps the one it was shown in when another
// tag's color changes, the save stores what the read showed.
func TestASaveKeepsTheColorsTheTagsWereShownIn(t *testing.T) {
	r, _, stateDir := tagsRepo(t)
	if err := os.WriteFile(filepath.Join(stateDir, "project-tags.json"), []byte(`{"tags":{"a":{"name":"a"},"b":{"name":"b"},"c":{"name":"c"}},"projects":{"main":["a","b"],"other":["c"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	shown := map[string]string{"b": tagOf(t, r, "main", "b").Color, "c": tagOf(t, r, "other", "c").Color}
	if err := r.SetTags(findProject(t, r, "main"), TagPick{IDs: []string{"a", "b"}, Names: []string{"d"}}); err != nil {
		t.Fatal(err)
	}
	for id, color := range shown {
		if stored := storedTags(t, stateDir).Tags[id].Color; stored != color {
			t.Fatalf("%s was shown in %s and stored in %s", id, color, stored)
		}
	}
}

// A tags file that is there but cannot be read is unknown, not empty: a save
// is refused instead of writing back nothing, and a removal leaves it alone.
func TestAnUnreadableTagsFileIsNeverWrittenOver(t *testing.T) {
	r, _, stateDir := tagsRepo(t)
	setTags(t, r, "other", nil, "tools")
	path := filepath.Join(stateDir, "project-tags.json")
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
	if err := r.SetTags(findProject(t, r, "main"), TagPick{Names: []string{"backend"}}); err == nil {
		t.Fatal("a save went through over a file that could not be read")
	}
	if err := r.Remove(findProject(t, r, "other")); err != nil {
		t.Fatal(err)
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

// A refused form comes back with what it sent: each tag in use in the color
// it wears, a typed name of a tag in use as that tag
// in its color, a new one in the color it showed or none, and an id of no tag
// left out.
func TestSentTags(t *testing.T) {
	inUse := []TagUse{{Tag: Tag{ID: "a1", Name: "api", Color: "blue"}, Projects: 1}, {Tag: Tag{ID: "w1", Name: "Web", Color: "red"}, Projects: 1}}
	got := SentTags(TagPick{
		IDs:       []string{"a1", "gone", "a1"},
		Names:     []string{" web ", "New-one", "new-one", ""},
		NewColors: map[string]string{"New-one": "teal", "web": "lime"},
	}, inUse)
	want := []Tag{{ID: "a1", Name: "api", Color: "blue"}, {ID: "w1", Name: "Web", Color: "red"}, {Name: "New-one", Color: "teal"}}
	if !slices.Equal(got, want) {
		t.Fatalf("sent tags %v, want %v", got, want)
	}
}

// A new tag name holds only A to Z, a to z, 0 to 9, - and _, at most
// maxTagRunes of them, from the dialog and from the settings alike. A refusal
// quotes the name cut and in ASCII, so it never grows with the name and shows
// what does not belong.
func TestATagNameHoldsOnlyTheAllowedCharacters(t *testing.T) {
	r, _, _ := tagsRepo(t)
	main := findProject(t, r, "main")
	for _, name := range []string{"a b", "Caf\u00e9", "\u00e4", "fire\U0001f525", "a.b", "rlo\u202eevil", "zw\u200bsp", "bell\x07", "a\xffb", strings.Repeat("x", maxTagRunes+1), strings.Repeat("\u202e", 5000)} {
		for where, err := range map[string]error{
			"dialog":   r.SetTags(main, TagPick{Names: []string{name}}),
			"settings": func() error { _, err := r.SaveTags([]TagChange{{Name: name}}); return err }(),
		} {
			if err == nil {
				t.Fatalf("the %s took %q", where, name)
			}
			if message := err.Error(); len(message) > 512 || strings.ContainsFunc(message, func(c rune) bool { return c > 0x7e || c < 0x20 }) {
				t.Fatalf("the %s refused %q with %q", where, name, message)
			}
		}
	}
	allowed := []string{"abc", "XYZ", "0123456789", "a-b_c", strings.Repeat("Q", maxTagRunes)}
	setTags(t, r, "main", nil, allowed...)
	if got := tagNames(findProject(t, r, "main").Tags); len(got) != len(allowed) {
		t.Fatalf("main carries %q", got)
	}
}

// A tag stored under a name from before the rule stays readable and blocks
// nothing: a project keeps it by id beside a new name, and typing it in any
// case is that tag, not a new one to check.
func TestAnOldTagNameBlocksNoSave(t *testing.T) {
	r, _, stateDir := tagsRepo(t)
	if err := os.WriteFile(filepath.Join(stateDir, "project-tags.json"), []byte(`{"tags":{"o1":{"name":"Caf\u00e9 au lait","color":"red"}},"projects":{"main":["o1"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	main := findProject(t, r, "main")
	if err := r.SetTags(main, TagPick{IDs: []string{"o1"}, Names: []string{"fresh"}}); err != nil {
		t.Fatalf("an old tag kept by id refused the save: %v", err)
	}
	if err := r.SetTags(findProject(t, r, "other"), TagPick{Names: []string{"CAF\u00c9 AU LAIT"}}); err != nil {
		t.Fatalf("an old name typed again refused the save: %v", err)
	}
	if got := tagOf(t, r, "other", "Caf\u00e9 au lait"); got.ID != "o1" {
		t.Fatalf("the typed old name made %+v", got)
	}
}

// A form naming more ids or more new names than a project carries is refused
// before anything is read, whatever the ids are.
func TestAPickOfMoreTagsThanAProjectCarriesIsRefused(t *testing.T) {
	many := func(prefix string) []string {
		out := make([]string, maxTags+1)
		for i := range out {
			out[i] = fmt.Sprintf("%s%d", prefix, i)
		}
		return out
	}
	for _, pick := range []TagPick{{IDs: many("id")}, {Names: many("tag")}} {
		if _, err := parsePick(pick); !errors.Is(err, errTooManyTags) {
			t.Fatalf("the pick %+v answered %v", pick, err)
		}
	}
	if _, err := parsePick(TagPick{IDs: many("id")[:maxTags], Names: many("tag")[:maxTags]}); err != nil {
		t.Fatalf("a pick at the bound was refused: %v", err)
	}
}
