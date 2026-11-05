package markdown

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// gfm renders GitHub Flavored Markdown. Raw HTML rendering stays disabled (the
// default), so goldmark strips embedded HTML and drops dangerous link schemes,
// which keeps the output safe to inject into a page without a separate
// sanitizer. The chat answers render through it.
var gfm = goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithParserOptions(externalLinks()))

// RenderGFM converts Markdown to HTML.
func RenderGFM(src string) (string, error) {
	var buf bytes.Buffer
	if err := gfm.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Escape is a value the way a line of Markdown holds it as the text it is:
// each ASCII mark that could open markup there, a tag, an emphasis, a strike,
// a link, a code span, an entity or a table cell, stands behind a backslash,
// so "Fix <form> in __init__" reads so on the page instead of losing a word
// to the HTML the renderer drops or turning bold. A value inside a code span
// is no text and is not escaped.
func Escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune("\\`*_~[]<>&|", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Plain reduces Markdown to the words in it, for the places that show a piece
// of an answer where no markup can be rendered: a notification title, a toast,
// a push body. It walks what the parser understood instead of cutting markup
// out with patterns, so a heading, a link, a list and a fenced block all come
// out as what they say, and an escaped mark or an entity as the character the
// page shows. Blocks and line breaks are kept as line breaks; the caller
// decides what a line means where it shows the text.
func Plain(src string) string {
	source := []byte(src)
	var b strings.Builder
	_ = ast.Walk(gfm.Parser().Parse(text.NewReader(source)), func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch node := n.(type) {
		case *ast.Text:
			if entering {
				b.Write(words(node, source))
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
				b.Write(words(node, source))
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

// words is what a run of text says on the page: escapes and entities read
// as the characters they stand for, inside a code span as written.
func words(node *ast.Text, source []byte) []byte {
	segment := node.Segment.Value(source)
	if parent := node.Parent(); parent != nil && parent.Kind() == ast.KindCodeSpan {
		return segment
	}
	return unescaped(segment)
}

// unescaped is a run of text as the renderer writes it, in one pass: a
// backslash before an ASCII mark stands for the mark, an entity for its
// character, and an escaped ampersand opens no entity.
func unescaped(src []byte) []byte {
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '\\' && i+1 < len(src) && util.IsPunct(src[i+1]) {
			out = append(out, src[i+1])
			i++
			continue
		}
		if c == '&' {
			end := i + 1
			for end < len(src) && (src[end] == '#' || util.IsAlphaNumeric(src[end])) {
				end++
			}
			if end < len(src) && src[end] == ';' {
				ref := src[i : end+1]
				if resolved := util.ResolveEntityNames(util.ResolveNumericReferences(ref)); !bytes.Equal(resolved, ref) {
					out = append(out, resolved...)
					i = end
					continue
				}
			}
		}
		out = append(out, c)
	}
	return out
}
