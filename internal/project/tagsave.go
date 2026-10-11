package project

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// TagState is one tag as the tags settings show it: the projects carrying
// it, and Version, which stands for its name, its color and every project
// the file says carries it, so a save of a page that loaded it long ago
// changes no tag that moved meanwhile.
type TagState struct {
	Tag
	Projects []string
	Version  string
}

// TagChange is what the tags settings send for one tag: the version, the name
// and the color the page loaded, the name and the color it shows now, and
// whether it is to go. A change without an ID names a new tag by its Name
// and its Color, empty for the next free one.
type TagChange struct {
	ID          string
	Version     string
	LoadedName  string
	LoadedColor string
	Name        string
	Color       string
	Remove      bool
}

// Changed reports whether the page changed the tag at all, or names a new one.
func (c TagChange) Changed() bool {
	if c.ID == "" {
		return c.Name != ""
	}
	return c.Remove || c.Name != c.LoadedName || c.Color != c.LoadedColor
}

// TagsSaved counts what one save of the tags settings did.
type TagsSaved struct {
	Created   int
	Renamed   int
	Recolored int
	Merged    int
	Removed   int
}

// TagsConflict refuses a save over tags that changed elsewhere since the page
// loaded them, or a tag the page never showed that a new name runs into. IDs
// are those tags, Names how the page loaded them, a tag it never showed how it
// is called now.
type TagsConflict struct {
	IDs   []string
	Names []string
}

// Error names the tags as a request sent them, so it names no more of them
// than a project carries and counts the rest.
func (e *TagsConflict) Error() string {
	names := make([]string, 0, min(len(e.Names), maxTags))
	for _, name := range e.Names[:min(len(e.Names), maxTags)] {
		names = append(names, cut(name))
	}
	if rest := len(e.Names) - len(names); rest > 0 {
		names = append(names, fmt.Sprintf("%d more", rest))
	}
	return fmt.Sprintf("Changed elsewhere since this page loaded: %s. Nothing was saved, the page shows them as they are now.", strings.Join(names, ", "))
}

// TagStates lists every tag the file holds in the order a tag list is shown,
// each with the main projects carrying it. A file that cannot be read answers
// an error, never an empty list.
func (r *Repository) TagStates() ([]TagState, error) {
	if r.tags == nil {
		return nil, nil
	}
	states, err := r.tags.states()
	if err != nil {
		return nil, err
	}
	mains := map[string]bool{}
	for _, p := range r.List() {
		if !p.GitWorktree {
			mains[p.Name] = true
		}
	}
	for i := range states {
		states[i].Projects = slices.DeleteFunc(states[i].Projects, func(name string) bool { return !mains[name] })
	}
	return states, nil
}

// SaveTags applies what the tags settings send in one write, see Tags.save.
// Colors and how many tags a save makes are checked before anything is read,
// a name where the save makes it new or changes it, see checkTagName.
func (r *Repository) SaveTags(changes []TagChange) (TagsSaved, error) {
	if r.tags == nil {
		return TagsSaved{}, errTagsUnsaved
	}
	checked := make([]TagChange, 0, len(changes))
	seen := map[string]bool{}
	made := 0
	for _, change := range changes {
		name := strings.TrimSpace(change.Name)
		if change.ID == "" {
			if name == "" || seen["new "+tagKey(name)] {
				continue
			}
			seen["new "+tagKey(name)] = true
			if made++; made > maxTags {
				return TagsSaved{}, errTooManyNewTags
			}
			if change.Color != "" {
				if err := checkColor(change.Color); err != nil {
					return TagsSaved{}, err
				}
			}
			checked = append(checked, TagChange{Name: name, Color: change.Color})
			continue
		}
		if seen[change.ID] {
			continue
		}
		seen[change.ID] = true
		if name == "" && !change.Remove {
			return TagsSaved{}, errors.New("A tag needs a name.")
		}
		if err := checkColor(change.Color); err != nil && !change.Remove {
			return TagsSaved{}, err
		}
		change.Name = name
		checked = append(checked, change)
	}
	return r.tags.save(checked)
}

func (t *Tags) states() ([]TagState, error) {
	f, err := t.read()
	if err != nil {
		return nil, err
	}
	carriers := f.carriers()
	states := make([]TagState, 0, len(f.Tags))
	for _, id := range slices.SortedFunc(maps.Keys(f.Tags), f.compare) {
		states = append(states, TagState{Tag: f.tag(id), Projects: carriers[id], Version: f.version(id, carriers[id])})
	}
	return states, nil
}

// save applies the changes the tags settings send, all of them or none. A
// change names a tag by its id and only counts where the page changed what
// it loaded: then it must come with the version the tag has now, else the
// tag changed elsewhere since the page loaded it. A removed tag
// comes off every project. Tags whose names end up the same in any case are
// merged into one: the one whose name the save left alone, else the first in
// list order, keeps its id and its color, and the name typed for the merge.
// A new tag is made last, unless a tag of its name in any case is there by
// then.
// The tag merged into must still be the one the page showed, and every tag
// of a merge must be one the page sent. A read that fails is unknown and
// refuses every change. A file read fine but empty holds none of the tags the
// page changed, so each of them is gone and the save is refused as stale.
// Nothing is ever deleted for not being in the file.
func (t *Tags) save(changes []TagChange) (TagsSaved, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(changes) == 0 {
		return TagsSaved{}, nil
	}
	f, err := t.read()
	if err != nil {
		return TagsSaved{}, err
	}
	before := maps.Clone(f.Tags)
	carriers := f.carriers()
	versions := map[string]string{}
	for id := range f.Tags {
		versions[id] = f.version(id, carriers[id])
	}
	sent := map[string]TagChange{}
	conflict := &TagsConflict{}
	stale := func(id, name string) {
		if !slices.Contains(conflict.IDs, id) {
			conflict.IDs = append(conflict.IDs, id)
			conflict.Names = append(conflict.Names, name)
		}
	}
	var touched []string
	var added []TagChange
	for _, change := range changes {
		if change.ID == "" {
			added = append(added, change)
			continue
		}
		sent[change.ID] = change
		if !change.Changed() {
			continue
		}
		tag, ok := before[change.ID]
		switch {
		case !ok:
			stale(change.ID, cmp.Or(change.LoadedName, change.ID))
		case change.Version != versions[change.ID]:
			stale(change.ID, cmp.Or(change.LoadedName, tag.Name))
		}
		touched = append(touched, change.ID)
	}
	if len(conflict.IDs) > 0 {
		return TagsSaved{}, conflict
	}
	if len(touched) == 0 && len(added) == 0 {
		return TagsSaved{}, nil
	}
	for _, id := range touched {
		if change := sent[id]; !change.Remove && change.Name != before[id].Name {
			if err := checkTagName(change.Name); err != nil {
				return TagsSaved{}, err
			}
		}
	}
	var saved TagsSaved
	for _, id := range touched {
		change := sent[id]
		if change.Remove {
			f.dropTag(id)
			saved.Removed++
			continue
		}
		f.Tags[id] = tagEntry{Name: change.Name, Color: change.Color}
	}
	groups := map[string][]string{}
	for _, id := range slices.SortedFunc(maps.Keys(f.Tags), func(a, b string) int {
		if c := compareTags(before[a].Name, before[b].Name); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	}) {
		key := tagKey(f.Tags[id].Name)
		groups[key] = append(groups[key], id)
	}
	merged := map[string]bool{}
	for _, ids := range groups {
		if len(ids) < 2 || !slices.ContainsFunc(ids, func(id string) bool { return slices.Contains(touched, id) }) {
			continue
		}
		into := ids[0]
		if i := slices.IndexFunc(ids, func(id string) bool { return f.Tags[id].Name == before[id].Name }); i >= 0 {
			into = ids[i]
		}
		for _, id := range ids {
			if _, ok := sent[id]; !ok {
				stale(id, f.Tags[id].Name)
			}
		}
		if change, ok := sent[into]; ok && change.Version != versions[into] {
			stale(into, cmp.Or(change.LoadedName, before[into].Name))
		}
		name := f.Tags[into].Name
		if name == before[into].Name {
			for _, id := range ids {
				if id != into {
					name = f.Tags[id].Name
					break
				}
			}
		}
		for _, id := range ids {
			if id != into {
				f.mergeTag(id, into)
				merged[id] = true
				saved.Merged++
			}
		}
		f.Tags[into] = tagEntry{Name: name, Color: f.Tags[into].Color}
	}
	if len(conflict.IDs) > 0 {
		return TagsSaved{}, conflict
	}
	for _, id := range touched {
		if sent[id].Remove || merged[id] {
			continue
		}
		if f.Tags[id].Name != before[id].Name {
			saved.Renamed++
		}
		if f.Tags[id].Color != before[id].Color {
			saved.Recolored++
		}
	}
	named := f.byKey()
	for _, change := range added {
		if _, ok := named[tagKey(change.Name)]; ok {
			continue
		}
		if err := checkTagName(change.Name); err != nil {
			return TagsSaved{}, err
		}
		id := statefile.NewID()
		f.Tags[id] = tagEntry{Name: change.Name, Color: change.Color}
		named[tagKey(change.Name)] = id
		saved.Created++
	}
	if len(touched) == 0 && saved.Created == 0 {
		return saved, nil
	}
	return saved, t.write(f)
}

// carriers answers, for every tag, the projects the file says carry it,
// sorted by name.
func (f *tagFile) carriers() map[string][]string {
	out := map[string][]string{}
	for _, project := range slices.Sorted(maps.Keys(f.Projects)) {
		for _, id := range f.Projects[project] {
			out[id] = append(out[id], project)
		}
	}
	return out
}

// version stands for a tag as one page saw it: its name, its color and the
// projects carrying it. A rename, a recolor or a project taking it on or off
// elsewhere gives it another.
func (f *tagFile) version(id string, carriers []string) string {
	tag := f.Tags[id]
	sum := sha256.Sum256([]byte(strings.Join(append([]string{tag.Name, tag.Color}, carriers...), "\x00")))
	return hex.EncodeToString(sum[:8])
}

// dropTag takes the tag off every project carrying it.
func (f *tagFile) dropTag(id string) {
	for project, ids := range f.Projects {
		f.Projects[project] = slices.DeleteFunc(slices.Clone(ids), func(each string) bool { return each == id })
	}
	delete(f.Tags, id)
}

// mergeTag puts the tag into on every project carrying the tag id, once, and
// takes id away.
func (f *tagFile) mergeTag(id, into string) {
	for project, ids := range f.Projects {
		f.Projects[project] = slices.Clone(ids)
		for i, each := range ids {
			if each == id {
				f.Projects[project][i] = into
			}
		}
	}
	delete(f.Tags, id)
}
