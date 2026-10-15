package editorintelligence

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var phpQualifiedName = regexp.MustCompile(`^[A-Za-z_\x{80}-\x{10FFFF}][A-Za-z0-9_\x{80}-\x{10FFFF}]*(\\[A-Za-z_\x{80}-\x{10FFFF}][A-Za-z0-9_\x{80}-\x{10FFFF}]*)*$`)

// phpClassKeywords are the declarations whose name `Foo::class` answers.
var phpClassKeywords = map[string]bool{"class": true, "interface": true, "trait": true, "enum": true}

var phpClassModifiers = map[string]bool{"final": true, "abstract": true, "readonly": true}

// phpClassName reads the fully qualified class name off an intelephense
// hover, empty when the hovered symbol is no class. The server titles a
// hover with the symbol's resolved name in strong emphasis and shows its
// declaration in a php block below, and it resolves an import or an alias to
// the class it stands for. A function, a method, a constant or a variable is
// titled the same way, so the title alone proves nothing: the declaration
// has to be a class, an interface, a trait or an enum and carry the title's
// last segment as its name. Only the first section counts, the hover of
// `new Foo()` appends the constructor's below a rule.
func phpClassName(hover string) string {
	src := []byte(hover)
	doc := goldmark.DefaultParser().Parse(text.NewReader(src))
	title, declaration := "", ""
	for n := doc.FirstChild(); n != nil && n.Kind() != ast.KindThematicBreak; n = n.NextSibling() {
		switch block := n.(type) {
		case *ast.Paragraph:
			if n == doc.FirstChild() {
				title = strongTitle(block, src)
			}
		case *ast.FencedCodeBlock:
			if declaration == "" && string(block.Language(src)) == "php" {
				declaration = firstDeclaration(block, src)
			}
		}
	}
	title = strings.TrimPrefix(title, `\`)
	if !phpQualifiedName.MatchString(title) {
		return ""
	}
	short := title[strings.LastIndex(title, `\`)+1:]
	words := strings.Fields(declaration)
	for len(words) > 0 && phpClassModifiers[words[0]] {
		words = words[1:]
	}
	if len(words) < 2 || !phpClassKeywords[words[0]] || !strings.EqualFold(phpLabel(words[1]), short) {
		return ""
	}
	return title
}

func strongTitle(p *ast.Paragraph, src []byte) string {
	strong, ok := p.FirstChild().(*ast.Emphasis)
	if !ok || strong.Level != 2 || strong.NextSibling() != nil {
		return ""
	}
	var b strings.Builder
	for c := strong.FirstChild(); c != nil; c = c.NextSibling() {
		switch inline := c.(type) {
		case *ast.Text:
			if inline.IsRaw() {
				b.Write(inline.Value(src))
			} else {
				b.Write(util.UnescapePunctuations(inline.Value(src)))
			}
		case *ast.String:
			b.Write(inline.Value)
		default:
			return ""
		}
	}
	return b.String()
}

func firstDeclaration(block *ast.FencedCodeBlock, src []byte) string {
	lines := block.Lines()
	for i := 0; i < lines.Len(); i++ {
		segment := lines.At(i)
		line := strings.TrimSpace(string(segment.Value(src)))
		if line != "" && line != "<?php" {
			return line
		}
	}
	return ""
}

// The colon of a backed enum ends its name like any rune a PHP label cannot
// hold.
func phpLabel(word string) string {
	for i, r := range word {
		if !(r == '_' || r >= 0x80 || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return word[:i]
		}
	}
	return word
}
