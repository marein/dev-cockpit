package markdown

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
	nethtml "golang.org/x/net/html"
)

// preview keeps a file's raw HTML, so its output is fit for the editor's
// preview alone. Tables align by attribute, the policy drops the style
// attribute goldmark would use instead.
var preview = goldmark.New(
	goldmark.WithExtensions(
		extension.Linkify, extension.Strikethrough, extension.TaskList,
		extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute)),
	),
	goldmark.WithParserOptions(parser.WithAutoHeadingID(), parser.WithASTTransformers(util.Prioritized(alerts{}, 100))),
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

var previewPolicy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowElements("kbd")
	p.AllowAttrs("align").Globally()
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^[\p{L}\p{N}\p{M}\p{Pc}-]*$`)).OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	p.AllowAttrs("checked", "disabled").OnElements("input")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^(border-[a-z]+|text-[a-z]+ fw-medium mb-2|ti ti-[a-z-]+)$`)).OnElements("blockquote", "p", "i")
	p.AllowAttrs("aria-hidden").OnElements("i")
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return p
}()

var previewLooks = map[string]string{"table": "table", "details": "mb-3"}

// RenderPreview converts a file of the editor to HTML for its preview.
func RenderPreview(src string) (string, error) {
	if len(src) > filesystem.MaxEditableBytes {
		return "", filesystem.ErrTooLarge
	}
	var buf bytes.Buffer
	if err := preview.Convert([]byte(src), &buf, parser.WithContext(parser.NewContext(parser.WithIDs(headingIDs{})))); err != nil {
		return "", err
	}
	return scoped(previewPolicy.Sanitize(buf.String())), nil
}

// headingIDs gives a heading the id GitHub gives it, letters of every
// script kept, so a table of contents written for GitHub jumps.
type headingIDs map[string]bool

func (ids headingIDs) Generate(value []byte, _ ast.NodeKind) []byte {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r == ' ':
			return '-'
		case r == '-' || unicode.In(r, unicode.L, unicode.N, unicode.M, unicode.Pc):
			return r
		}
		return -1
	}, strings.ToLower(string(value)))
	id := slug
	for n := 1; ids[id]; n++ {
		id = fmt.Sprintf("%s-%d", slug, n)
	}
	ids.Put([]byte(id))
	return []byte(id)
}

func (ids headingIDs) Put(value []byte) { ids[string(value)] = true }

// scoped gives every id GitHub's user-content- prefix and drops every name,
// so no element of the file stands in for a global the page looks up.
func scoped(sanitized string) string {
	var b strings.Builder
	z := nethtml.NewTokenizer(strings.NewReader(sanitized))
	for z.Next() != nethtml.ErrorToken {
		t := z.Token()
		attrs := t.Attr[:0]
		for _, a := range t.Attr {
			if a.Key == "id" {
				a.Val = "user-content-" + a.Val
			}
			if a.Key != "name" {
				attrs = append(attrs, a)
			}
		}
		if look, ok := previewLooks[t.Data]; ok && t.Type == nethtml.StartTagToken {
			attrs = append(attrs, nethtml.Attribute{Key: "class", Val: look})
		}
		t.Attr = attrs
		b.WriteString(t.String())
	}
	return b.String()
}
