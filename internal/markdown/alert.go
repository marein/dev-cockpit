package markdown

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

var alertKinds = map[string]struct{ title, color, icon string }{
	"NOTE":      {"Note", "blue", "ti-info-circle"},
	"TIP":       {"Tip", "green", "ti-bulb"},
	"IMPORTANT": {"Important", "purple", "ti-message-report"},
	"WARNING":   {"Warning", "yellow", "ti-alert-triangle"},
	"CAUTION":   {"Caution", "red", "ti-alert-octagon"},
}

// alerts turns a quote whose first line is [!NOTE] or one of its kin into an
// alert. Like GitHub it takes only a quote at the top of the document that
// says something after the marker.
type alerts struct{}

func (alerts) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		quote, ok := n.(*ast.Blockquote)
		if !ok {
			continue
		}
		first, ok := quote.FirstChild().(*ast.Paragraph)
		if !ok {
			continue
		}
		marker := first.Lines().At(0)
		label := strings.TrimSpace(string(marker.Value(source)))
		if !strings.HasPrefix(label, "[!") || !strings.HasSuffix(label, "]") {
			continue
		}
		kind, ok := alertKinds[strings.ToUpper(label[2:len(label)-1])]
		if !ok || first.Lines().Len() == 1 && first.NextSibling() == nil {
			continue
		}
		for c := first.FirstChild(); c != nil; c = first.FirstChild() {
			if t, isText := c.(*ast.Text); !isText || t.Segment.Start >= marker.Stop {
				break
			}
			first.RemoveChild(first, c)
		}
		if first.ChildCount() == 0 {
			quote.RemoveChild(quote, first)
		}
		title := ast.NewParagraph()
		title.SetAttributeString("class", "text-"+kind.color+" fw-medium mb-2")
		line := ast.NewString(fmt.Appendf(nil, `<i aria-hidden="true" class="ti %s"></i> %s`, kind.icon, kind.title))
		line.SetCode(true)
		title.AppendChild(title, line)
		quote.SetAttributeString("class", "border-"+kind.color)
		quote.InsertBefore(quote, quote.FirstChild(), title)
	}
}
