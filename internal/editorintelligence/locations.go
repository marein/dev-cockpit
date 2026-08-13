package editorintelligence

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

// Location is one navigation target inside the project, in editor
// coordinates: Path is project relative, Line is 1-based, Character a
// 0-based UTF-16 offset into that line, both as the server reported them.
type Location struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Character int    `json:"character"`
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

// projectLocations maps the server's URIs onto project relative paths,
// deduplicated in server order. Targets outside the project root (a stub
// inside the server's own install, a module cache) cannot be opened by this
// editor and are only counted.
func projectLocations(rootURI string, raw []lspLocation) (locs []Location, outside int) {
	root := uriPath(rootURI)
	locs = []Location{}
	seen := map[Location]bool{}
	for _, l := range raw {
		target := uriPath(l.URI)
		if target == "" || root == "" {
			outside++
			continue
		}
		rel, ok := strings.CutPrefix(target, root+"/")
		if !ok || rel == "" {
			outside++
			continue
		}
		loc := Location{Path: rel, Line: l.Range.Start.Line + 1, Character: l.Range.Start.Character}
		if seen[loc] {
			continue
		}
		seen[loc] = true
		locs = append(locs, loc)
	}
	return locs, outside
}

// atRequestPosition reports whether one of the answered ranges covers the
// asked position in the asked document, which is what "the cursor already
// sits on the declaration" means for a definition answer. The comparison is
// range containment, not the start line: one server answers the whole
// declaration body, docblock included, so a method's name line lies well
// inside the range and never on its first line.
func atRequestPosition(rootURI string, raw []lspLocation, req Request) bool {
	root := uriPath(rootURI)
	for _, l := range raw {
		rel, ok := strings.CutPrefix(uriPath(l.URI), root+"/")
		if !ok || rel != req.Path {
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
// was asked in first, then the other files alphabetically, each file top to
// bottom.
func sortReferences(locs []Location, activePath string) {
	sort.SliceStable(locs, func(i, j int) bool {
		a, b := locs[i], locs[j]
		if (a.Path == activePath) != (b.Path == activePath) {
			return a.Path == activePath
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
