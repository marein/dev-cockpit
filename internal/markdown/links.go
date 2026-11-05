package markdown

import (
	"bytes"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// externalLinks is the transformer every chat renderer carries. It runs after
// the media transformer, so a link that became a player is no link any more.
func externalLinks() parser.Option {
	return parser.WithASTTransformers(util.Prioritized(externalLinkTransformer{}, 200))
}

// externalLinkTransformer marks every link that leaves the cockpit to open in
// its own window: in an installed app a plain link would replace the cockpit's
// own page. The destination alone decides, the request host is not compared,
// so a link to another path of this very server stays in the page.
type externalLinkTransformer struct{}

func (externalLinkTransformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Link:
			if isExternal(node.Destination) {
				markExternal(node)
			}
		case *ast.AutoLink:
			if node.AutoLinkType == ast.AutoLinkURL && isExternal(node.URL(source)) {
				markExternal(node)
			}
		}
		return ast.WalkContinue, nil
	})
}

func markExternal(n ast.Node) {
	n.SetAttributeString("target", []byte("_blank"))
	n.SetAttributeString("rel", []byte("noopener noreferrer"))
}

// isExternal is true for an absolute http or https address, in any case, and
// for a protocol relative one. A bare www. address is what the autolink
// extension renders with http:// in front.
func isExternal(destination []byte) bool {
	d := bytes.TrimSpace(destination)
	return hasFoldPrefix(d, "http://") || hasFoldPrefix(d, "https://") ||
		hasFoldPrefix(d, "//") || hasFoldPrefix(d, "www.")
}

func hasFoldPrefix(b []byte, prefix string) bool {
	return len(b) >= len(prefix) && bytes.EqualFold(b[:len(prefix)], []byte(prefix))
}
