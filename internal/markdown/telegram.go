package markdown

import (
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// The Telegram Bot API parses a tiny subset of HTML and nothing else. Handing
// it Markdown raw ends in 400s, and MarkdownV2 is worse than useless here: it
// asks for a dozen characters to be escaped, so one bare underscore in an
// identifier takes the whole message with it. So an answer is translated into
// that subset, and the subset is all there is: b, i, code, pre, a, s.
//
// Everything that cannot be mapped becomes text and is never dropped: a
// heading turns bold, a list keeps its markers, a table is passed through as a
// code block because Telegram has none. Whoever sends the result must still be
// able to fall back to plain text on a 400, because a bug in here may make an
// answer plain, never lose it.

// telegramEscaper is the whole escaping the Bot API asks for in text.
var telegramEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// telegramHrefEscaper also takes the quote, which would otherwise end the
// attribute it sits in. That is about the attribute, not about text.
var telegramHrefEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

// RenderTelegramHTML translates Markdown into the HTML subset Telegram parses.
func RenderTelegramHTML(src string) string {
	source := []byte(src)
	doc := gfm.Parser().Parse(text.NewReader(source))
	return strings.TrimSpace(telegramBlocks(doc, source, "\n\n"))
}

// telegramBlocks renders the block children of a node, joined by sep. The
// separator is a blank line at the top level and a single newline inside a
// list item, which is what keeps a list from being double spaced.
func telegramBlocks(n ast.Node, source []byte, sep string) string {
	var parts []string
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if rendered := telegramBlock(child, source); rendered != "" {
			parts = append(parts, rendered)
		}
	}
	return strings.Join(parts, sep)
}

func telegramBlock(n ast.Node, source []byte) string {
	switch node := n.(type) {
	case *ast.Heading:
		// Telegram has no headings. Bold is what a heading is for.
		return "<b>" + telegramInlines(node, source) + "</b>"
	case *ast.Paragraph, *ast.TextBlock:
		return telegramInlines(n, source)
	case *ast.FencedCodeBlock:
		return telegramCode(nodeLines(n, source), string(node.Language(source)))
	case *ast.CodeBlock:
		return telegramCode(nodeLines(n, source), "")
	case *ast.HTMLBlock:
		// Raw HTML in an answer is not markup this may pass on: Telegram would
		// refuse the message, and it is not ours to render either.
		return telegramEscaper.Replace(strings.TrimRight(nodeLines(n, source), "\n"))
	case *ast.List:
		return telegramList(node, source)
	case *ast.Blockquote:
		return telegramPrefix(telegramBlocks(node, source, "\n\n"), "&gt; ")
	case *ast.ThematicBreak:
		return "———"
	case *east.Table:
		// Telegram knows no tables, and a table pulled apart into lines is
		// unreadable. As a code block it keeps its columns.
		return telegramCode(telegramTable(node, source), "")
	default:
		return telegramBlocks(n, source, "\n\n")
	}
}

// telegramList renders a list, one line per item, nested content indented
// under its marker.
func telegramList(list *ast.List, source []byte) string {
	number := list.Start
	var lines []string
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		marker := "- "
		if list.IsOrdered() {
			marker = strconv.Itoa(number) + ". "
			number++
		}
		body := telegramBlocks(item, source, "\n")
		lines = append(lines, marker+telegramIndent(body, strings.Repeat(" ", len(marker))))
	}
	return strings.Join(lines, "\n")
}

// telegramIndent indents every line but the first, so a wrapped list item
// stays under its own marker.
func telegramIndent(body, indent string) string {
	lines := strings.Split(body, "\n")
	for i := 1; i < len(lines); i++ {
		if lines[i] != "" {
			lines[i] = indent + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func telegramPrefix(body, prefix string) string {
	lines := strings.Split(body, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

// telegramCode renders a code block. The language rides on the inner code
// element, which is where Telegram looks for it.
func telegramCode(body, language string) string {
	code := telegramEscaper.Replace(strings.TrimRight(body, "\n"))
	if lang := strings.TrimSpace(language); lang != "" {
		return `<pre><code class="language-` + telegramHrefEscaper.Replace(lang) + `">` + code + "</code></pre>"
	}
	return "<pre>" + code + "</pre>"
}

// telegramTable lays a GFM table out as text, columns joined by pipes.
func telegramTable(table *east.Table, source []byte) string {
	var rows []string
	for row := table.FirstChild(); row != nil; row = row.NextSibling() {
		var cells []string
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cells = append(cells, strings.TrimSpace(string(nodeText(cell, source))))
		}
		if len(cells) > 0 {
			rows = append(rows, strings.Join(cells, " | "))
		}
	}
	return strings.Join(rows, "\n")
}

func telegramInlines(n ast.Node, source []byte) string {
	var b strings.Builder
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		b.WriteString(telegramInline(child, source))
	}
	return b.String()
}

func telegramInline(n ast.Node, source []byte) string {
	switch node := n.(type) {
	case *ast.Text:
		out := telegramEscaper.Replace(string(node.Segment.Value(source)))
		if node.SoftLineBreak() || node.HardLineBreak() {
			out += "\n"
		}
		return out
	case *ast.String:
		return telegramEscaper.Replace(string(node.Value))
	case *ast.CodeSpan:
		return "<code>" + telegramEscaper.Replace(string(nodeText(node, source))) + "</code>"
	case *ast.Emphasis:
		if node.Level >= 2 {
			return "<b>" + telegramInlines(node, source) + "</b>"
		}
		return "<i>" + telegramInlines(node, source) + "</i>"
	case *east.Strikethrough:
		return "<s>" + telegramInlines(node, source) + "</s>"
	case *ast.Link:
		return telegramLink(string(node.Destination), telegramInlines(node, source))
	case *ast.AutoLink:
		url := string(node.URL(source))
		return telegramLink(url, telegramEscaper.Replace(url))
	case *ast.Image:
		// Telegram shows no image from a link, so it becomes what it says plus
		// where it sits, which is more than a broken tag would be.
		label := telegramInlines(node, source)
		if strings.TrimSpace(label) == "" {
			label = telegramEscaper.Replace(string(node.Destination))
		}
		return telegramLink(string(node.Destination), label)
	case *ast.RawHTML:
		return telegramEscaper.Replace(rawHTMLText(node, source))
	default:
		return telegramInlines(n, source)
	}
}

// telegramLink writes an anchor for the schemes Telegram opens. Anything else
// keeps its words: an unknown scheme in an href is a refused message, and the
// text is what the answer wanted to say anyway.
func telegramLink(destination, label string) string {
	url := strings.TrimSpace(destination)
	lower := strings.ToLower(url)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		if strings.TrimSpace(label) == "" {
			return telegramEscaper.Replace(url)
		}
		return label
	}
	return `<a href="` + telegramHrefEscaper.Replace(url) + `">` + label + "</a>"
}

// nodeLines is the raw source of a block that carries its content as lines.
func nodeLines(n ast.Node, source []byte) string {
	var b strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		line := lines.At(i)
		b.Write(line.Value(source))
	}
	return b.String()
}

// rawHTMLText is the source of a raw inline HTML node.
func rawHTMLText(n *ast.RawHTML, source []byte) string {
	var b strings.Builder
	for i := 0; i < n.Segments.Len(); i++ {
		segment := n.Segments.At(i)
		b.Write(segment.Value(source))
	}
	return b.String()
}
