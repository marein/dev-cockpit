package editorintelligence

import (
	"encoding/json"
	"net/url"
	"path"
	"sort"
	"strings"
)

// Location is one navigation target in editor coordinates: Line is 1-based,
// Character a 0-based UTF-16 offset into that line, both as the server
// reported them. Path is project relative, or the absolute path of a file
// under one of the language server's own source roots, which External
// marks: a dependency's downloaded sources, the standard library, a stub.
// Those open read only and through a route of their own, so a client can
// never confuse one with a file of the project.
type Location struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Character int    `json:"character"`
	External  bool   `json:"external,omitempty"`
}

// lspPosition, lspRange and lspLocation are the LSP wire shapes.
type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

// lspLocationLink is the alternative answer shape for definition. It is
// decoded defensively even though the initialize never announces
// linkSupport.
type lspLocationLink struct {
	TargetURI            string   `json:"targetUri"`
	TargetSelectionRange lspRange `json:"targetSelectionRange"`
	TargetRange          lspRange `json:"targetRange"`
}

// decodeLocations accepts every answer shape the navigation methods may
// return: null, a single Location, an array of Locations, or an array of
// LocationLinks.
func decodeLocations(raw json.RawMessage) ([]lspLocation, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "{") {
		var loc lspLocation
		if err := json.Unmarshal(raw, &loc); err != nil {
			return nil, err
		}
		return []lspLocation{loc}, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	locs := make([]lspLocation, 0, len(entries))
	for _, entry := range entries {
		var loc lspLocation
		if err := json.Unmarshal(entry, &loc); err != nil {
			return nil, err
		}
		if loc.URI == "" {
			var link lspLocationLink
			if err := json.Unmarshal(entry, &link); err != nil {
				return nil, err
			}
			loc = lspLocation{URI: link.TargetURI, Range: link.TargetSelectionRange}
			if loc.Range == (lspRange{}) {
				loc.Range = link.TargetRange
			}
		}
		if loc.URI != "" {
			locs = append(locs, loc)
		}
	}
	return locs, nil
}

// mapLocations maps the server's URIs onto the paths the editor opens,
// deduplicated in server order: project relative inside the project root,
// absolute and marked external inside one of the given source roots, where
// the dependency sources, the standard library and the stubs live. Whatever
// lies in neither cannot be opened by this editor and is only counted.
func mapLocations(rootURI string, raw []lspLocation, roots []SourceRoot) (locs []Location, outside int) {
	root := uriPath(rootURI)
	locs = []Location{}
	seen := map[Location]bool{}
	for _, l := range raw {
		target := uriPath(l.URI)
		if target == "" {
			outside++
			continue
		}
		loc := Location{Line: l.Range.Start.Line + 1, Character: l.Range.Start.Character}
		if rel, ok := strings.CutPrefix(target, root+"/"); root != "" && ok && rel != "" {
			loc.Path = rel
		} else if _, ok := FindSourceRoot(roots, target); ok {
			loc.Path, loc.External = target, true
		} else {
			outside++
			continue
		}
		if seen[loc] {
			continue
		}
		seen[loc] = true
		locs = append(locs, loc)
	}
	return locs, outside
}

// documentPath is the absolute path of one document the editor addresses:
// a file of the project hangs under the workspace root, a source outside it
// (a dependency, the standard library, a stub) is already absolute and is
// the same path inside the container and out, which is what the cache bind
// is for. Every place that turns an editor path into a file goes through
// here, so there is one notion of a path and not two.
func documentPath(root, p string) string {
	if path.IsAbs(p) {
		return p
	}
	return root + "/" + p
}

// atRequestPosition reports whether one of the answered ranges covers the
// asked position in the asked document, which is what "the cursor already
// sits on the declaration" means for a definition answer. The comparison is
// range containment, not the start line: one server answers the whole
// declaration body, docblock included, so a method's name line lies well
// inside the range and never on its first line.
func atRequestPosition(rootURI string, raw []lspLocation, req Request) bool {
	asked := documentPath(uriPath(rootURI), req.Path)
	for _, l := range raw {
		if uriPath(l.URI) != asked {
			continue
		}
		r := l.Range
		after := req.Line > r.Start.Line || (req.Line == r.Start.Line && req.Character >= r.Start.Character)
		before := req.Line < r.End.Line || (req.Line == r.End.Line && req.Character <= r.End.Character)
		if after && before {
			return true
		}
	}
	return false
}

// uriPath turns a file:// URI into its filesystem path, percent encoding
// unescaped. Anything else answers empty.
func uriPath(uri string) string {
	rest, ok := strings.CutPrefix(uri, "file://")
	if !ok {
		return ""
	}
	path, err := url.PathUnescape(rest)
	if err != nil {
		return rest
	}
	return path
}

// sortReferences orders a usages list for the panel: the file the question
// was asked in first, then the other files of the project alphabetically,
// and the ones outside it last, each file top to bottom. A usage in a
// dependency is a usage in somebody else's code and belongs behind the
// project's own.
func sortReferences(locs []Location, activePath string) {
	sort.SliceStable(locs, func(i, j int) bool {
		a, b := locs[i], locs[j]
		if (a.Path == activePath) != (b.Path == activePath) {
			return a.Path == activePath
		}
		if a.External != b.External {
			return b.External
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Character < b.Character
	})
}
