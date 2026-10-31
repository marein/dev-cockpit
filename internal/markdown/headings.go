package markdown

import (
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Heading is one heading of a Markdown text, written with # or underlined:
// its level, its words as the page shows them, the markup gone, the byte
// offset in the text its words start at, and the id the preview gives it,
// without the user-content- prefix.
type Heading struct {
	Level  int
	Text   string
	Offset int
	ID     string
}

// Headings answers the headings of src in document order, those inside a
// quote or a list among them. The parse is the preview's, so a line of a
// code block or an HTML block is never one and the ids are the preview's,
// a repeated heading counted in. A heading without words is left out,
// nothing could name it.
func Headings(src string) []Heading {
	source := []byte(src)
	var out []Heading
	doc := preview.Parser().Parse(text.NewReader(source), parser.WithContext(parser.NewContext(parser.WithIDs(headingIDs{}))))
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		heading, ok := n.(*ast.Heading)
		if !ok || !entering {
			return ast.WalkContinue, nil
		}
		words := strings.Join(strings.Fields(inlineText(heading, source)), " ")
		if words != "" && heading.Lines().Len() > 0 {
			id, _ := heading.AttributeString("id")
			slug, _ := id.([]byte)
			out = append(out, Heading{Level: heading.Level, Text: words, Offset: heading.Lines().At(0).Start, ID: string(slug)})
		}
		return ast.WalkSkipChildren, nil
	})
	return out
}

// inlineText is what the inlines below n say: escapes and character
// references resolved outside code, a link's words without its address,
// an autolink's address, and no tag of raw HTML.
func inlineText(n ast.Node, source []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Text:
			value := node.Value(source)
			if !node.IsRaw() {
				value = util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(value)))
			}
			b.Write(value)
			if node.SoftLineBreak() || node.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(node.Value)
		case *ast.AutoLink:
			b.Write(node.Label(source))
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}
