package editorintelligence

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/marein/dev-cockpit/internal/markdown"
)

// maxStructureSymbols caps a structure answer. A generated file can declare
// tens of thousands of symbols, far more than anybody reads through in a
// list, so the walk stops there in file order and says it did.
const maxStructureSymbols = 2000

// structureScope keys a structure request in flight apart from a type
// search's reading of the same file's symbols for the same client, so
// neither cancels the other.
const structureScope = "structure "

// symbolKinds are the words for the LSP symbol kinds a structure shows.
var symbolKinds = map[int]string{
	1: "file", 2: "module", 3: "namespace", 4: "package", 5: "class",
	6: "method", 7: "property", 8: "field", 9: "constructor", 10: "enum",
	11: "interface", 12: "function", 13: "variable", 14: "constant",
	15: "string", 16: "number", 17: "boolean", 18: "array", 19: "object",
	20: "key", 21: "null", 22: "enum member", 23: "struct", 24: "event",
	25: "operator", 26: "type parameter",
}

// bodyKinds are the kinds whose children are a body, its locals, parameters
// and callbacks, and no declaration of the file.
var bodyKinds = map[int]bool{6: true, 9: true, 12: true}

// documentSymbol is the wire shape of one document symbol in either form a
// server answers: a node of the tree, with the range of the whole
// declaration, the one of its name, its detail and its children, or the
// plain shape of a flat list, with a location and the name of its
// container instead.
type documentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail"`
	Kind           int              `json:"kind"`
	Range          *lspRange        `json:"range"`
	SelectionRange *lspRange        `json:"selectionRange"`
	Location       *lspLocation     `json:"location"`
	ContainerName  string           `json:"containerName"`
	Children       []documentSymbol `json:"children"`
}

// StructureSymbol is one declaration of a document: its name, the word for
// its kind, the detail the server gives beside it, a signature or a type,
// the 1-based line and 0-based UTF-16 column its name starts at, whether
// the request's position stands in it, and what it declares inside, in
// file order. A markdown heading carries the id of its preview heading.
type StructureSymbol struct {
	Name      string            `json:"name"`
	Kind      string            `json:"kind"`
	Detail    string            `json:"detail,omitempty"`
	Anchor    string            `json:"anchor,omitempty"`
	Line      int               `json:"line"`
	Character int               `json:"character"`
	Cursor    bool              `json:"cursor,omitempty"`
	Children  []StructureSymbol `json:"children,omitempty"`
	span      lspRange
}

// StructureResult is one structure answer. A server that cannot answer
// travels as a status like a lookup's; Truncated says the document declares
// more than the answer holds.
type StructureResult struct {
	Available bool              `json:"available"`
	Status    string            `json:"status,omitempty"`
	Symbols   []StructureSymbol `json:"symbols"`
	Truncated bool              `json:"truncated,omitempty"`
}

// Structure answers the declarations of the request's document, read by its
// server off the text the request carries, the innermost one around the
// request's position marked. It goes out without the wait for the index: a
// server reads one document's declarations off that document alone, so the
// answer is whole while the project still indexes. Safe on a nil receiver,
// like every entry point the web layer calls.
func (s *Service) Structure(ctx context.Context, req Request) StructureResult {
	if s == nil {
		return StructureResult{Status: StatusUnavailable, Symbols: []StructureSymbol{}}
	}
	profile, langID, ok := ProfileForPath(req.Path)
	if !ok {
		return StructureResult{Status: StatusNoLanguage, Symbols: []StructureSymbol{}}
	}
	res := StructureResult{Available: true}
	_, status := s.exchange(ctx, exchange{
		client:    req.Client,
		project:   req.ProjectName,
		root:      req.ProjectRoot,
		launcher:  req.Launcher,
		profile:   profile,
		scope:     structureScope + req.Path,
		method:    methodDocumentSymbol,
		unindexed: true,
		send: func(c *lspConn) (int64, chan rpcMessage, error) {
			if err := c.ensureDocument(req.Client, req.Path, langID, req.Content); err != nil {
				return 0, nil, err
			}
			return c.startCall(methodDocumentSymbol, map[string]any{"textDocument": map[string]any{"uri": c.docURI(req.Path)}})
		},
		read: func(raw json.RawMessage) error {
			var err error
			res.Symbols, res.Truncated, err = documentStructure(raw)
			markCursor(res.Symbols, lspPosition{Line: req.Line, Character: req.Character})
			return err
		},
	})
	if status != "" {
		return StructureResult{Status: status, Symbols: []StructureSymbol{}}
	}
	return res
}

// MarkdownStructure answers the headings of a markdown document as its
// structure, read without a server: each heading under the nearest one
// before it of a higher level, its kind the level's word, its section
// running to the next heading of its own level or above, and the innermost
// section around the position marked like a declaration.
func MarkdownStructure(content string, line, character int) StructureResult {
	type section struct {
		node  *structureNode
		level int
	}
	end := lspPosition{Line: strings.Count(content, "\n") + 1}
	var top []*structureNode
	var open []section
	at, from := 0, 0
	for _, h := range markdown.Headings(content) {
		if h.Offset < from {
			at, from = 0, 0
		}
		at += strings.Count(content[from:h.Offset], "\n")
		from = h.Offset
		lineStart := strings.LastIndexByte(content[:h.Offset], '\n') + 1
		n := &structureNode{
			sym:  StructureSymbol{Name: h.Text, Kind: "h" + strconv.Itoa(h.Level), Anchor: h.ID},
			span: lspRange{Start: lspPosition{Line: at}, End: end},
			at:   lspPosition{Line: at, Character: utf16Len(content[lineStart:h.Offset])},
		}
		for len(open) > 0 && open[len(open)-1].level >= h.Level {
			open[len(open)-1].node.span.End = n.span.Start
			open = open[:len(open)-1]
		}
		if len(open) > 0 {
			parent := open[len(open)-1].node
			parent.children = append(parent.children, n)
		} else {
			top = append(top, n)
		}
		open = append(open, section{node: n, level: h.Level})
	}
	budget := maxStructureSymbols
	symbols := structureOf(top, &budget)
	markCursor(symbols, lspPosition{Line: line, Character: character})
	return StructureResult{Available: true, Symbols: symbols, Truncated: budget < 0}
}

// structureNode is one symbol of an answer on the way to the structure:
// the range of its whole declaration, where its name starts, the container
// a flat entry names, and its children.
type structureNode struct {
	sym       StructureSymbol
	kind      int
	span      lspRange
	at        lspPosition
	flat      bool
	container string
	children  []*structureNode
}

// documentStructure reads a document symbol answer into the declarations of
// the document, nested and in file order, and says whether it cut them at
// maxStructureSymbols. A tree keeps its nesting. An entry of a flat list
// stands under the innermost entry whose declaration encloses its own, the
// one its container names first, which also places the plain entries of an
// answer mixing both forms, as intelephense's for a file with an HTML part.
func documentStructure(raw json.RawMessage) ([]StructureSymbol, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return []StructureSymbol{}, false, nil
	}
	var answer []documentSymbol
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, false, err
	}
	nodes := make([]*structureNode, 0, len(answer))
	for _, sym := range answer {
		if n, ok := structureNodeOf(sym); ok {
			nodes = append(nodes, n)
		}
	}
	budget := maxStructureSymbols
	symbols := structureOf(nestFlat(nodes), &budget)
	return symbols, budget < 0, nil
}

func structureNodeOf(sym documentSymbol) (*structureNode, bool) {
	kind, ok := symbolKinds[sym.Kind]
	if !ok {
		kind = "symbol"
	}
	n := &structureNode{
		sym:  StructureSymbol{Name: sym.Name, Kind: kind, Detail: strings.Join(strings.Fields(sym.Detail), " ")},
		kind: sym.Kind,
	}
	switch {
	case sym.Location != nil:
		n.span, n.at, n.flat, n.container = sym.Location.Range, sym.Location.Range.Start, true, sym.ContainerName
	case sym.SelectionRange != nil:
		n.span, n.at = *sym.SelectionRange, sym.SelectionRange.Start
		if sym.Range != nil {
			n.span = *sym.Range
		}
		for _, child := range sym.Children {
			if c, ok := structureNodeOf(child); ok {
				n.children = append(n.children, c)
			}
		}
	default:
		return nil, false
	}
	return n, true
}

// nestFlat places the flat entries among the top level of an answer under
// the entries that enclose them, and answers what stands at the top.
func nestFlat(nodes []*structureNode) []*structureNode {
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i].span, nodes[j].span
		if a.Start != b.Start {
			return positionBefore(a.Start, b.Start)
		}
		return positionBefore(b.End, a.End)
	})
	var top, open []*structureNode
	for _, n := range nodes {
		for len(open) > 0 && !encloses(open[len(open)-1].span, n.span) {
			open = open[:len(open)-1]
		}
		var parent *structureNode
		if n.flat && len(open) > 0 {
			parent = open[len(open)-1]
			for i := len(open) - 1; i >= 0 && n.container != ""; i-- {
				if open[i].sym.Name == n.container {
					parent = open[i]
					break
				}
			}
		}
		if parent != nil {
			parent.children = append(parent.children, n)
		} else {
			top = append(top, n)
		}
		open = append(open, n)
	}
	return top
}

// structureOf orders the nodes by where their names stand and spends the
// budget on them depth first, a body's content left out.
func structureOf(nodes []*structureNode, budget *int) []StructureSymbol {
	sort.SliceStable(nodes, func(i, j int) bool { return positionBefore(nodes[i].at, nodes[j].at) })
	out := make([]StructureSymbol, 0, len(nodes))
	for _, n := range nodes {
		if *budget <= 0 {
			*budget = -1
			return out
		}
		*budget--
		sym := n.sym
		sym.Line, sym.Character, sym.span = n.at.Line+1, n.at.Character, n.span
		if !bodyKinds[n.kind] && len(n.children) > 0 {
			sym.Children = structureOf(n.children, budget)
		}
		out = append(out, sym)
	}
	return out
}

// markCursor marks the innermost declaration standing around the position:
// of those whose whole span holds it, the one starting last, then the one
// ending first, and of equal spans the one further in. The end counts as
// inside, where a cursor rests behind a declaration's last character.
func markCursor(symbols []StructureSymbol, at lspPosition) {
	var best *StructureSymbol
	var walk func(list []StructureSymbol)
	walk = func(list []StructureSymbol) {
		for i := range list {
			sym := &list[i]
			if encloses(sym.span, lspRange{Start: at, End: at}) && (best == nil || !narrower(best.span, sym.span)) {
				best = sym
			}
			walk(sym.Children)
		}
	}
	walk(symbols)
	if best != nil {
		best.Cursor = true
	}
}

func narrower(a, b lspRange) bool {
	if a.Start != b.Start {
		return positionBefore(b.Start, a.Start)
	}
	return positionBefore(a.End, b.End)
}

func positionBefore(a, b lspPosition) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
}

func encloses(outer, inner lspRange) bool {
	return !positionBefore(inner.Start, outer.Start) && !positionBefore(outer.End, inner.End)
}
