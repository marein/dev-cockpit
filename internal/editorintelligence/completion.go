package editorintelligence

import (
	"encoding/json"
	"sort"
	"strings"
)

// maxCompletionItems bounds the list a response carries. The popup stays
// short and CodeMirror's own filtering narrows it further while typing.
const maxCompletionItems = 50

// maxDocumentationChars bounds the documentation preview per item.
const maxDocumentationChars = 1200

// lspCompletionItem is the subset of the LSP CompletionItem the editor
// consumes.
type lspCompletionItem struct {
	Label            string          `json:"label"`
	Kind             int             `json:"kind"`
	Detail           string          `json:"detail"`
	Documentation    json.RawMessage `json:"documentation"`
	SortText         string          `json:"sortText"`
	FilterText       string          `json:"filterText"`
	InsertText       string          `json:"insertText"`
	InsertTextFormat int             `json:"insertTextFormat"`
	TextEdit         *lspTextEdit    `json:"textEdit"`
}

// lspTextEdit covers both the plain TextEdit and the InsertReplaceEdit
// shape; for the latter the insert range decides.
type lspTextEdit struct {
	NewText string    `json:"newText"`
	Range   *lspRange `json:"range"`
	Insert  *lspRange `json:"insert"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Item is one normalized completion choice. Insert applies at the shared
// word range the response carries, so the client can hand the plain string
// to CodeMirror and position mapping keeps working while the user types.
type Item struct {
	Label  string `json:"label"`
	Kind   string `json:"kind,omitempty"`
	Detail string `json:"detail,omitempty"`
	Doc    string `json:"doc,omitempty"`
	Insert string `json:"insert"`
}

// completionKinds maps LSP completion item kinds to the CodeMirror
// completion type names that pick the popup icon.
var completionKinds = map[int]string{
	1:  "text",
	2:  "method",
	3:  "function",
	4:  "function",
	5:  "property",
	6:  "variable",
	7:  "class",
	8:  "interface",
	9:  "namespace",
	10: "property",
	11: "constant",
	12: "constant",
	13: "enum",
	14: "keyword",
	15: "text",
	16: "constant",
	17: "text",
	18: "text",
	19: "namespace",
	20: "constant",
	21: "constant",
	22: "type",
	23: "class",
	24: "keyword",
	25: "type",
}

// normalizeCompletions converts raw LSP items into ranked editor items that
// all apply at the word range starting at wordStart16. Items whose edit
// starts elsewhere are dropped instead of guessed at.
func normalizeCompletions(raw []lspCompletionItem, doc *docText, line, char int) (int, []Item) {
	wordStart, prefix := doc.wordStart16(line, char)
	type scored struct {
		item     Item
		rank     int
		sortText string
	}
	candidates := make([]scored, 0, len(raw))
	for i := range raw {
		it := &raw[i]
		insert := it.InsertText
		if it.TextEdit != nil {
			editRange := it.TextEdit.Range
			if it.TextEdit.Insert != nil {
				editRange = it.TextEdit.Insert
			}
			if editRange != nil {
				start, ok := doc.offset16(editRange.Start.Line, editRange.Start.Character)
				if !ok || start != wordStart {
					continue
				}
			}
			insert = it.TextEdit.NewText
		}
		if insert == "" {
			insert = it.Label
		}
		if it.InsertTextFormat == 2 {
			insert = stripSnippet(insert)
		}
		if insert == "" {
			continue
		}
		rank, ok := matchRank(prefix, filterText(it))
		if !ok {
			continue
		}
		candidates = append(candidates, scored{
			item: Item{
				Label:  it.Label,
				Kind:   completionKinds[it.Kind],
				Detail: it.Detail,
				Doc:    documentationText(it.Documentation),
				Insert: insert,
			},
			rank:     rank,
			sortText: sortText(it),
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank < candidates[j].rank
		}
		if candidates[i].sortText != candidates[j].sortText {
			return candidates[i].sortText < candidates[j].sortText
		}
		return candidates[i].item.Label < candidates[j].item.Label
	})
	// A duplicate needs label, insert and detail to match: different
	// symbols regularly share one insert text (same class name in several
	// namespaces), and the detail carries what tells them apart.
	type dedupeKey struct {
		label  string
		insert string
		detail string
	}
	seen := map[dedupeKey]bool{}
	items := make([]Item, 0, min(len(candidates), maxCompletionItems))
	for _, c := range candidates {
		key := dedupeKey{label: c.item.Label, insert: c.item.Insert, detail: c.item.Detail}
		if seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, c.item)
		if len(items) >= maxCompletionItems {
			break
		}
	}
	return wordStart, items
}

func filterText(it *lspCompletionItem) string {
	if it.FilterText != "" {
		return it.FilterText
	}
	return it.Label
}

func sortText(it *lspCompletionItem) string {
	if it.SortText != "" {
		return it.SortText
	}
	return it.Label
}

// matchRank scores how well the candidate matches the typed prefix: exact
// prefix, case insensitive prefix, then in order subsequence. Candidates
// that do not even match as a subsequence are dropped.
func matchRank(prefix, candidate string) (int, bool) {
	if prefix == "" {
		return 0, true
	}
	if strings.HasPrefix(candidate, prefix) {
		return 0, true
	}
	if strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(prefix)) {
		return 1, true
	}
	if isSubsequence(strings.ToLower(prefix), strings.ToLower(candidate)) {
		return 2, true
	}
	return 0, false
}

func isSubsequence(needle, haystack string) bool {
	i := 0
	for _, r := range haystack {
		if i >= len(needle) {
			return true
		}
		if strings.HasPrefix(needle[i:], string(r)) {
			i += len(string(r))
		}
	}
	return i >= len(needle)
}

// documentationText extracts the plain documentation string from either the
// bare string or the MarkupContent shape, bounded for the popup preview.
func documentationText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		var markup struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(raw, &markup); err != nil {
			return ""
		}
		text = markup.Value
	}
	text = strings.TrimSpace(text)
	if len(text) > maxDocumentationChars {
		cut := maxDocumentationChars
		for cut > 0 && text[cut]&0xC0 == 0x80 {
			cut--
		}
		text = text[:cut] + "…"
	}
	return text
}

// stripSnippet reduces an LSP snippet to its plain text: placeholders keep
// their default text, tab stops and variables disappear. Escaped characters
// are unescaped.
func stripSnippet(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\\' && i+1 < len(s) {
			out.WriteByte(s[i+1])
			i++
			continue
		}
		if ch != '$' {
			out.WriteByte(ch)
			continue
		}
		if i+1 < len(s) && s[i+1] == '{' {
			depth := 1
			j := i + 2
			start := j
			content := ""
			for ; j < len(s); j++ {
				if s[j] == '\\' {
					j++
					continue
				}
				if s[j] == '{' {
					depth++
				}
				if s[j] == '}' {
					depth--
					if depth == 0 {
						content = s[start:j]
						break
					}
				}
			}
			if _, rest, found := strings.Cut(content, ":"); found {
				out.WriteString(stripSnippet(rest))
			}
			i = j
			continue
		}
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == i+1 {
			for j < len(s) && (isWordRune(rune(s[j])) && s[j] < 0x80) {
				j++
			}
		}
		i = j - 1
	}
	return out.String()
}
