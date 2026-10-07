package web

import (
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/project"
	"golang.org/x/net/html"
	"golang.org/x/text/width"
)

// A file a terminal mentions opens in the editor of the terminal's project.
// Only the server knows which mentions name a file there: the copy view's text
// carries them beside it, a conversation's bubbles carry them in their markup,
// the live terminal asks for the rows around a click or a tap.
type fileLink struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Href  string `json:"href"`
}

// handleFileLinks answers the links of the rows a live terminal was pressed
// on, see pressedFileLinks.
func (s *Server) handleFileLinks(c *gin.Context) {
	ref, ok := s.terminalSessions()[c.Param("id")]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "This terminal is not running."})
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The text could not be read."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"links": pressedFileLinks(req.Text, s.fileFinder(ref.CWD))})
}

// fileFinder answers the links of texts for a terminal in cwd, none for one
// outside every project.
func (s *Server) fileFinder(cwd string) func(string) []fileLink {
	p, ok := s.projectAt(cwd)
	if !ok {
		return func(string) []fileLink { return nil }
	}
	refs := filesystem.FileRefFinder(p.Path)
	return func(text string) []fileLink {
		var links []fileLink
		for _, r := range refs(text) {
			links = append(links, fileLink{Start: r.Start, End: r.End, Href: editorFileURL(p.Name, r)})
		}
		return links
	}
}

// wrapRows is how many rows one mention may run over, as many as the live
// terminal sends around the row it was pressed on.
const wrapRows = 9

// wrappedFileLinks answers the links of terminal text. A line that continues
// in the next may go on there, wrapped by the terminal or by the program, the
// text no longer says which. A mention over such a break links when the joined
// lines name a file, and then takes the place of what the lines alone name.
func wrappedFileLinks(text string, continues func(line, next string) bool, find func(string) []fileLink) []fileLink {
	var bridged []fileLink
	lines := strings.Split(text, "\n")
	for i, at := 0, 0; i < len(lines); i++ {
		joined, breaks := lines[i], []int{}
		for i+1 < len(lines) && len(breaks)+1 < wrapRows && continues(lines[i], lines[i+1]) {
			i++
			breaks = append(breaks, filesystem.UTF16Len(joined))
			joined += lines[i]
		}
		if len(breaks) > 0 {
			for _, link := range find(joined) {
				before, crossed := 0, 0
				for _, b := range breaks {
					if b <= link.Start {
						before++
					}
					if b < link.End {
						crossed++
					}
				}
				if crossed > before {
					bridged = append(bridged, fileLink{Start: at + link.Start + before, End: at + link.End + crossed, Href: link.Href})
				}
			}
		}
		at += filesystem.UTF16Len(joined) + len(breaks) + 1
	}
	links := bridged
	for _, link := range find(text) {
		if !slices.ContainsFunc(bridged, func(b fileLink) bool { return link.Start < b.End && b.Start < link.End }) {
			links = append(links, link)
		}
	}
	return links
}

// pressedFileLinks answers the links of the rows a live terminal was pressed
// on. Rows the terminal wrapped or a program filled to the last column come
// joined as the copy view joins them, and every break of them may be joined.
// Rows a program wrapped at its margin come with the margin and the indent,
// so a line starting with a blank after a break marks them.
func pressedFileLinks(text string, find func(string) []fileLink) []fileLink {
	lines := strings.Split(text, "\n")
	indented := func(line string) bool { return strings.TrimLeftFunc(line, unicode.IsSpace) != line }
	if len(lines) > wrapRows || !slices.ContainsFunc(lines[1:], indented) {
		always := func(string, string) bool { return true }
		return wrappedFileLinks(text, always, find)
	}
	return indentedFileLinks(text, find)
}

// indentedFileLinks answers the links of rows a program wrapped at its margin
// and went on with at an indent. It may as well have started a new line
// there, so a mention over a break links in the longest join that names a
// file, and only where the rows alone name none: what a row names stays as it
// reads.
func indentedFileLinks(text string, find func(string) []fileLink) []fileLink {
	var rows []wrappedRow
	at := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
		rows = append(rows, wrappedRow{strings.TrimRightFunc(trimmed, unicode.IsSpace), at + filesystem.UTF16Len(line[:len(line)-len(trimmed)])})
		at += filesystem.UTF16Len(line) + 1
	}
	links := find(text)
	for span := len(rows); span > 1; span-- {
		for from := 0; from+span <= len(rows); from++ {
			for _, link := range bridgedLinks(rows[from:from+span], find) {
				if !slices.ContainsFunc(links, func(b fileLink) bool { return link.Start < b.End && b.Start < link.End }) {
					links = append(links, link)
				}
			}
		}
	}
	return links
}

// wrappedRow is a row at its UTF-16 offset in the text, without the blanks
// around it.
type wrappedRow struct {
	text string
	at   int
}

// bridgedLinks answers the mentions that run over a break between the rows
// once they are joined.
func bridgedLinks(rows []wrappedRow, find func(string) []fileLink) []fileLink {
	joined, starts := "", make([]int, len(rows))
	for k, row := range rows {
		starts[k] = filesystem.UTF16Len(joined)
		joined += row.text
	}
	rowAt := func(at int) int {
		k := len(starts) - 1
		for starts[k] >= at {
			k--
		}
		return k
	}
	var links []fileLink
	for _, l := range find(joined) {
		if first, last := rowAt(l.Start+1), rowAt(l.End); first < last {
			links = append(links, fileLink{Start: rows[first].at + l.Start - starts[first], End: rows[last].at + l.End - starts[last], Href: l.Href})
		}
	}
	return links
}

// continuesLine says whether a line of a pane cols wide may go on in the next:
// it fills the last column and the next starts without a space.
func continuesLine(line, next string, cols int) bool {
	if cols <= 0 || next == "" || next[0] == ' ' {
		return false
	}
	cells := 0
	for _, r := range line {
		cells++
		if k := width.LookupRune(r).Kind(); k == width.EastAsianWide || k == width.EastAsianFullwidth {
			cells++
		}
	}
	return cells == cols
}

// projectAt answers the project a path lies in.
func (s *Server) projectAt(path string) (project.Project, bool) {
	name := s.projects.ProjectNameFor(path)
	if name == "" {
		return project.Project{}, false
	}
	p, err := s.projects.FindByName(name)
	return p, err == nil
}

func editorFileURL(project string, r filesystem.FileRef) string {
	q := url.Values{"file": {r.Path}}
	if r.Line > 0 {
		q.Set("line", strconv.Itoa(r.Line))
	}
	if r.Col > 0 {
		q.Set("col", strconv.Itoa(r.Col))
	}
	return "/projects/" + url.PathEscape(project) + "/editor?" + q.Encode()
}

// linkedHTML puts the file links into rendered HTML, around the mentions in
// each of its texts outside a link, so they land on the text nodes the browser
// builds from it. The first text beyond the budget, counted in bytes, ends
// the linking, it and everything after it stays unlinked.
// A text is written escaped anew, its raw form is gone once it was read.
func linkedHTML(fragment template.HTML, budget *int, find func(string) []fileLink) template.HTML {
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(string(fragment)))
	inLink := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return template.HTML(b.String())
		case html.TextToken:
			text := string(z.Text())
			if inLink == 0 && len(text) > *budget {
				*budget = 0
			}
			if inLink > 0 || *budget == 0 {
				b.WriteString(template.HTMLEscapeString(text))
				continue
			}
			*budget -= len(text)
			b.WriteString(linkedText(text, find(text)))
			continue
		case html.StartTagToken:
			if name, _ := z.TagName(); string(name) == "a" {
				inLink++
			}
		case html.EndTagToken:
			if name, _ := z.TagName(); string(name) == "a" && inLink > 0 {
				inLink--
			}
		}
		b.Write(z.Raw())
	}
}

// linkedText escapes text with an anchor around every link, links in order and
// apart as FileRefFinder answers them.
func linkedText(text string, links []fileLink) string {
	units := utf16.Encode([]rune(text))
	part := func(from, to int) string { return template.HTMLEscapeString(string(utf16.Decode(units[from:to]))) }
	var b strings.Builder
	at := 0
	for _, link := range links {
		b.WriteString(part(at, link.Start))
		b.WriteString(`<a href="` + template.HTMLEscapeString(link.Href) + `" data-file-link>` + part(link.Start, link.End) + "</a>")
		at = link.End
	}
	b.WriteString(part(at, len(units)))
	return b.String()
}
