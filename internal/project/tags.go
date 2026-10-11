package project

import (
	"errors"
	"fmt"
	"log"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// Every page carries every project's tags in its filters, so the bounds
// refuse a pasted text, never a real list of a few words.
const (
	maxTags     = 20
	maxTagRunes = 40
)

// TagColors are the colors a tag can wear: Tabler's named colors, in Tabler's
// order. The page styles each one through the theme's own variable, so a
// stored color is only ever one of these words and nothing else reaches CSS.
var TagColors = []string{"blue", "azure", "indigo", "purple", "pink", "red", "orange", "yellow", "lime", "green", "teal", "cyan"}

// freshColors is the order in which new tags take the colors nobody wears
// yet. Tabler's order is not even in hue, blue, azure, indigo and cyan stand
// close, so each color here is the one farthest in hue from all before it,
// and the first tags of an install look the least alike.
var freshColors = []string{"blue", "orange", "green", "purple", "pink", "lime", "teal", "red", "cyan", "indigo", "yellow", "azure"}

var (
	errTagsUnread  = errors.New("The tags could not be read, nothing was changed.")
	errTagsUnsaved = errors.New("The tags could not be saved.")
	errTagGone     = errors.New("A tag of this form was deleted or merged meanwhile, open the form again.")
	errTooManyTags = fmt.Errorf("A project carries at most %d tags.", maxTags)
	errStrayColor  = errors.New("The form sent a color for a tag it does not name, nothing was changed.")
	// One save makes no more tags than a project carries: a longer list is
	// pasted, never typed row by row.
	errTooManyNewTags = fmt.Errorf("One save makes at most %d new tags.", maxTags)
)

// Tag is one tag as every surface shows it. ID is what points at it, from a
// project, a filter, a link or a form, so a rename breaks none of them. Two
// tags never share a name in any case, and Color is one of TagColors.
type Tag struct {
	ID    string
	Name  string
	Color string
}

// TagUse is a tag with the number of projects that carry it.
type TagUse struct {
	Tag
	Projects int
}

// TagPick is what a project's tags form sends: the tags in use it keeps by
// id, and the tags it names anew with the color each shows, keyed by name. A
// new name that is a tag's in use is that tag and keeps its color, the form
// changes no color.
type TagPick struct {
	IDs       []string
	Names     []string
	NewColors map[string]string
}

// Tags keeps every tag with its name and color by id, and the ids each main
// project carries by project name, in one file under the state directory. A
// tag lives on its own: no project needs to carry it, and only a removal in
// the tags settings takes it away. Reads go through the file on every call,
// the lock only orders this process's read, modify and write.
type Tags struct {
	mu   sync.Mutex
	path string
}

type tagFile struct {
	Tags     map[string]tagEntry `json:"tags,omitempty"`
	Projects map[string][]string `json:"projects,omitempty"`
}

type tagEntry struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// NewTags keeps the tags in <state-dir>/project-tags.json.
func NewTags(stateDir string) *Tags {
	return &Tags{path: filepath.Join(stateDir, "project-tags.json")}
}

// all answers every project's tags. A file that cannot be read answers none,
// which costs one render its tags and nothing else.
func (t *Tags) all() map[string][]Tag {
	var f tagFile
	statefile.Load(t.path, &f)
	f.settle()
	out := make(map[string][]Tag, len(f.Projects))
	for name, ids := range f.Projects {
		for _, id := range ids {
			out[name] = append(out[name], f.tag(id))
		}
	}
	return out
}

// set replaces one project's tags with the ones picked, an empty pick takes
// its entry away. A new name that is a tag's in use, in any case, takes that
// tag in its color, so one name never stands for two tags, and only a name no
// tag has is checked, see checkTagName. An id that names no tag any more
// refuses the save, since the tag it meant may live on under another id.
func (t *Tags) set(project string, pick TagPick) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, err := t.read()
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(pick.IDs)+len(pick.Names))
	for _, id := range pick.IDs {
		if _, ok := f.Tags[id]; !ok {
			return errTagGone
		}
		ids = append(ids, id)
	}
	named := f.byKey()
	for _, name := range pick.Names {
		key := tagKey(name)
		id, ok := named[key]
		if !ok {
			if err := checkTagName(name); err != nil {
				return err
			}
			id = statefile.NewID()
			f.Tags[id] = tagEntry{Name: name, Color: pick.NewColors[key]}
			named[key] = id
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) > maxTags {
		return errTooManyTags
	}
	delete(f.Projects, project)
	if len(ids) > 0 {
		f.Projects[project] = ids
	}
	return t.write(f)
}

// read is the first half of every change: a file that is there but cannot be
// read refuses it, since writing back what the read answered would wipe every
// project's tags. It settles what it read, so the colors the tags were shown
// in are the ones a change keeps.
func (t *Tags) read() (tagFile, error) {
	var f tagFile
	if err := statefile.Read(t.path, &f); err != nil {
		log.Printf("project tags: %v", err)
		return f, errTagsUnread
	}
	f.settle()
	return f, nil
}

func (t *Tags) write(f tagFile) error {
	f.settle()
	if err := statefile.Write(t.path, 0o644, f); err != nil {
		log.Printf("project tags: %v", err)
		return errTagsUnsaved
	}
	return nil
}

// forget drops the entry of a project that is gone, its tags stay. A read
// that fails cannot tell whether there is anything to drop, so it leaves the
// file alone.
func (t *Tags) forget(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var f tagFile
	if err := statefile.Read(t.path, &f); err != nil {
		log.Printf("project tags: %v, the tags of %s stay", err, name)
		return
	}
	if _, ok := f.Projects[name]; !ok {
		return
	}
	f.settle()
	delete(f.Projects, name)
	f.settle()
	statefile.Save(t.path, 0o644, f)
}

// settle drops nameless tags and what points at no tag, keeps each project's
// tags once and in the order a tag list is shown, and gives every tag
// without one of TagColors the color the fewest tags wear. The read
// runs it too, without writing, so a file of another shape or written by hand
// shows what of it still makes sense, and a write runs it on what it read
// before it changes anything, so the colors the tags were shown in are kept.
func (f *tagFile) settle() {
	if f.Tags == nil {
		f.Tags = map[string]tagEntry{}
	}
	if f.Projects == nil {
		f.Projects = map[string][]string{}
	}
	var worn []string
	for id, tag := range f.Tags {
		switch {
		case strings.TrimSpace(tag.Name) == "":
			delete(f.Tags, id)
		case slices.Contains(TagColors, tag.Color):
			worn = append(worn, tag.Color)
		}
	}
	for project, ids := range f.Projects {
		kept := slices.DeleteFunc(slices.Clone(ids), func(id string) bool {
			_, ok := f.Tags[id]
			return !ok
		})
		slices.SortFunc(kept, f.compare)
		kept = slices.Compact(kept)
		if len(kept) == 0 {
			delete(f.Projects, project)
			continue
		}
		f.Projects[project] = kept
	}
	for _, id := range slices.SortedFunc(maps.Keys(f.Tags), f.compare) {
		if tag := f.Tags[id]; !slices.Contains(TagColors, tag.Color) {
			tag.Color = colorOrder(worn)[0]
			worn = append(worn, tag.Color)
			f.Tags[id] = tag
		}
	}
}

// compare orders two tag ids the way a tag list is shown, by name.
func (f *tagFile) compare(a, b string) int {
	if c := compareTags(f.Tags[a].Name, f.Tags[b].Name); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func (f *tagFile) tag(id string) Tag {
	return Tag{ID: id, Name: f.Tags[id].Name, Color: f.Tags[id].Color}
}

// byKey answers every tag's id by its name in lower case.
func (f *tagFile) byKey() map[string]string {
	named := make(map[string]string, len(f.Tags))
	for id, tag := range f.Tags {
		named[tagKey(tag.Name)] = id
	}
	return named
}

// colorOrder lists the colors by how many tags wear each, the fewest first
// and in the order of freshColors among equals, so the tags in use stay apart
// for as long as there are colors left.
func colorOrder(worn []string) []string {
	count := map[string]int{}
	for _, color := range worn {
		count[color]++
	}
	order := slices.Clone(freshColors)
	sort.SliceStable(order, func(i, j int) bool { return count[order[i]] < count[order[j]] })
	return order
}

// NextTagColors is the order in which new tags take their colors, given the
// tags there are, so a form can show a new tag in the color the save will
// give it.
func NextTagColors(tags []TagUse) []string {
	var worn []string
	for _, tag := range tags {
		worn = append(worn, tag.Color)
	}
	return colorOrder(worn)
}

// KnownTags lists every tag there is in the order a tag list is shown, each
// with the number of the given projects carrying it, a tag no project
// carries at none. A file that cannot be read lists the tags the projects
// carry.
func (r *Repository) KnownTags(projects []Project) []TagUse {
	uses := TagsInUse(projects)
	if r.tags == nil {
		return uses
	}
	states, err := r.tags.states()
	if err != nil {
		return uses
	}
	for _, state := range states {
		if !slices.ContainsFunc(uses, func(use TagUse) bool { return use.ID == state.ID }) {
			uses = append(uses, TagUse{Tag: state.Tag})
		}
	}
	slices.SortFunc(uses, func(a, b TagUse) int { return compareTags(a.Name, b.Name) })
	return uses
}

// tagOwner names the project whose tags find p in a text search: its own for
// a main project, the main project's for a worktree, and none for a worktree
// whose main repository is no project here.
func tagOwner(p Project) string {
	if p.GitWorktree {
		return p.GitWorktreeOf
	}
	return p.Name
}

// TagsRefused answers why p's tags cannot be set, nil when they can. A
// worktree carries no tags, its main project does.
func TagsRefused(p Project) error {
	if !p.GitWorktree {
		return nil
	}
	if p.GitWorktreeOf != "" {
		return fmt.Errorf("A worktree carries no tags, its main project %q does.", p.GitWorktreeOf)
	}
	return errors.New("A worktree carries no tags.")
}

// parsePick checks what a tags form sent before anything is read. Each new
// name is trimmed, a name that differs from an earlier one by case alone is
// the same tag and is dropped, and a color that is none of TagColors is
// refused. More ids or more names than a project carries are refused here
// already, distinct names and ids of tags there are always stand for that
// many tags. A color for no new name the form sends is refused, the form
// never sends one. Whether a name is new, and so checked, only the file
// knows.
func parsePick(raw TagPick) (TagPick, error) {
	pick := TagPick{NewColors: map[string]string{}}
	for _, id := range raw.IDs {
		if id != "" && !slices.Contains(pick.IDs, id) {
			pick.IDs = append(pick.IDs, id)
		}
	}
	seen := map[string]bool{}
	for _, value := range raw.Names {
		name := strings.TrimSpace(value)
		if key := tagKey(name); name != "" && !seen[key] {
			seen[key] = true
			pick.Names = append(pick.Names, name)
		}
	}
	if len(pick.IDs) > maxTags || len(pick.Names) > maxTags {
		return TagPick{}, errTooManyTags
	}
	for value, color := range raw.NewColors {
		name := strings.TrimSpace(value)
		if name == "" || color == "" {
			continue
		}
		if !seen[tagKey(name)] {
			return TagPick{}, errStrayColor
		}
		if err := checkColor(color); err != nil {
			return TagPick{}, err
		}
		pick.NewColors[tagKey(name)] = color
	}
	return pick, nil
}

func checkColor(color string) error {
	if !slices.Contains(TagColors, color) {
		return fmt.Errorf("A tag cannot wear %s, it is none of the tag colors.", quoted(color))
	}
	return nil
}

// SentTags shows what a refused form sent as tags again: each tag in use it
// kept in the color it wears, a name of a tag in use as that tag, and each
// new one in the color it showed or none. An id that names no tag in use is
// left out, the form then shows what a save would keep.
func SentTags(pick TagPick, inUse []TagUse) []Tag {
	byID := map[string]Tag{}
	byKey := map[string]Tag{}
	for _, use := range inUse {
		byID[use.ID] = use.Tag
		byKey[tagKey(use.Name)] = use.Tag
	}
	newColors := map[string]string{}
	for name, color := range pick.NewColors {
		newColors[tagKey(strings.TrimSpace(name))] = color
	}
	var tags []Tag
	seen := map[string]bool{}
	add := func(tag Tag, color string) {
		mark := tag.ID
		if mark == "" {
			mark = "new " + tagKey(tag.Name)
		}
		if seen[mark] {
			return
		}
		seen[mark] = true
		if slices.Contains(TagColors, color) {
			tag.Color = color
		}
		tags = append(tags, tag)
	}
	for _, id := range pick.IDs {
		if tag, ok := byID[id]; ok {
			add(tag, "")
		}
	}
	for _, raw := range pick.Names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if tag, ok := byKey[tagKey(name)]; ok {
			add(tag, "")
			continue
		}
		add(Tag{Name: name}, newColors[tagKey(name)])
	}
	return tags
}

// checkTagName refuses a name a tag may not take: one longer than
// maxTagRunes or holding anything but A to Z, a to z, 0 to 9, - and _. Two
// names that look alike then are alike, and nothing in a name can hide or turn
// the text around it. It runs only where a name is new or changed, a stored
// name is never checked, so a tag named before the rule stays readable and
// blocks no other change.
func checkTagName(name string) error {
	if utf8.RuneCountInString(name) > maxTagRunes {
		return fmt.Errorf("The tag %s is longer than %d characters.", quoted(name), maxTagRunes)
	}
	if strings.IndexFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) >= 0 {
		return fmt.Errorf("The tag %s may hold only A to Z, a to z, 0 to 9, - and _.", quoted(name))
	}
	return nil
}

// cut is how a message shows a value a request sent: cut after the runes a
// tag may hold, so a refusal stays one sentence however long the value was.
func cut(value string) string {
	if utf8.RuneCountInString(value) > maxTagRunes {
		return string([]rune(value)[:maxTagRunes]) + "…"
	}
	return value
}

// quoted is cut in quotes, every character outside ASCII escaped, which is
// what a refused name holds and what a reader must see.
func quoted(value string) string {
	return strconv.QuoteToASCII(cut(value))
}

func tagKey(tag string) string {
	return strings.ToLower(tag)
}

// TagsInUse lists every tag the projects carry once, with how many of them
// carry it, in the order a tag list is shown, so the filters can offer every
// tag and the form the color a tag already has.
func TagsInUse(projects []Project) []TagUse {
	var uses []TagUse
	index := map[string]int{}
	for _, p := range projects {
		for _, tag := range p.Tags {
			if i, ok := index[tag.ID]; ok {
				uses[i].Projects++
				continue
			}
			index[tag.ID] = len(uses)
			uses = append(uses, TagUse{Tag: tag, Projects: 1})
		}
	}
	slices.SortFunc(uses, func(a, b TagUse) int { return compareTags(a.Name, b.Name) })
	return uses
}

func compareTags(a, b string) int {
	if c := strings.Compare(tagKey(a), tagKey(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}
