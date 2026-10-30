package editorintelligence

import (
	"context"
	"encoding/json"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/filesystem"
)

const (
	methodImplementation       = "textDocument/implementation"
	methodPrepareTypeHierarchy = "textDocument/prepareTypeHierarchy"
	methodSubtypes             = "typeHierarchy/subtypes"
)

// The groups a usages lookup lists the types below the asked one in.
const (
	groupImplementations = "implementations"
	groupSubclasses      = "subclasses"
)

// heritage reads a language's type declarations for a server whose
// implementation request answers the types below a type. declares answers
// the group below the type a line declares at the UTF-16 column, empty when
// it declares none there. reads answers whether a type is declared at a
// location of the server's answer, and its head, the clauses naming the
// types it stands below; line answers a line of the location's file by its
// 1-based number.
type heritage struct {
	declares func(line string, character int) string
	reads    func(at Location, line func(n int) (string, bool)) (heritageHead, bool)
}

// heritageHead is the head of a type's declaration: the last line of its
// heritage and where the names stand in it that name the types it stands
// below, a type argument's or a constraint's apart. A reference of the
// asked type among those names makes the type a direct one below it.
type heritageHead struct {
	last  int
	names []Location
}

// clauses reads a language's type declarations for a server without an
// implementation request, whose references of a type include the names in
// the heritage clauses of the types below it. declares answers the group
// below the type a line declares at the UTF-16 column, empty when it
// declares none there. names answers the type whose heritage clauses hold a
// reference at a location, with the group the clause puts it in. alias
// answers, for a reference in an import that renames the type, where the
// new name stands, whose references the server answers apart. line answers
// a line of the location's file by its 1-based number.
type clauses struct {
	declares func(line string, character int) string
	names    func(at Location, line func(n int) (string, bool)) (belowType, bool)
	alias    func(at Location, line func(n int) (string, bool)) (Location, bool)
}

// belowType is a type below another one: where it is declared and the group
// it stands in.
type belowType struct {
	at    Location
	group string
}

// maxAliasAsks bounds how many imports renaming a type have their
// references asked for the clauses naming the new name; past it the group
// says it was cut.
const maxAliasAsks = 50

// maxWalkTypes bounds how many types below a type have the types below
// them asked in turn, the walk to the indirect ones; walkBudget outside
// tests bounds how long it takes. Past either the indirect ones say they
// were cut.
const (
	maxWalkTypes = 50
	walkBudget   = 5 * time.Second
)

// typesBelow is what a usages lookup on a type's declaration lists apart from
// the usages: the types below it in the group they form, the ones naming it
// in their heritage apart from the indirect ones, which stand below it only
// through another one, how many of them lie outside the project, whether
// some were left unread or unwalked, and the lines that name the type in
// their heritage, a usage on one of which is the declaration naming the
// type and no usage of its own.
type typesBelow struct {
	group       string
	locations   []Location
	indirect    []Location
	outside     int
	cut         bool
	indirectCut bool
	lines       map[fileLine]bool
}

// fill puts the types below into the result's group, each list ordered for
// the panel and capped, with whether it was cut.
func (b typesBelow) fill(res *Result, activePath string) {
	sortReferences(b.locations, activePath)
	sortReferences(b.indirect, activePath)
	listed, cut := capLocations(b.locations)
	switch b.group {
	case groupImplementations:
		res.Implementations, res.ImplementationsTruncated = listed, cut || b.cut
	case groupSubclasses:
		res.Subclasses, res.SubclassesTruncated = listed, cut || b.cut
	}
	indirect, cut := capLocations(b.indirect)
	res.Indirect, res.IndirectTruncated = indirect, cut || b.indirectCut
	res.Below = b.group
}

type fileLine struct {
	path string
	n    int
}

// typeHierarchyItem is the wire shape of one type in a type hierarchy
// answer, as far as the lookup reads it.
type typeHierarchyItem struct {
	Kind           int      `json:"kind"`
	URI            string   `json:"uri"`
	SelectionRange lspRange `json:"selectionRange"`
}

// typesBelow answers the types below the one the request stands on the
// declaration of, the way the server names them, refs being the usages
// lookup's answer.
func (s *Service) typesBelow(ctx context.Context, req Request, profile *Profile, mc *managedConn, roots []SourceRoot, refs []lspLocation) (typesBelow, string) {
	switch {
	case profile.TypeHierarchy:
		return s.subtypes(ctx, req, profile, mc.conn.rootURI, roots)
	case profile.Heritage != nil:
		return s.implementedBy(ctx, req, profile, mc, roots, refs)
	case profile.Clauses != nil:
		return s.referencedBelow(ctx, req, profile, mc, roots, refs)
	}
	return typesBelow{}, ""
}

// subtypes answers where the types implementing an interface are declared,
// when the request stands on the interface's declaration, read off the
// server's type hierarchy. Its subtypes lead down only: a concrete type has
// none, where the server's implementation request would answer the
// interfaces such a type satisfies. A server refusing the position says that
// no type is named there.
func (s *Service) subtypes(ctx context.Context, req Request, profile *Profile, rootURI string, roots []SourceRoot) (typesBelow, string) {
	_, langID, _ := ProfileForPath(req.Path)
	var item json.RawMessage
	prepare := positionExchange(req, profile, langID, methodPrepareTypeHierarchy, func(answer json.RawMessage) error {
		items, _ := decodeItems(answer)
		item = interfaceItem(items, func(at typeHierarchyItem) bool {
			return atRequestPosition(rootURI, []lspLocation{{URI: at.URI, Range: at.SelectionRange}}, req)
		})
		return nil
	})
	prepare.refusable = true
	if _, status := s.exchange(ctx, prepare); status != "" || item == nil {
		return typesBelow{}, status
	}
	return s.subtypesOf(ctx, req, profile, item, rootURI, roots)
}

// subtypesOf answers the types below an interface item of the server's
// type hierarchy, carried back to it verbatim.
func (s *Service) subtypesOf(ctx context.Context, req Request, profile *Profile, item json.RawMessage, rootURI string, roots []SourceRoot) (typesBelow, string) {
	var found []lspLocation
	subtypes := exchange{
		client:   req.Client,
		project:  req.ProjectName,
		root:     req.ProjectRoot,
		launcher: req.Launcher,
		profile:  profile,
		scope:    req.Path,
		method:   methodSubtypes,
		send: func(c *lspConn) (int64, chan rpcMessage, error) {
			return c.startCall(methodSubtypes, map[string]any{"item": item})
		},
		read: func(answer json.RawMessage) error {
			var items []typeHierarchyItem
			if trimmed := strings.TrimSpace(string(answer)); trimmed != "" && trimmed != "null" {
				if err := json.Unmarshal(answer, &items); err != nil {
					return err
				}
			}
			for _, sub := range items {
				found = append(found, lspLocation{URI: sub.URI, Range: sub.SelectionRange})
			}
			return nil
		},
	}
	if _, status := s.exchange(ctx, subtypes); status != "" {
		return typesBelow{}, status
	}
	below := typesBelow{group: groupImplementations, lines: map[fileLine]bool{}}
	below.locations, below.outside = mapLocations(rootURI, found, roots)
	for _, loc := range below.locations {
		below.lines[fileLine{loc.Path, loc.Line}] = true
	}
	return below, ""
}

// implementedBy answers the types below an interface or a class the request
// stands on the declaration of, for a server whose implementation request
// names them, read by the language's heritage rule, refs being the type's
// references. The server names the indirect ones too, and they stay apart:
// a class extending one that implements the interface implements it as
// well. What else it names, the asked class itself and the values typed
// with the type, is left out, and so is an answer outside the project and
// every source root, whose file nobody reads.
func (s *Service) implementedBy(ctx context.Context, req Request, profile *Profile, mc *managedConn, roots []SourceRoot, refs []lspLocation) (typesBelow, string) {
	doc := newDocText(req.Content)
	group := profile.Heritage.declares(doc.lines[req.Line], req.Character)
	if group == "" {
		return typesBelow{}, ""
	}
	raw, status := s.locationsAt(ctx, req, profile, methodImplementation, requestLocation(req))
	if status != "" {
		return typesBelow{}, status
	}
	others := make([]lspLocation, 0, len(raw))
	for _, l := range raw {
		if !atRequestPosition(mc.conn.rootURI, []lspLocation{l}, req) {
			others = append(others, l)
		}
	}
	lines := newSourceLines(req.ProjectRoot, roots, &s.images, mc.launcher)
	lines.files[req.Path] = doc.lines
	return heritageBelow(ctx, profile, group, others, refs, lines, mc.conn.rootURI, roots), ""
}

// referencedBelow answers the types below an interface or a class the
// request stands on the declaration of, read off the clauses among its
// references.
func (s *Service) referencedBelow(ctx context.Context, req Request, profile *Profile, mc *managedConn, roots []SourceRoot, refs []lspLocation) (typesBelow, string) {
	doc := newDocText(req.Content)
	text := doc.lines[req.Line]
	group := profile.Clauses.declares(text, req.Character)
	if group == "" {
		return typesBelow{}, ""
	}
	start, _ := wordStart(text, req.Character)
	asked := Location{Path: req.Path, External: path.IsAbs(req.Path), Line: req.Line + 1, Character: utf16Len(text[:start])}
	lines := newSourceLines(req.ProjectRoot, roots, &s.images, mc.launcher)
	lines.files[req.Path] = doc.lines
	return s.clausesBelow(ctx, req, profile, asked, group, refs, lines, mc.conn.rootURI, roots)
}

// clausesBelow answers the types below a type, the one declared with its
// name at asked: the ones its references name in their clauses, and below
// them the indirect ones the walk finds.
func (s *Service) clausesBelow(ctx context.Context, req Request, profile *Profile, asked Location, group string, refs []lspLocation, lines *sourceLines, rootURI string, roots []SourceRoot) (typesBelow, string) {
	below, status := s.clausesOf(ctx, req, profile, group, refs, lines, rootURI, roots)
	if status != "" {
		return typesBelow{}, status
	}
	return below, s.walkClauses(ctx, req, profile, asked, &below, lines, rootURI, roots)
}

// walkClauses adds the indirect types below a type to the direct ones: the
// types below each type below it, read off that type's own references the
// way the direct ones are, in its own group, a class's subclasses, an
// interface's implementations, and so on down. A type stands in the lists
// once, a direct one never among the indirect ones, and the asked type
// never below itself. The walk asks one type after the other, at most
// maxWalkTypes within walkBudget; what it found when either runs out stays,
// and so does what it found before a request fails, the group saying it
// was cut. Only a lookup the caller cancelled answers its status.
func (s *Service) walkClauses(ctx context.Context, req Request, profile *Profile, asked Location, below *typesBelow, lines *sourceLines, rootURI string, roots []SourceRoot) string {
	walkCtx, cancel := context.WithTimeout(ctx, s.walkBudget)
	defer cancel()
	seen := map[Location]bool{asked: true}
	for _, at := range below.locations {
		seen[at] = true
	}
	queue := slices.Clone(below.locations)
	for walked := 0; len(queue) > 0; {
		at := queue[0]
		queue = queue[1:]
		text, _, _ := lines.line(at.Path, at.Line)
		group := profile.Clauses.declares(text, at.Character)
		if group == "" {
			continue
		}
		if walked == maxWalkTypes || walkCtx.Err() != nil {
			below.indirectCut = true
			return ""
		}
		walked++
		refs, status := s.locationsAt(walkCtx, req, profile, methodReferences, at)
		var found typesBelow
		if status == "" {
			found, status = s.clausesOf(walkCtx, req, profile, group, refs, lines, rootURI, roots)
		}
		if status != "" {
			if ctx.Err() != nil {
				return status
			}
			below.indirectCut = true
			return ""
		}
		below.indirectCut = below.indirectCut || found.cut
		for _, loc := range found.locations {
			if !seen[loc] {
				seen[loc] = true
				below.indirect = append(below.indirect, loc)
				queue = append(queue, loc)
			}
		}
	}
	return ""
}

// clausesOf reads which of a type's references stand in the heritage
// clauses of the types below it in the group, by the language's rule, and
// answers those types with the lines of the references, which are no usages
// of their own. An import renaming the type has the references of its new
// name asked and read the same way. A file outside the project is read with
// the others, in one run.
func (s *Service) clausesOf(ctx context.Context, req Request, profile *Profile, group string, refs []lspLocation, lines *sourceLines, rootURI string, roots []SourceRoot) (typesBelow, string) {
	rule := profile.Clauses
	below := typesBelow{group: group, lines: map[fileLine]bool{}}
	listed := map[Location]bool{}
	var aliases []Location
	read := func(loc Location, renamed bool) (pending bool) {
		line := func(n int) (string, bool) {
			text, ok, missing := lines.line(loc.Path, n)
			pending = pending || missing
			return text, ok
		}
		if hit, ok := rule.names(loc, line); ok && !pending {
			if hit.group == group {
				below.lines[fileLine{loc.Path, loc.Line}] = true
				if !listed[hit.at] {
					listed[hit.at] = true
					below.locations = append(below.locations, hit.at)
				}
			}
			return false
		}
		if alias, ok := rule.alias(loc, line); ok && !pending && !renamed {
			aliases = append(aliases, alias)
		}
		return pending
	}
	readAll := func(found []Location, renamed bool) {
		var later []Location
		for _, loc := range found {
			if read(loc, renamed) {
				later = append(later, loc)
			}
		}
		if len(later) > 0 {
			lines.fetch(ctx)
			for _, loc := range later {
				read(loc, renamed)
			}
		}
	}
	found, _ := mapLocations(rootURI, refs, roots)
	readAll(found, false)
	if len(aliases) > maxAliasAsks {
		aliases, below.cut = aliases[:maxAliasAsks], true
	}
	for _, alias := range aliases {
		raw, status := s.locationsAt(ctx, req, profile, methodReferences, alias)
		if status != "" {
			return typesBelow{}, status
		}
		found, _ := mapLocations(rootURI, raw, roots)
		readAll(found, true)
	}
	return below, ""
}

// requestLocation is the request's position as a location.
func requestLocation(req Request) Location {
	return Location{Path: req.Path, Line: req.Line + 1, Character: req.Character}
}

// locationsAt asks the server's request of the method at a position in a
// document, which may be another than the request's own: the request's
// document is synced first, so a server that loads the project only through
// an open document knows the other one. That other one, a file of the
// project, is held open for the request, from the disk when no editor holds
// it, and let go with the answer: intelephense answers nothing at a position
// in a file it has not opened since its start.
func (s *Service) locationsAt(ctx context.Context, req Request, profile *Profile, method string, at Location) ([]lspLocation, string) {
	_, langID, _ := ProfileForPath(req.Path)
	var raw []lspLocation
	ask := positionExchange(req, profile, langID, method, func(answer json.RawMessage) error {
		locs, err := decodeLocations(answer)
		raw = locs
		return err
	})
	open := ask.open
	holder := lookupHolder + strconv.FormatInt(s.holds.Add(1), 10)
	var held *lspConn
	ask.send = func(c *lspConn) (int64, chan rpcMessage, error) {
		if err := open(c); err != nil {
			return 0, nil, err
		}
		if held == nil && at.Path != req.Path && !at.External {
			opened, err := holdDocument(c, holder, req.ProjectRoot, profile, at.Path)
			if err != nil {
				return 0, nil, err
			}
			if opened {
				held = c
			}
		}
		return c.startAt(method, at.Path, at.Line-1, at.Character)
	}
	_, status := s.exchange(ctx, ask)
	if held != nil {
		held.docMu.Lock()
		held.closeDocument(holder, at.Path)
		held.docMu.Unlock()
	}
	return raw, status
}

// lookupHolder is the start of the holder of a document a lookup opens for
// a request at a position in it, apart from the ones the editors hold; each
// lookup's own number follows, so one letting go never closes the document
// under another.
const lookupHolder = "lookup "

// holdDocument holds a file of the project of the profile's language open
// for the holder: as an editor holds it, or else with its text on the disk.
// It reports whether it holds it, a file that cannot be read as text it does
// not. The caller holds docMu.
func holdDocument(c *lspConn, holder, root string, profile *Profile, rel string) (bool, error) {
	p, langID, ok := ProfileForPath(rel)
	if !ok || p != profile {
		return false, nil
	}
	text, open := c.docText[rel]
	if !open {
		var err error
		if text, _, err = filesystem.ReadFileText(root, rel); err != nil {
			return false, nil
		}
	}
	return true, c.ensureDocument(holder, rel, langID, text)
}

// heritageBelow reads which of the server's implementation answer are types
// declared below the asked one, by the language's heritage rule, together
// with the lines their heritage takes. A type whose heritage holds one of
// the asked type's references names it and is a direct one, any other
// stands below it through another one. A file outside the project is read
// with the others, in one run.
func heritageBelow(ctx context.Context, profile *Profile, group string, raw, refs []lspLocation, lines *sourceLines, rootURI string, roots []SourceRoot) typesBelow {
	found, _ := mapLocations(rootURI, raw, roots)
	named, _ := mapLocations(rootURI, refs, roots)
	naming := make(map[Location]bool, len(named))
	for _, ref := range named {
		naming[ref] = true
	}
	below := typesBelow{group: group, lines: map[fileLine]bool{}}
	read := func(loc Location) (pending bool) {
		head, ok := profile.Heritage.reads(loc, func(n int) (string, bool) {
			text, ok, missing := lines.line(loc.Path, n)
			pending = pending || missing
			return text, ok
		})
		if !ok || pending {
			return pending
		}
		if slices.ContainsFunc(head.names, func(name Location) bool { return naming[name] }) {
			below.locations = append(below.locations, loc)
		} else {
			below.indirect = append(below.indirect, loc)
		}
		for n := loc.Line; n <= head.last; n++ {
			below.lines[fileLine{loc.Path, n}] = true
		}
		return false
	}
	var later []Location
	for _, loc := range found {
		if read(loc) {
			later = append(later, loc)
		}
	}
	if len(later) > 0 {
		lines.fetch(ctx)
		for _, loc := range later {
			read(loc)
		}
	}
	return below
}

// decodeItems reads a type hierarchy preparation's items, verbatim each, as
// the subtypes request carries one back.
func decodeItems(answer json.RawMessage) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if trimmed := strings.TrimSpace(string(answer)); trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal(answer, &items); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// interfaceItem picks the first of a type hierarchy preparation's items that
// is an interface and passes keep.
func interfaceItem(items []json.RawMessage, keep func(typeHierarchyItem) bool) json.RawMessage {
	for _, entry := range items {
		var item typeHierarchyItem
		if err := json.Unmarshal(entry, &item); err == nil && item.Kind == symbolInterface && keep(item) {
			return entry
		}
	}
	return nil
}

// withoutLines leaves out the usages on a line one of the types below takes:
// a Go type embedding the interface names it on the line of its own name, a
// class names it in its heritage, and one row stands for both, in the
// group of the types below.
func withoutLines(usages []Location, taken map[fileLine]bool) []Location {
	if len(taken) == 0 {
		return usages
	}
	kept := make([]Location, 0, len(usages))
	for _, use := range usages {
		if !taken[fileLine{use.Path, use.Line}] {
			kept = append(kept, use)
		}
	}
	return kept
}
