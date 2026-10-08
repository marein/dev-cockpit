package web

import (
	"html/template"
	"log"
	"net/url"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
	"golang.org/x/net/html"
	"golang.org/x/text/width"
)

// A file a terminal mentions opens in the editor of the terminal's project.
// Only the server knows which mentions name a file there: the live terminal
// asks for the words it shows and marks them in xterm's own buffer
// (@dc/termlinks), the copy view's text carries them beside it, a
// conversation's bubbles carry them in their markup.

// fileLinker links the mentions of the files of one project.
type fileLinker struct {
	refs    *filesystem.FileRefs
	project string
	root    string
}

// fileLinker answers the linker of a terminal in cwd, nil for one outside
// every project, which links nothing.
func (s *Server) fileLinker(cwd string) *fileLinker {
	p, ok := s.projectAt(cwd)
	if !ok {
		return nil
	}
	return &fileLinker{refs: filesystem.NewFileRefs(p.Path), project: p.Name, root: p.Path}
}

// linksPanicked logs the first panic of the linking only, a bug there may hit
// every chunk of every stream.
var linksPanicked sync.Once

// recoverLinks, deferred, stops a panic of the linking and calls fallback, so
// a bug in it costs the links, never the terminal or the page.
func recoverLinks(fallback func()) {
	if r := recover(); r != nil {
		linksPanicked.Do(func() { log.Printf("file links: %v\n%s", r, debug.Stack()) })
		fallback()
	}
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

// fileLink is a link in the copy view's text, in the browser's UTF-16 units.
type fileLink struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Href  string `json:"href"`
}

// copyLinkBudget is how much text of one page is searched for file links, in
// bytes. Text dense with paths costs about 200ms per MiB, a page may hold up
// to coder.TranscriptCap, so a quarter of that keeps a page at about 50ms.
const copyLinkBudget = coder.TranscriptCap / 4

// newestFileLinks links the lines at the end of a terminal's text, where the
// view opens, as far back as copyLinkBudget reaches. They link as the live
// terminal links them.
func (l *fileLinker) newestFileLinks(text string, cols int) (links []fileLink) {
	if l == nil {
		return nil
	}
	defer recoverLinks(func() { links = nil })
	from := 0
	if cut := len(text) - copyLinkBudget; cut > 0 {
		from = len(text)
		if i := strings.IndexByte(text[cut:], '\n'); i >= 0 {
			from = cut + i + 1
		}
	}
	at, units := 0, 0
	for _, span := range l.rowSpans(text[from:], cols) {
		start, end := from+span.start, from+span.end
		units += filesystem.UTF16Len(text[at:start])
		link := fileLink{Start: units, End: units + filesystem.UTF16Len(text[start:end]), Href: editorFileURL(l.project, span.ref)}
		links = append(links, link)
		at, units = end, link.End
	}
	return links
}

// linkCopyViews links the file mentions in the bubbles' words and tool lines,
// the newest first, which the view opens on.
func (l *fileLinker) linkCopyViews(views []render.ChatMessageView) {
	if l == nil {
		return
	}
	budget := copyLinkBudget
	for i := len(views) - 1; i >= 0; i-- {
		view := &views[i]
		view.HTML = l.linkedHTML(view.HTML, &budget)
		for j := range view.Parts {
			view.Parts[j].HTML = l.linkedHTML(view.Parts[j].HTML, &budget)
		}
	}
}

// linkedHTML puts the file links into rendered HTML, around the mentions in
// each of its texts outside a link, so they land on the text nodes the browser
// builds from it. The first text beyond the budget, counted in bytes, ends
// the linking, it and everything after it stays unlinked.
// A text is written escaped anew, its raw form is gone once it was read.
func (l *fileLinker) linkedHTML(fragment template.HTML, budget *int) (linked template.HTML) {
	defer recoverLinks(func() { linked = fragment })
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
			b.WriteString(l.linkedText(text))
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

// linkedText escapes text with an anchor around every mention.
func (l *fileLinker) linkedText(text string) string {
	var b strings.Builder
	at := 0
	for _, ref := range l.refs.Find(text) {
		b.WriteString(template.HTMLEscapeString(text[at:ref.Start]))
		b.WriteString(`<a href="` + template.HTMLEscapeString(editorFileURL(l.project, ref)) + `" data-file-link>` + template.HTMLEscapeString(text[ref.Start:ref.End]) + "</a>")
		at = ref.End
	}
	b.WriteString(template.HTMLEscapeString(text[at:]))
	return b.String()
}

// fileSpan is the bytes of a text one mention shows, or one row's part of a
// mention a program wrapped.
type fileSpan struct {
	start, end int
	ref        filesystem.FileRef
}

// rowWord is a word of terminal text, the bytes between two separators on one
// row, and the columns it takes there.
type rowWord struct {
	start, end int
	row        int
	col, upTo  int
}

// termRow is a row of terminal text and where its text stands, from the
// column of its first non blank cell to past its last one, upTo 0 for a blank
// row. A frame is blank.
type termRow struct {
	text      string
	col, upTo int
}

// rowWords answers the words of text and its rows. A word drawn of box
// drawing and block characters only is the frame a program puts around its
// text, a border or a scroll bar, and no word.
func rowWords(text string) ([]rowWord, []termRow) {
	var words []rowWord
	var rows []termRow
	at := 0
	for row, line := range strings.Split(text, "\n") {
		r := termRow{text: line}
		col, from, fromCol := 0, -1, 0
		end := func(i int) {
			if from >= 0 && !isFrame(line[from:i]) {
				words = append(words, rowWord{start: at + from, end: at + i, row: row, col: fromCol, upTo: col})
			}
			from = -1
		}
		for i, c := range line {
			if filesystem.SeparatorAt(line, i) > 0 {
				end(i)
			} else if from < 0 {
				from, fromCol = i, col
			}
			if !blank(c) {
				if r.upTo == 0 {
					r.col = col
				}
				r.upTo = col + cellWidth(c)
			}
			col += cellWidth(c)
		}
		end(len(line))
		rows = append(rows, r)
		at += len(line) + 1
	}
	return words, rows
}

func isFrame(word string) bool {
	for _, r := range word {
		if !frame(r) {
			return false
		}
	}
	return true
}

func frame(r rune) bool { return r >= 0x2500 && r <= 0x259f }

func blank(r rune) bool { return unicode.IsSpace(r) || frame(r) }

func cellWidth(r rune) int {
	if k := width.LookupRune(r).Kind(); k == width.EastAsianWide || k == width.EastAsianFullwidth {
		return 2
	}
	return 1
}

// startsWordAt says whether a word starts at col of the row, after a blank.
func (r termRow) startsWordAt(col int) bool {
	at, after := 0, false
	for _, c := range r.text {
		if at >= col {
			return at == col && after && !blank(c)
		}
		after = blank(c)
		at += cellWidth(c)
	}
	return false
}

// rowJoin is how the text of a row may run on in the row below.
type rowJoin uint8

const (
	notJoined rowJoin = iota
	// joinedFull: the row fills the last column and the next goes on in the
	// first, as the terminal wraps. A mention over the break wins over what
	// the rows name alone.
	joinedFull
	// joinedIndent: a program filled the row up to its margin and goes on in
	// the next at an indent where a word of the row starts, as claude wraps a
	// tool line. What a row names alone stays.
	joinedIndent
)

// rowJoins answers how each row joins the next, the rule @dc/termlinks
// follows on the live screen. A row that joins a neighbour full is never part
// of an indented block. Of a block, the rows at one indent and the row above
// them where a word starts at it, only a row that reaches the margin, the
// farthest any row of the block ends, runs on.
func rowJoins(rows []termRow, cols int) []rowJoin {
	joins := make([]rowJoin, len(rows))
	full := func(r int) bool {
		return r >= 0 && r+1 < len(rows) && cols > 0 && rows[r].upTo == cols && rows[r+1].upTo > 0 && rows[r+1].col == 0
	}
	lone := make([]bool, len(rows))
	for r := range rows {
		lone[r] = rows[r].upTo > 0 && !full(r-1) && !full(r)
		if full(r) {
			joins[r] = joinedFull
		}
	}
	for a := 0; a < len(rows); a++ {
		indent := rows[a].col
		if !lone[a] || indent == 0 {
			continue
		}
		head, b, margin := a, a, 0
		for b+1 < len(rows) && lone[b+1] && rows[b+1].col == indent {
			b++
		}
		if a > 0 && lone[a-1] && rows[a-1].startsWordAt(indent) {
			head = a - 1
		}
		for r := head; r <= b; r++ {
			margin = max(margin, rows[r].upTo)
		}
		for r := head; r < b; r++ {
			if rows[r].upTo == margin {
				joins[r] = joinedIndent
			}
		}
		a = b
	}
	return joins
}

// rowSpans answers the spans of the mentions in terminal text cols wide, in
// order. A word that ends its row runs on in the word that starts the row
// below where rowJoins joins the rows, as far as wrappedMention reads them
// joined.
func (l *fileLinker) rowSpans(text string, cols int) []fileSpan {
	words, rows := rowWords(text)
	joins := rowJoins(rows, cols)
	joinOf := func(a, b rowWord) rowJoin {
		if b.row != a.row+1 || a.upTo != rows[a.row].upTo || b.col != rows[b.row].col {
			return notJoined
		}
		return joins[a.row]
	}
	var spans []fileSpan
	parts := make([]string, 0, joinRows)
	for i := 0; i < len(words); {
		parts = append(parts[:0], text[words[i].start:words[i].end])
		join := notJoined
		if i == 0 || joinOf(words[i-1], words[i]) != joinedFull {
			for k := i + 1; k < len(words) && len(parts) < joinRows && joinOf(words[k-1], words[k]) != notJoined; k++ {
				join = joinOf(words[k-1], words[k])
				parts = append(parts, text[words[k].start:words[k].end])
			}
		}
		ref, last, ok := wrappedMention(l.refs, parts, join == joinedFull)
		if !ok {
			i++
			continue
		}
		end := ref.End
		for _, w := range words[i : i+last+1] {
			spans = append(spans, fileSpan{w.start, w.start + min(end, w.end-w.start), ref})
			end -= w.end - w.start
		}
		i += last + 1
	}
	return spans
}

// joinRows is how many rows one wrapped path may run over.
const joinRows = 4

// wrappedMention reads parts, a word that ends its row and the words that
// start the rows below it, as a mention from the start of the first. It
// answers the mention and the part it ends in. Rows the terminal wrapped
// (full) read joined, and alone only when the join names no file. A program
// that wraps at an indent may as well have started a new line there, so a
// part that names a file alone stays as it reads, only parts that name
// nothing alone join, and the longest such join that names a file wins.
func wrappedMention(refs *filesystem.FileRefs, parts []string, full bool) (filesystem.FileRef, int, bool) {
	if full && len(parts) > 1 {
		if ref, ok := refs.Mention(strings.Join(parts, "")); ok {
			return ref, partAt(parts, ref.End), true
		}
		parts = parts[:1]
	}
	if ref, ok := refs.Mention(parts[0]); ok || len(parts) == 1 {
		return ref, 0, ok
	}
	n := 1
	for n < len(parts) {
		if _, ok := refs.Mention(parts[n]); ok {
			break
		}
		n++
	}
	for ; n > 1; n-- {
		if ref, ok := refs.Mention(strings.Join(parts[:n], "")); ok {
			return ref, partAt(parts[:n], ref.End), true
		}
	}
	return filesystem.FileRef{}, 0, false
}

// partAt answers the part of parts their join ends in at byte end.
func partAt(parts []string, end int) int {
	k, before := 0, len(parts[0])
	for end > before && k+1 < len(parts) {
		k++
		before += len(parts[k])
	}
	return k
}

// fileWordLink is what a word the live terminal shows links to: the file://
// address /terminal-link reads, the part of the word its mention ends in and
// how far into that part, in UTF-16 units.
type fileWordLink struct {
	URI  string `json:"uri"`
	Part int    `json:"part"`
	End  int    `json:"end"`
}

// wordLinks answers which words name a file. A word a program may have
// wrapped comes as the words of its rows, apart by line breaks.
func (l *fileLinker) wordLinks(words []string) (links map[string]fileWordLink) {
	links = map[string]fileWordLink{}
	if l == nil {
		return links
	}
	defer recoverLinks(func() { clear(links) })
	for _, word := range words {
		parts := strings.Split(word, "\n")
		if len(parts) > joinRows {
			continue
		}
		ref, part, ok := wrappedMention(l.refs, parts, false)
		if !ok {
			continue
		}
		before := len(strings.Join(parts[:part], ""))
		links[word] = fileWordLink{URI: fileLinkTarget(l.root, ref), Part: part, End: filesystem.UTF16Len(parts[part][:ref.End-before])}
	}
	return links
}

// fileLinkTarget is the file:// address of a mention, its line and column in
// the fragment, which /terminal-link reads.
func fileLinkTarget(root string, ref filesystem.FileRef) string {
	u := url.URL{Scheme: "file", Path: filepath.Join(root, filepath.FromSlash(ref.Path))}
	if ref.Line > 0 {
		u.Fragment = strconv.Itoa(ref.Line)
		if ref.Col > 0 {
			u.Fragment += ":" + strconv.Itoa(ref.Col)
		}
	}
	return u.String()
}
