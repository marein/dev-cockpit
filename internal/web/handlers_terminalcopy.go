package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// What a terminal shows, as text, for the text face of the copy view: a
// shell's history in as many lines as the reader asked for, or a coder's
// screen. A canvas holds no selectable text, so this is the one way to reach
// text that is no longer on the visible screen.
//
// The read goes through capture-pane, which never attaches, so the pane keeps
// the size the client that owns it gave it. Reading through the terminal hub
// would resize the pane to the reader's window instead.
//
// A pane on the alternate screen has no scrollback at all, that is what the
// alternate screen is, so there the answer is the screen itself. A coder runs
// on one, which is why its conversation is read from its own record on a
// route of its own, see handleCoderConversation.
const (
	copyDefaultLines = 500
	copyMaxLines     = 50000
)

type terminalCopy struct {
	Text  string     `json:"text"`
	Links []fileLink `json:"links"`
	// Kind and Screen are read by a tab still on the page from before the
	// conversation face, which chose its wording by them. Screen is always
	// true, the answer is always the terminal picture.
	// TODO(v2.0.0): remove Kind and Screen.
	Kind   string `json:"kind"`
	Screen bool   `json:"screen"`
}

func (s *Server) handleTerminalCopy(c *gin.Context) {
	ref, ok := s.terminalSessions()[c.Param("id")]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "This terminal is not running."})
		return
	}
	lines := copyDefaultLines
	if raw := strings.TrimSpace(c.Query("lines")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			lines = n
		}
	}
	if lines > copyMaxLines {
		lines = copyMaxLines
	}
	text, cols, err := tmux.New().CapturePaneWidth(ref.TmuxSession, lines)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, terminalCopy{Text: text, Links: newestFileLinks(text, cols, s.fileFinder(ref.CWD)), Kind: ref.Kind, Screen: true})
}

// newestFileLinks links the lines at the end of text, where the view opens,
// as far back as copyLinkBudget reaches, the budget of a conversation page.
func newestFileLinks(text string, cols int, find func(string) []fileLink) []fileLink {
	from := 0
	if cut := len(text) - copyLinkBudget; cut > 0 {
		from = len(text)
		if i := strings.IndexByte(text[cut:], '\n'); i >= 0 {
			from = cut + i + 1
		}
	}
	continues := func(line, next string) bool { return continuesLine(line, next, cols) }
	links := wrappedFileLinks(text[from:], continues, find)
	shift := filesystem.UTF16Len(text[:from])
	for i := range links {
		links[i].Start += shift
		links[i].End += shift
	}
	return links
}
