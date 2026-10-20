package markdown

import (
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Fence is a fenced code block standing at the top level of a document: the
// language its info string names and the lines between its fences.
type Fence struct {
	Language string
	Body     string
}

// Fences answers the fenced code blocks at the top level of src, in document
// order. A block inside a list or a quote is no top level block.
func Fences(src string) []Fence {
	source := []byte(src)
	var out []Fence
	for _, n := range topFences(gfm.Parser().Parse(text.NewReader(source))) {
		block := n.(*ast.FencedCodeBlock)
		var body strings.Builder
		lines := block.Lines()
		for i := 0; i < lines.Len(); i++ {
			line := lines.At(i)
			body.Write(line.Value(source))
		}
		out = append(out, Fence{Language: string(block.Language(source)), Body: body.String()})
	}
	return out
}

func topFences(doc ast.Node) []ast.Node {
	var out []ast.Node
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		if n.Kind() == ast.KindFencedCodeBlock {
			out = append(out, n)
		}
	}
	return out
}
