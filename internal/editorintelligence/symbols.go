package editorintelligence

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/marein/dev-cockpit/internal/filesystem"
)

const (
	methodWorkspaceSymbol = "workspace/symbol"
	methodDocumentSymbol  = "textDocument/documentSymbol"
)

// The LSP symbol kinds the languages file their types under, and the
// namespace one stands in.
const (
	symbolModule    = 2
	symbolNamespace = 3
	symbolClass     = 5
	symbolEnum      = 10
	symbolInterface = 11
	symbolFunction  = 12
	symbolStruct    = 23
)

// maxTypes caps a type search answer over all of the project's languages.
const maxTypes = 100

// maxQueryWords bounds how many words of a query a server that reads one
// word is asked, which bounds the requests of a search however many words
// are typed.
const maxQueryWords = 6

// maxSymbolCandidates bounds what one server's answer keeps before the
// types are read off it. A server without a limit of its own names every
// symbol of a large workspace for a short query, a quarter million and
// tens of megabytes for one letter, so the answer is taken streamed where
// the server can and cut here, which the list words as truncated.
const maxSymbolCandidates = 10000

// anchorScanCap bounds the search for the file a type search opens, as
// far as the walk that finds the project's languages reaches, and
// anchorMaxBytes the file: any one serves, so a small one does.
const (
	anchorScanCap  = 50000
	anchorMaxBytes = 256 << 10
)

// typesHolder holds the document a type search opens, apart from the ones
// the editors hold.
const typesHolder = "type search"

// Symbol is one entry of a workspace symbol answer as the server gave it:
// its name, the LSP symbol kind, the 1-based lines its range starts and
// ends on and the 0-based UTF-16 column it starts at.
type Symbol struct {
	Name      string
	Kind      int
	Line      int
	EndLine   int
	Character int
}

// Declared is the type a language reads off one of its symbols: its name,
// the word for its kind, the 1-based line and 0-based UTF-16 column its name
// stands at, and the namespace holding it where the server named it inside
// the name instead of as the symbol's container.
type Declared struct {
	Name      string
	Kind      string
	Line      int
	Character int
	Container string
}

// ListsTypes reports whether the language's types can be searched.
func (p *Profile) ListsTypes() bool {
	return p.TypeOf != nil
}

// TypesRequest asks the project's servers for the types whose names match
// the query. Profiles are the languages to ask with the way each server
// runs; which ones the project holds and which the settings leave on is the
// caller's to say.
type TypesRequest struct {
	Client      string
	ProjectName string
	ProjectRoot string
	Query       string
	Profiles    []WarmMode
	// Exclusions are the folders the editor's search settings keep the
	// file search out of; the type search stays out of them alike.
	Exclusions filesystem.Exclusions
}

// TypeSymbol is one type a server named: where it is declared, its name, its
// kind in the language's own word, the package or namespace holding it and
// the language it is written in. Library marks a type that is not the
// project's own: a dependency's, in a folder a package manager installs into
// or among a server's sources outside the project, or the standard
// library's.
type TypeSymbol struct {
	Location
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Container string `json:"container,omitempty"`
	Language  string `json:"language"`
	Library   bool   `json:"library,omitempty"`
}

// symbolAsk is one workspace symbol request of a type search: its query and,
// for a server that reads a setting per request, the settings sent right
// before it. The server applies them in order with the requests, so they
// hold for this one, and every request of such a server states its own.
type symbolAsk struct {
	query    string
	settings map[string]any
	// library marks an ask that only adds the libraries' symbols, sent only
	// to a project that holds own files of the server's LibraryFiles.
	library bool
}

// LanguageStatus names a language whose server could not answer, with the
// status a lookup would carry.
type LanguageStatus struct {
	Label  string `json:"label"`
	Status string `json:"status"`
}

// TypesResult is one type search answer, ranked by the quick open rule.
// Truncated says that more types may match than the list holds, because a
// server cut its answer or the list hit its own cap.
type TypesResult struct {
	Types     []TypeSymbol     `json:"types"`
	Truncated bool             `json:"truncated,omitempty"`
	Statuses  []LanguageStatus `json:"statuses,omitempty"`
}

// lspSymbol is the wire shape of one workspace symbol. A server may leave
// the range out and expect a resolve, which this client never announces.
type lspSymbol struct {
	Name          string `json:"name"`
	Kind          int    `json:"kind"`
	ContainerName string `json:"containerName"`
	Location      struct {
		URI   string    `json:"uri"`
		Range *lspRange `json:"range"`
	} `json:"location"`
}

func decodeSymbols(raw json.RawMessage) ([]lspSymbol, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var syms []lspSymbol
	if err := json.Unmarshal(raw, &syms); err != nil {
		return nil, err
	}
	return syms, nil
}

// Types answers the types whose names match the query, asked of every given
// language at once: the project's own first, then those of its libraries the
// servers name. Servers match their own way, fuzzy or by prefix, so their
// answers are only the candidates: the quick open rule decides what matches
// and in which order, the one file search follows, so both modes of the
// palette agree. A language that cannot answer is named with its status
// beside the types of the others. Nil-receiver-safe like the other
// web-facing entry points.
func (s *Service) Types(ctx context.Context, req TypesRequest) TypesResult {
	res := TypesResult{Types: []TypeSymbol{}}
	type answer struct {
		types  []TypeSymbol
		cut    bool
		status string
		label  string
	}
	var answers []*answer
	var wg sync.WaitGroup
	for _, mode := range req.Profiles {
		profile := profileByID(mode.ProfileID)
		if profile == nil || !profile.ListsTypes() {
			continue
		}
		a := &answer{label: profile.Label}
		answers = append(answers, a)
		if s == nil {
			a.status = StatusUnavailable
			continue
		}
		wg.Add(1)
		go func(launcher Launcher) {
			defer wg.Done()
			a.types, a.cut, a.status = s.profileTypes(ctx, req, profile, launcher)
		}(mode.Launcher)
	}
	wg.Wait()
	for _, a := range answers {
		if a.status != "" {
			res.Statuses = append(res.Statuses, LanguageStatus{Label: a.label, Status: a.status})
			continue
		}
		res.Types = append(res.Types, a.types...)
		res.Truncated = res.Truncated || a.cut
	}
	rankTypes(res.Types, matchQuery(req.Query))
	if len(res.Types) > maxTypes {
		res.Types, res.Truncated = res.Types[:maxTypes], true
	}
	return res
}

func profileByID(id string) *Profile {
	for _, p := range profiles {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// profileTypes asks one language's server and keeps what the quick open rule
// matches among the types it named, outside the folders the search settings
// exclude. cut reports that an answer was cut, by the server at its limit or
// here, so more types may match than it named.
func (s *Service) profileTypes(ctx context.Context, req TypesRequest, profile *Profile, launcher Launcher) ([]TypeSymbol, bool, string) {
	words := symbolWords(profile, req.Query)
	match := req
	match.Query = matchQuery(req.Query)
	var asks []symbolAsk
	firsts := map[string]int{}
	libraries := len(profile.LibraryFiles) == 0 || s.libraryFiles.held(req.ProjectRoot, profile.LibraryFiles, s.now())
	askWord := func(word string) {
		firsts[word] = len(asks)
		if profile.TypeAsks == nil {
			asks = append(asks, symbolAsk{query: word})
			return
		}
		for _, ask := range profile.TypeAsks(req.ProjectRoot, word) {
			if libraries || !ask.library {
				asks = append(asks, ask)
			}
		}
	}
	// A server that matches qualified names takes the first word alone,
	// which finds a type whose name was typed first, and the words as one
	// namespace in the order typed, which finds a type behind its namespace
	// words in one request, a qualified name as typed too. Each other word is
	// asked apart only when the namespace's answer holds no type the query
	// matches: the words then stand in another order, or one of them only in
	// a folder. The first word goes first, an empty answer of the namespace
	// being common.
	namespace := -1
	if profile.Qualified && len(words) > 1 {
		askWord(words[0])
		namespace = len(asks)
		asks = append(asks, symbolAsk{query: strings.Join(queryParts(req.Query), `\`)})
	} else {
		for _, word := range words {
			askWord(word)
		}
	}
	// A server that answers from an open document gets one of the
	// project's own files, opened by the first request and held until every
	// request has its answer. A project without one has no own types in the
	// language, and the server names a library's only beside them.
	var open func(c *lspConn) error
	var anchored *lspConn
	var anchor string
	if profile.LoadsByDocument || profile.ListsLate {
		rel, ok := typeAnchor(req.ProjectRoot, profile, skipDir(req.Exclusions))
		if !ok {
			return []TypeSymbol{}, false, ""
		}
		open = func(c *lspConn) error {
			opened, err := openTypeAnchor(c, req.ProjectRoot, profile, rel)
			if opened {
				anchored, anchor = c, rel
			}
			return err
		}
	}
	// The first request a search sends waits for the index the way a lookup
	// does and opens what the server needs, and the others follow once it
	// answered. An ask the client's last search sent is not sent again, its
	// answer stands in. Only a word whose first answer filled the server's
	// limit can have had the project's own types crowded out, which is when a
	// server that does gets the requests of its crowded plan for those words.
	now := s.now()
	reuseKey := typeReuseKey{client: req.Client, project: req.ProjectName, profile: profile.ID}
	last := s.typeReuse.last(reuseKey)
	var answers []*symbolStream
	var conns []*managedConn
	var statuses []string
	var asked []time.Time
	var rounds []*documentRound
	// unanswered answers the asks added since its last call that the last
	// search did not answer already.
	unanswered := func() []int {
		var fresh []int
		for i := len(answers); i < len(asks); i++ {
			conns, statuses = append(conns, nil), append(statuses, "")
			if a, ok := last.answer(asks[i], now); ok {
				answers, asked = append(answers, &symbolStream{syms: a.syms, cut: a.cut}), append(asked, a.at)
				continue
			}
			answers, asked = append(answers, &symbolStream{}), append(asked, now)
			fresh = append(fresh, i)
		}
		return fresh
	}
	follow := func(fresh []int, documents []ownDocument) {
		var wg sync.WaitGroup
		for _, i := range fresh {
			wg.Add(1)
			go func() {
				defer wg.Done()
				conns[i], statuses[i] = s.exchange(ctx, symbolExchange(req, profile, launcher, i, asks[i], answers[i], nil))
			}()
		}
		for _, doc := range documents {
			round := &documentRound{doc: doc}
			rounds = append(rounds, round)
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.readDocumentTypes(ctx, req, profile, launcher, round)
			}()
		}
		wg.Wait()
	}
	if fresh := unanswered(); len(fresh) > 0 {
		first := fresh[0]
		conns[first], statuses[first] = s.exchange(ctx, symbolExchange(req, profile, launcher, first, asks[first], answers[first], open))
		if statuses[first] == "" {
			follow(fresh[1:], nil)
		}
	}
	failed := func() bool {
		return slices.ContainsFunc(statuses, func(status string) bool { return status != "" })
	}
	place := func() (string, []SourceRoot, Launcher) {
		if i := slices.IndexFunc(conns, func(mc *managedConn) bool { return mc != nil }); i >= 0 {
			return uriPath(conns[i].conn.rootURI), conns[i].launcher.SourceRoots(req.ProjectName, profile), conns[i].launcher
		}
		return last.root, last.roots, last.launcher
	}
	if namespace >= 0 && !failed() {
		root, roots, _ := place()
		if raw, _ := answers[namespace].result(); !holdsType(profile, root, roots, raw, match) {
			for _, word := range words[1:] {
				askWord(word)
			}
			follow(unanswered(), nil)
		}
	}
	var plan crowdedPlan
	if !failed() && profile.Crowded != nil {
		var crowded, complete []string
		for _, word := range words {
			if first, ok := firsts[word]; ok && answers[first].filled(profile.SymbolLimit) {
				crowded = append(crowded, word)
			} else {
				complete = append(complete, word)
			}
		}
		if len(crowded) > 0 {
			plan = profile.Crowded(&s.ownFiles, ownWalk{
				root: req.ProjectRoot,
				skip: skipDir(req.Exclusions),
				owns: func(rel string) bool {
					p, _, ok := ProfileForPath(rel)
					return ok && p == profile
				},
			}, crowded, complete)
			asks = append(asks, plan.asks...)
			follow(unanswered(), plan.documents)
		}
	}
	if anchored != nil {
		anchored.docMu.Lock()
		anchored.closeDocument(typesHolder, anchor)
		anchored.docMu.Unlock()
	}
	for _, round := range rounds {
		statuses = append(statuses, round.status)
	}
	for _, status := range statuses {
		if status != "" {
			return nil, false, status
		}
	}
	round := &typeRound{answers: map[string]typeAnswer{}}
	round.root, round.roots, round.launcher = place()
	root, roots := round.root, round.roots
	var found []typeCandidate
	cut := plan.cut
	for i, answer := range answers {
		raw, answerCut := answer.result()
		round.answers[askKey(asks[i])] = typeAnswer{syms: raw, cut: answerCut, at: asked[i]}
		cut = cut || answerCut || profile.SymbolLimit > 0 && len(raw) >= profile.SymbolLimit
		for _, sym := range raw {
			if c, ok := candidateOf(root, roots, sym, match); ok {
				found = append(found, c)
			}
		}
	}
	s.typeReuse.keep(reuseKey, round, now)
	types := readTypes(ctx, profile, found, newSourceLines(root, roots, &s.images, round.launcher), match.Query)
	documented := plan.types
	for _, round := range rounds {
		if round.opened {
			s.ownFiles.remember(filepath.Join(req.ProjectRoot, filepath.FromSlash(round.doc.rel)), round.doc, round.types)
		}
		documented = append(documented, round.types...)
	}
	return withDocumented(types, documented, match.Query), cut, ""
}

// documentRound is the reading of one file's types off its document
// symbols: the file, whether the search opened it, and the answer.
type documentRound struct {
	doc    ownDocument
	opened bool
	types  []TypeSymbol
	status string
}

// readDocumentTypes asks the server for one file's document symbols. A file
// no editor holds open is opened for it with the text the scan read and
// closed again, and only then is the answer the file's own and kept; an
// editor's open document is asked as it stands.
func (s *Service) readDocumentTypes(ctx context.Context, req TypesRequest, profile *Profile, launcher Launcher, round *documentRound) {
	rel := round.doc.rel
	_, langID, _ := ProfileForPath(rel)
	var holder *lspConn
	_, round.status = s.exchange(ctx, exchange{
		client:   req.Client,
		project:  req.ProjectName,
		root:     req.ProjectRoot,
		launcher: launcher,
		profile:  profile,
		scope:    rel,
		method:   methodDocumentSymbol,
		open: func(c *lspConn) error {
			if _, open := c.docVersion[rel]; open {
				return nil
			}
			holder, round.opened = c, true
			return c.ensureDocument(typesHolder, rel, langID, round.doc.text)
		},
		send: func(c *lspConn) (int64, chan rpcMessage, error) {
			return c.startCall(methodDocumentSymbol, map[string]any{"textDocument": map[string]any{"uri": c.docURI(rel)}})
		},
		read: func(raw json.RawMessage) error {
			types, err := phpDocumentTypes(raw, rel, profile.Label)
			round.types = types
			return err
		},
	})
	if holder != nil {
		holder.docMu.Lock()
		holder.closeDocument(typesHolder, rel)
		holder.docMu.Unlock()
	}
	round.opened = round.opened && round.status == ""
}

// withDocumented adds the types document symbols named to those of the
// requests, where the quick open rule matches them and no request named
// them already.
func withDocumented(types, documented []TypeSymbol, query string) []TypeSymbol {
	type place struct {
		path, name string
		line       int
	}
	seen := map[place]bool{}
	for _, t := range types {
		seen[place{t.Path, t.Name, t.Line}] = true
	}
	for _, t := range documented {
		at := place{t.Path, t.Name, t.Line}
		if _, _, ok := filesystem.RankQuickOpen(t.Name, t.Container+" "+t.Path, query); ok && !seen[at] {
			seen[at] = true
			types = append(types, t)
		}
	}
	return types
}

// symbolExchange is the request of a type search's ask at index i, which
// the ask at the same index of the client's next search overtakes.
func symbolExchange(req TypesRequest, profile *Profile, launcher Launcher, i int, ask symbolAsk, answer *symbolStream, open func(c *lspConn) error) exchange {
	return exchange{
		client:   req.Client,
		project:  req.ProjectName,
		root:     req.ProjectRoot,
		launcher: launcher,
		profile:  profile,
		scope:    strconv.Itoa(i),
		method:   methodWorkspaceSymbol,
		open:     open,
		send: func(c *lspConn) (int64, chan rpcMessage, error) {
			if ask.settings != nil {
				if err := c.notify("workspace/didChangeConfiguration", map[string]any{"settings": ask.settings}); err != nil {
					return 0, nil, err
				}
			}
			params := map[string]any{"query": ask.query}
			return c.startStreamedCall(methodWorkspaceSymbol, params, func(part json.RawMessage) { _ = answer.add(part) })
		},
		read: answer.add,
	}
}

// typeCandidate is a symbol of a type search's answers that the editor can
// open, standing outside the folders the search leaves out, whose name and
// place hold the query's words.
type typeCandidate struct {
	sym       Symbol
	loc       Location
	container string
	library   bool
}

// candidateOf reads one symbol of an answer. Its name and container still
// hold what a language's TypeOf strips, a package or a namespace, so the
// quick open rule passes here what it may drop later, and only those few
// are read from their files.
func candidateOf(root string, roots []SourceRoot, sym lspSymbol, req TypesRequest) (typeCandidate, bool) {
	if sym.Location.Range == nil {
		return typeCandidate{}, false
	}
	r := *sym.Location.Range
	loc, ok := mapLocation(root, lspLocation{URI: sym.Location.URI, Range: r}, roots)
	if !ok {
		return typeCandidate{}, false
	}
	library := loc.External
	if !loc.External {
		excluded, dependency := placeOf(loc.Path, req.Exclusions)
		if excluded {
			return typeCandidate{}, false
		}
		library = dependency
	}
	if _, _, ok := filesystem.RankQuickOpen(sym.Name, sym.ContainerName+" "+loc.Path, req.Query); !ok {
		return typeCandidate{}, false
	}
	return typeCandidate{
		sym:       Symbol{Name: sym.Name, Kind: sym.Kind, Line: loc.Line, EndLine: r.End.Line + 1, Character: loc.Character},
		loc:       loc,
		container: sym.ContainerName,
		library:   library,
	}, true
}

// readTypes reads the types off the candidates by the language's rule and
// keeps those the quick open rule matches, each once. A candidate whose
// rule asked for a line of a file in the server's image is read again once
// every such file of the answer came out of the image together.
func readTypes(ctx context.Context, profile *Profile, found []typeCandidate, lines *sourceLines, query string) []TypeSymbol {
	types := []TypeSymbol{}
	seen := map[TypeSymbol]bool{}
	read := func(c typeCandidate) (pending bool) {
		decl, ok := profile.TypeOf(c.sym, func(n int) (string, bool) {
			text, ok, missing := lines.line(c.loc.Path, n)
			pending = pending || missing
			return text, ok
		})
		if !ok || pending {
			return pending
		}
		t := TypeSymbol{Location: c.loc, Name: decl.Name, Kind: decl.Kind, Container: c.container, Language: profile.Label, Library: c.library}
		t.Line, t.Character = decl.Line, decl.Character
		if decl.Container != "" {
			t.Container = decl.Container
		}
		if _, _, ok := filesystem.RankQuickOpen(t.Name, t.Container+" "+t.Path, query); ok && !seen[t] {
			seen[t] = true
			types = append(types, t)
		}
		return false
	}
	var later []typeCandidate
	for _, c := range found {
		if read(c) {
			later = append(later, c)
		}
	}
	if len(later) > 0 {
		lines.fetch(ctx)
		for _, c := range later {
			read(c)
		}
	}
	return types
}

// holdsType reports whether an answer names a type the query matches, by
// the language's rule with no line of a file read.
func holdsType(profile *Profile, root string, roots []SourceRoot, raw []lspSymbol, req TypesRequest) bool {
	for _, sym := range raw {
		c, ok := candidateOf(root, roots, sym, req)
		if !ok {
			continue
		}
		decl, ok := profile.TypeOf(c.sym, func(int) (string, bool) { return "", false })
		container := c.container
		if decl.Container != "" {
			container = decl.Container
		}
		if _, _, matches := filesystem.RankQuickOpen(decl.Name, container+" "+c.loc.Path, req.Query); ok && matches {
			return true
		}
	}
	return false
}

// symbolWords are what a server is asked for a type search, each in requests
// of its own. The quick open rule ignores case, and one server reads an upper
// case letter as one that must match as typed, so every server is asked in
// lower case. A server that reads the query as one word is asked each word
// of matchQuery apart, since any of them may be the one in the type's name
// while the others stand in its namespace or path, and at most maxQueryWords
// of them: the first ones and the last, the two ends a name is typed at.
func symbolWords(profile *Profile, query string) []string {
	query = strings.ToLower(query)
	words := strings.Fields(matchQuery(query))
	if profile.QueryWords || len(words) == 0 {
		return []string{query}
	}
	var asked []string
	for _, word := range words {
		if !slices.Contains(asked, word) {
			asked = append(asked, word)
		}
	}
	if len(asked) > maxQueryWords {
		asked = append(asked[:maxQueryWords-1], asked[len(asked)-1])
	}
	return asked
}

// queryParts are the words of a type search's query in the order typed:
// blanks part them, and so do the separators a qualified name is written
// with, the backslash of a PHP namespace and the dot of a Go package, a
// Python module or a TypeScript namespace. The servers name the parts of a
// qualified name apart, the name and the namespace or the package holding
// it, or the module only by its file's path.
func queryParts(query string) []string {
	return strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return r == '\\' || r == '.' || unicode.IsSpace(r)
	})
}

// matchQuery is the query the quick open rule matches the types of a search
// against: its parts as words, so a qualified name finds its type like the
// words it is made of in any order. The rule ranks by the first word, which
// of a qualified name typed first is its last part, the type's own name.
func matchQuery(query string) string {
	parts := queryParts(query)
	if first := strings.Fields(query); len(first) > 0 {
		if n := len(queryParts(first[0])); n > 1 {
			parts = append(append([]string{parts[n-1]}, parts[:n-1]...), parts[n:]...)
		}
	}
	return strings.Join(parts, " ")
}

// symbolStream gathers one workspace symbol answer, whether the server sends
// it whole or streams it in parts, up to maxSymbolCandidates; past that the
// rest is dropped and the answer counts as cut. A part arrives on the
// connection's reading goroutine, the whole answer on the asking one.
type symbolStream struct {
	mu   sync.Mutex
	syms []lspSymbol
	cut  bool
}

func (st *symbolStream) add(raw json.RawMessage) error {
	syms, err := decodeSymbols(raw)
	st.mu.Lock()
	defer st.mu.Unlock()
	if room := maxSymbolCandidates - len(st.syms); len(syms) > room {
		syms, st.cut = syms[:room], true
	}
	st.syms = append(st.syms, syms...)
	return err
}

// filled reports an answer the server cut at its limit, or this stream at
// its bound.
func (st *symbolStream) filled(limit int) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.cut || limit > 0 && len(st.syms) >= limit
}

func (st *symbolStream) result() ([]lspSymbol, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.syms, st.cut
}

// skipDir answers which folders hold none of the project's own types: git's
// own, those any language installs its dependencies into and those the
// search settings exclude. rel is the folder's path in the project, name its
// own.
func skipDir(ex filesystem.Exclusions) func(rel, name string) bool {
	return func(rel, name string) bool {
		return excludedDir(rel, name, ex) || dependencyDir(name)
	}
}

// excludedDir reports a folder the type search stays out of: git's own and
// those the search settings exclude, a dependency's among them when the
// settings say so.
func excludedDir(rel, name string, ex filesystem.Exclusions) bool {
	return name == ".git" || ex.SkipDir(rel, name)
}

// dependencyDir reports a folder any language installs its dependencies
// into. A package of one language carries files of another, scripts in a
// PHP vendor folder, and a server whose configuration takes in the whole
// project names them as if they were the project's own.
func dependencyDir(name string) bool {
	for _, p := range profiles {
		for _, dep := range p.Dependencies {
			if name == dep {
				return true
			}
		}
	}
	return false
}

// placeOf answers where a file of the project stands for the type search:
// in a folder it stays out of, or in one holding a dependency, whose types
// are a library's.
func placeOf(rel string, ex filesystem.Exclusions) (excluded, dependency bool) {
	for dir := path.Dir(rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if excludedDir(dir, path.Base(dir), ex) {
			return true, false
		}
		dependency = dependency || dependencyDir(path.Base(dir))
	}
	return false, dependency
}

// openTypeAnchor opens the anchor, one of the project's own files of the
// language, for a type search when the server needs a document to answer
// from: one that loads a project only for an open document
// (Profile.LoadsByDocument), or one that shows its workspace listed only
// through an open document's first diagnostics (Profile.ListsLate) and has
// not shown it yet. A document already open does either. It reports
// whether it opened the anchor, which the type search closes again with
// its answer, so the text it sent from the disk never outlives the search
// beside an editor's own. A file that cannot be read as text opens
// nothing. The caller holds docMu.
func openTypeAnchor(c *lspConn, root string, profile *Profile, rel string) (bool, error) {
	if len(c.docVersion) > 0 || !profile.LoadsByDocument && c.listed() {
		return false, nil
	}
	text, _, err := filesystem.ReadFileText(root, rel)
	if err != nil {
		return false, nil
	}
	_, langID, _ := ProfileForPath(rel)
	return true, c.ensureDocument(typesHolder, rel, langID, text)
}

// typeAnchor finds the file a type search opens: the shallowest file of the
// language outside the folders skip names, the first by name among equals,
// and none larger than anchorMaxBytes, a bundle at the root among them. A
// server takes its project from the configuration it finds above the file,
// so the shallowest is the one a configuration at the root covers, and the
// one the image's default configuration covers whole.
func typeAnchor(root string, profile *Profile, skip func(rel, name string) bool) (string, bool) {
	rel, found, _ := firstFile(root, skip, func(rel string, entry os.DirEntry) bool {
		if p, _, ok := ProfileForPath(rel); !ok || p != profile || !entry.Type().IsRegular() {
			return false
		}
		info, err := entry.Info()
		return err == nil && info.Size() <= anchorMaxBytes
	})
	return rel, found
}

// firstFile walks the project breadth first, past the folders skip names,
// and answers the first file match takes, the first by name among equals in
// a folder. complete is false when the walk stopped at anchorScanCap before
// it saw every file.
func firstFile(root string, skip func(rel, name string) bool, match func(rel string, entry os.DirEntry) bool) (rel string, found, complete bool) {
	dirs := []string{"."}
	scanned := 0
	for len(dirs) > 0 {
		var next []string
		for _, dir := range dirs {
			entries, err := os.ReadDir(filepath.Join(root, dir))
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if scanned++; scanned > anchorScanCap {
					return "", false, false
				}
				rel := path.Join(dir, entry.Name())
				if entry.IsDir() {
					if !skip(rel, entry.Name()) {
						next = append(next, rel)
					}
					continue
				}
				if match(rel, entry) {
					return rel, true, true
				}
			}
		}
		dirs = next
	}
	return "", false, true
}

// rankTypes orders the types the way quick open orders files, the project's
// own before every library's: the better tier and the earlier hit in the
// name first, then the shorter name, then by name and place.
func rankTypes(types []TypeSymbol, query string) {
	type ranked struct {
		TypeSymbol
		tier, pos int
	}
	all := make([]ranked, len(types))
	for i, t := range types {
		tier, pos, _ := filesystem.RankQuickOpen(t.Name, t.Container+" "+t.Path, query)
		all[i] = ranked{t, tier, pos}
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.Library != b.Library {
			return b.Library
		}
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.pos != b.pos {
			return a.pos < b.pos
		}
		if len(a.Name) != len(b.Name) {
			return len(a.Name) < len(b.Name)
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	for i, r := range all {
		types[i] = r.TypeSymbol
	}
}
