package markdown

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// gfm renders GitHub Flavored Markdown. Raw HTML rendering stays disabled (the
// default), so goldmark strips embedded HTML and drops dangerous link schemes,
// which keeps the output safe to inject into a page without a separate
// sanitizer. The editor preview and the chat answers share it.
var gfm = goldmark.New(goldmark.WithExtensions(extension.GFM))

// RenderGFM converts Markdown to HTML.
func RenderGFM(src string) (string, error) {
	var buf bytes.Buffer
	if err := gfm.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Plain reduces Markdown to the words in it, for the places that show a piece
// of an answer where no markup can be rendered: a notification title, a toast,
// a push body. It walks what the parser understood instead of cutting markup
// out with patterns, so a heading, a link, a list and a fenced block all come
// out as what they say. Blocks and line breaks are kept as line breaks; the
// caller decides what a line means where it shows the text.
func Plain(src string) string {
	source := []byte(src)
	var b strings.Builder
	_ = ast.Walk(gfm.Parser().Parse(text.NewReader(source)), func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch node := n.(type) {
		case *ast.Text:
			if entering {
				b.Write(node.Segment.Value(source))
				if node.SoftLineBreak() || node.HardLineBreak() {
					b.WriteByte('\n')
				}
			}
		case *ast.String:
			if entering {
				b.Write(node.Value)
			}
		case *ast.AutoLink:
			// The only inline whose words are not text children: it says what
			// it links to, so the address is what it says.
			if entering {
				b.Write(node.URL(source))
			}
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			if entering {
				lines := n.Lines()
				for i := 0; i < lines.Len(); i++ {
					line := lines.At(i)
					b.Write(line.Value(source))
				}
			}
			return ast.WalkSkipChildren, nil
		default:
			if !entering && n.Type() == ast.TypeBlock {
				b.WriteByte('\n')
			}
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

// Speech reduces Markdown to what a voice should read out, for the spoken
// answers. It is Plain with the parts no listener wants taken away: a code
// block falls out entirely, a program is not prose, and a bare link address
// says nothing a voice can say. Inline code stays, a spoken sentence may name
// a command; a link's words stay while its destination goes, which the walk
// gives for free because the destination is no child of the link.
func Speech(src string) string {
	source := []byte(src)
	var b strings.Builder
	_ = ast.Walk(gfm.Parser().Parse(text.NewReader(source)), func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch node := n.(type) {
		case *ast.Text:
			if entering {
				b.Write(node.Segment.Value(source))
				if node.SoftLineBreak() || node.HardLineBreak() {
					b.WriteByte('\n')
				}
			}
		case *ast.String:
			if entering {
				b.Write(node.Value)
			}
		case *ast.FencedCodeBlock, *ast.CodeBlock, *ast.AutoLink:
			return ast.WalkSkipChildren, nil
		default:
			if !entering && n.Type() == ast.TypeBlock {
				b.WriteByte('\n')
			}
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}
