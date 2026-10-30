package editorintelligence

import (
	"encoding/json"
	"path"
	"sort"
	"strings"
	"unicode"

	"github.com/marein/dev-cockpit/internal/filesystem"
)

// phpTypeKinds are the symbol kinds intelephense files the types under, one
// of its own for each: a trait comes under Module, which nothing else does.
var phpTypeKinds = map[int]string{
	symbolClass:     "class",
	symbolInterface: "interface",
	symbolModule:    "trait",
	symbolEnum:      "enum",
}

// phpDeclarationScan bounds how far below the start of a declaration its
// name is looked for, past the doc comment and the attributes.
const phpDeclarationScan = 200

// phpNamespaceAsks bounds how many namespaces a crowded type search asks
// intelephense for beside the query.
const phpNamespaceAsks = 20

// phpCrowded asks intelephense for the project's own types apart once the
// query filled its limit of a thousand symbols of every kind. It ranks the
// names the query's letters stand earliest in first, so with a large vendor
// folder a short query answers the vendor's methods and the project's own
// types drop out. A query holding a backslash is matched against the
// qualified name instead, from its first letter on, so a namespace in front
// of the query puts what is declared under it first; a type counts with each
// of its members there, which is why every namespace a level below one the
// project autoloads gets a request of its own, one for each crowded word. The
// namespaces come from composer.json and from what the project's files
// declare outside them. No query reaches the global namespace, whose types
// the files' own document symbols name instead, see ownFiles. Beside complete
// words only the places where they all stand are asked, see phpPlaces.
func phpCrowded(files *ownFiles, walk ownWalk, words, complete []string) crowdedPlan {
	scan := files.scan(walk, words)
	autoloaded, below := composerNamespaces(walk.root)
	var namespaces []string
	seen := map[string]bool{}
	add := func(namespace string) {
		if seen[namespace] {
			return
		}
		seen[namespace] = true
		namespaces = append(namespaces, namespace)
	}
	for _, namespace := range autoloaded {
		add(namespace)
	}
	for _, namespace := range outermost(scan.namespaces) {
		if !insideAny(namespace, autoloaded) {
			add(namespace)
		}
	}
	for _, namespace := range below {
		add(namespace)
	}
	plan := crowdedPlan{documents: scan.documents, types: scan.types, cut: scan.cut}
	if len(complete) > 0 {
		namespaces, plan.documents = phpPlaces(namespaces, scan, complete)
	}
	if len(namespaces) > phpNamespaceAsks {
		namespaces, plan.cut = namespaces[:phpNamespaceAsks], true
	}
	for _, namespace := range namespaces {
		for _, word := range words {
			plan.asks = append(plan.asks, symbolAsk{query: namespace + `\` + word})
		}
	}
	return plan
}

// insideAny reports whether the namespace is one of the others or lies
// below one of them.
func insideAny(namespace string, others []string) bool {
	for _, other := range others {
		if namespace == other || strings.HasPrefix(namespace, other+`\`) {
			return true
		}
	}
	return false
}

// composerAutoload is the part of composer.json naming the namespaces a
// project's own classes stand under, each with the folders that hold them.
type composerAutoload struct {
	PSR4 map[string]json.RawMessage `json:"psr-4"`
	PSR0 map[string]json.RawMessage `json:"psr-0"`
}

// composerNamespaces reads the namespaces the project's composer.json
// autoloads for its code and its tests, and the ones below them, a
// subfolder of their folders each, in lower case like every query, each
// once and the shortest first.
// An entry without a trailing backslash may be a prefix of class names and
// no namespace, and the empty one names the global namespace, which no
// query can ask for. A PSR-0 namespace lies in its folder under its own
// path.
func composerNamespaces(root string) (autoloaded, below []string) {
	text, _, err := filesystem.ReadFileText(root, "composer.json")
	if err != nil {
		return nil, nil
	}
	var manifest struct {
		Autoload    composerAutoload `json:"autoload"`
		AutoloadDev composerAutoload `json:"autoload-dev"`
	}
	if json.Unmarshal([]byte(text), &manifest) != nil {
		return nil, nil
	}
	for _, autoload := range []composerAutoload{manifest.Autoload, manifest.AutoloadDev} {
		for _, standard := range []struct {
			entries map[string]json.RawMessage
			nested  bool
		}{{autoload.PSR4, false}, {autoload.PSR0, true}} {
			for prefix, folders := range standard.entries {
				namespace := strings.Trim(prefix, `\`)
				if namespace == "" || !strings.HasSuffix(prefix, `\`) {
					continue
				}
				autoloaded = append(autoloaded, strings.ToLower(namespace))
				for _, folder := range composerFolders(folders) {
					if standard.nested {
						folder = path.Join(folder, strings.ReplaceAll(namespace, `\`, "/"))
					}
					entries, _ := filesystem.ListDir(root, folder)
					for _, entry := range entries {
						if entry.IsDir && phpNamespaceName(entry.Name) {
							below = append(below, strings.ToLower(namespace+`\`+entry.Name))
						}
					}
				}
			}
		}
	}
	return unique(autoloaded), unique(below)
}

// outermost sorts the namespaces, the shortest first, and keeps those that
// lie inside none of the others.
func outermost(namespaces []string) []string {
	var kept []string
	for _, namespace := range unique(namespaces) {
		if !insideAny(namespace, kept) {
			kept = append(kept, namespace)
		}
	}
	return kept
}

// unique sorts the namespaces, the shortest first, each once.
func unique(namespaces []string) []string {
	sort.Slice(namespaces, func(i, j int) bool {
		if len(namespaces[i]) != len(namespaces[j]) {
			return len(namespaces[i]) < len(namespaces[j])
		}
		return namespaces[i] < namespaces[j]
	})
	var once []string
	for i, namespace := range namespaces {
		if i == 0 || namespace != namespaces[i-1] {
			once = append(once, namespace)
		}
	}
	return once
}

// composerFolders reads the folders of an autoload entry, one or a list.
func composerFolders(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(raw, &many)
	return many
}

// phpNamespaceName reports whether a folder's name can be a segment of a
// namespace.
func phpNamespaceName(name string) bool {
	for i, r := range name {
		if r == '$' || !isIdentifierRune(r) || i == 0 && unicode.IsDigit(r) {
			return false
		}
	}
	return name != ""
}

// phpTypeOf reads intelephense's workspace symbols. The range of a type
// starts where its declaration does, at the doc comment or the first
// attribute, so the name is looked up from there: the line holding the
// keyword and the name is where the palette opens, a comment line never
// is, and the start stands in when no line within the declaration shows
// them. A type a namespace was asked for comes under its qualified name.
func phpTypeOf(sym Symbol, line func(n int) (string, bool)) (Declared, bool) {
	kind, ok := phpTypeKinds[sym.Kind]
	if !ok {
		return Declared{}, false
	}
	at := Declared{Name: sym.Name, Kind: kind, Line: sym.Line, Character: sym.Character}
	if i := strings.LastIndexByte(sym.Name, '\\'); i >= 0 {
		at.Container, at.Name = sym.Name[:i], sym.Name[i+1:]
	}
	for n := sym.Line; n <= min(sym.EndLine, sym.Line+phpDeclarationScan); n++ {
		text, ok := line(n)
		if !ok {
			break
		}
		if phpCommentLine(text) {
			continue
		}
		if column, ok := phpNameColumn(text, at.Name); ok {
			at.Line, at.Character = n, column
			break
		}
	}
	return at, true
}

// phpCommentLine reports a line of a doc comment, a block comment or a
// line comment. An attribute starts like a comment of the hash kind and may
// hold the declaration behind it.
func phpCommentLine(text string) bool {
	text = strings.TrimLeft(text, " \t")
	return strings.HasPrefix(text, "*") || strings.HasPrefix(text, "/*") || strings.HasPrefix(text, "//") ||
		strings.HasPrefix(text, "#") && !strings.HasPrefix(text, "#[")
}

// phpNameColumn answers the UTF-16 column the name stands at when the line
// declares it: the keyword of a type in any case, as PHP takes it, then
// white space and the name as a whole word.
func phpNameColumn(text, name string) (int, bool) {
	for from := 0; from < len(text); {
		i := strings.Index(text[from:], name)
		if i < 0 {
			return 0, false
		}
		i += from
		before := strings.TrimRight(text[:i], " \t")
		if len(before) < i && !startsIdentifier(text[i+len(name):]) && endsWithKeyword(before, "class", "interface", "trait", "enum") {
			return utf16Len(text[:i]), true
		}
		from = i + len(name)
	}
	return 0, false
}

// endsWithKeyword reports whether text ends with one of the keywords as a
// whole word, ignoring case.
func endsWithKeyword(text string, keywords ...string) bool {
	for _, keyword := range keywords {
		if len(text) < len(keyword) || !strings.EqualFold(text[len(text)-len(keyword):], keyword) {
			continue
		}
		if rest := text[:len(text)-len(keyword)]; rest == "" || !endsIdentifier(rest) {
			return true
		}
	}
	return false
}

// phpPlaces keeps of a crowded plan the namespaces and files where an own
// type could stand that holds every complete word outside its name: one
// holding such a word in its name is in that word's whole answer already.
// A namespace is kept for a file whose namespace and path hold all of them,
// the deepest of the plan's that holds the file's namespace.
func phpPlaces(namespaces []string, scan ownScan, complete []string) ([]string, []ownDocument) {
	holds := func(place string) bool {
		for _, word := range complete {
			if !strings.Contains(place, word) {
				return false
			}
		}
		return true
	}
	keep := map[string]bool{}
	for _, file := range scan.files {
		for _, namespace := range file.namespaces {
			if !holds(strings.ToLower(file.rel) + " " + namespace) {
				continue
			}
			deepest := ""
			for _, candidate := range namespaces {
				if (namespace == candidate || strings.HasPrefix(namespace, candidate+`\`)) && len(candidate) > len(deepest) {
					deepest = candidate
				}
			}
			if deepest != "" {
				keep[deepest] = true
			}
		}
	}
	var kept []string
	for _, namespace := range namespaces {
		if keep[namespace] {
			kept = append(kept, namespace)
		}
	}
	var documents []ownDocument
	for _, doc := range scan.documents {
		if holds(strings.ToLower(doc.rel)) {
			documents = append(documents, doc)
		}
	}
	return kept, documents
}
