package editorintelligence

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func item(label string) lspCompletionItem {
	return lspCompletionItem{Label: label}
}

func TestNormalizeCompletionsRanking(t *testing.T) {
	doc := newDocText("Pri")
	raw := []lspCompletionItem{
		item("aPrint"),
		item("print"),
		item("Print"),
		item("unrelated"),
	}
	from, items := normalizeCompletions(raw, doc, 0, 3)
	if from != 0 {
		t.Fatalf("from = %d", from)
	}
	var labels []string
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	got := strings.Join(labels, ",")
	if got != "Print,print,aPrint" {
		t.Fatalf("ranking: %s", got)
	}
}

func TestNormalizeCompletionsTextEdit(t *testing.T) {
	doc := newDocText("fmt.Pri")
	edit := func(startChar int) *lspTextEdit {
		return &lspTextEdit{
			NewText: "Println",
			Range: &lspRange{
				Start: lspPosition{Line: 0, Character: startChar},
				End:   lspPosition{Line: 0, Character: 7},
			},
		}
	}
	raw := []lspCompletionItem{
		{Label: "Println", TextEdit: edit(4)},
		{Label: "Elsewhere", TextEdit: edit(0)},
	}
	from, items := normalizeCompletions(raw, doc, 0, 7)
	if from != 4 {
		t.Fatalf("from = %d", from)
	}
	if len(items) != 1 || items[0].Insert != "Println" {
		t.Fatalf("items: %+v", items)
	}
}

func TestNormalizeCompletionsInsertReplaceEdit(t *testing.T) {
	doc := newDocText("Pri")
	raw := []lspCompletionItem{
		{Label: "Println", TextEdit: &lspTextEdit{
			NewText: "Println",
			Insert: &lspRange{
				Start: lspPosition{Line: 0, Character: 0},
				End:   lspPosition{Line: 0, Character: 3},
			},
		}},
	}
	_, items := normalizeCompletions(raw, doc, 0, 3)
	if len(items) != 1 {
		t.Fatalf("insert replace edit dropped: %+v", items)
	}
}

func TestNormalizeCompletionsDedupeAndCap(t *testing.T) {
	doc := newDocText("x")
	raw := []lspCompletionItem{
		{Label: "xa", InsertText: "xa", SortText: "0"},
		{Label: "xa", InsertText: "xa", SortText: "1"},
	}
	for i := 0; i < maxCompletionItems+20; i++ {
		raw = append(raw, lspCompletionItem{Label: fmt.Sprintf("x%03d", i)})
	}
	_, items := normalizeCompletions(raw, doc, 0, 1)
	if len(items) != maxCompletionItems {
		t.Fatalf("cap: %d items", len(items))
	}
	count := 0
	for _, it := range items {
		if it.Label == "xa" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("exact duplicate must collapse, got %d", count)
	}
}

// The same class name in several namespaces shares label and insert but
// differs in detail; every namespace variant must stay listed, the exact
// case that hid Symfony's Request behind a project class.
func TestNormalizeCompletionsKeepsNamespaceVariants(t *testing.T) {
	doc := newDocText("Requ")
	raw := []lspCompletionItem{
		{Label: "Request", Detail: "Gaming\\Http\\Request", InsertText: "Request", SortText: "0001"},
		{Label: "Request", Detail: "Symfony\\Component\\HttpFoundation\\Request", InsertText: "Request", SortText: "0002"},
		{Label: "Request", Detail: "Amp\\Http\\Client\\Request", InsertText: "Request", SortText: "0003"},
		{Label: "Request", Detail: "Symfony\\Component\\HttpFoundation\\Request", InsertText: "Request", SortText: "0004"},
	}
	_, items := normalizeCompletions(raw, doc, 0, 4)
	if len(items) != 3 {
		t.Fatalf("expected the three distinct namespaces, got %d: %+v", len(items), items)
	}
	if items[1].Detail != "Symfony\\Component\\HttpFoundation\\Request" {
		t.Fatalf("order: %+v", items)
	}
}

func TestNormalizeCompletionsSnippet(t *testing.T) {
	doc := newDocText("")
	raw := []lspCompletionItem{
		{Label: "for", InsertText: "for ${1:i} := 0; $1 < ${2:n}; $1++ {\n\t$0\n}", InsertTextFormat: 2},
	}
	_, items := normalizeCompletions(raw, doc, 0, 0)
	if len(items) != 1 {
		t.Fatalf("items: %+v", items)
	}
	want := "for i := 0;  < n; ++ {\n\t\n}"
	if items[0].Insert != want {
		t.Fatalf("snippet stripped to %q, want %q", items[0].Insert, want)
	}
}

func TestStripSnippetEscapes(t *testing.T) {
	if got := stripSnippet(`a\$b\}c`); got != "a$b}c" {
		t.Fatalf("escapes: %q", got)
	}
	if got := stripSnippet("plain"); got != "plain" {
		t.Fatalf("plain: %q", got)
	}
	if got := stripSnippet("${TM_FILENAME}"); got != "" {
		t.Fatalf("variable: %q", got)
	}
}

func TestDocumentationText(t *testing.T) {
	if got := documentationText(json.RawMessage(`"plain"`)); got != "plain" {
		t.Fatalf("string doc: %q", got)
	}
	if got := documentationText(json.RawMessage(`{"kind":"markdown","value":"# md"}`)); got != "# md" {
		t.Fatalf("markup doc: %q", got)
	}
	long := strings.Repeat("ä", maxDocumentationChars)
	got := documentationText(json.RawMessage(`"` + long + `"`))
	if !strings.HasSuffix(got, "…") || len(got) > maxDocumentationChars+len("…") {
		t.Fatalf("doc not bounded, len %d", len(got))
	}
	if got := documentationText(nil); got != "" {
		t.Fatalf("empty doc: %q", got)
	}
}

func TestKindMappingCoversPopupTypes(t *testing.T) {
	for kind := 1; kind <= 25; kind++ {
		if completionKinds[kind] == "" {
			t.Fatalf("kind %d unmapped", kind)
		}
	}
}
