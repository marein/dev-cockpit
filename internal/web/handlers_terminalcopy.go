package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// What a terminal has said, as text, for the copy sheet. The sheet is the one
// way to reach text that is no longer on the visible screen: a canvas holds no
// selectable text, and making the pane taller than the window to get more of it
// onto the screen is what the extra rows setting used to do, at the price of
// every full screen program laying itself out for a size nobody can see.
//
// The read goes through capture-pane, which never attaches, so the pane keeps
// the size the client that owns it gave it. Reading through the terminal hub
// would resize the pane to the reader's window instead.
//
// A pane on the alternate screen has no scrollback at all, that is what the
// alternate screen is, so there the answer is the screen itself. That is not a
// shortcoming of this route but of the place the text would have to come from,
// and for a coder the honest source is its own record, which is a separate
// capability, see the copy sheet.
const (
	copyDefaultLines = 500
	copyMaxLines     = 50000
)

type terminalCopy struct {
	Text string `json:"text"`
	// Lines is what the sheet says it is showing, so a person can tell at a
	// glance whether the whole conversation is in front of them.
	Lines int    `json:"lines"`
	Kind  string `json:"kind"`
	// Screen says the text is the terminal picture and not a record, which is
	// a coder's input line and its own layout rather than what was said.
	Screen bool `json:"screen"`
	// Dropped is how many messages the bounds left out at the top, so the
	// sheet can offer to fetch them.
	Dropped int `json:"dropped"`
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
	messages := coder.TranscriptPage
	if raw := strings.TrimSpace(c.Query("messages")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			messages = n
		}
	}
	// A coder has two honest sources, and which one is asked for is the
	// reader's choice: what was said, which is its record, and what stands on
	// the screen right now, which is the only place a prompt somebody is still
	// typing exists. Everything else has one source and ignores this.
	text, screen, dropped := "", true, 0
	if ref.Kind == "coder" && strings.TrimSpace(c.Query("source")) != "screen" {
		if recorded, ok := s.coderTranscript(c.Param("id"), messages); ok {
			text, screen, dropped = recorded.Text, recorded.Screen, recorded.Dropped
		}
	}
	if screen {
		captured, err := tmux.New().CapturePane(ref.TmuxSession, lines)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": userFacingError(c, err)})
			return
		}
		text = captured
	}
	count := 0
	if text != "" {
		count = strings.Count(text, "\n") + 1
	}
	c.JSON(http.StatusOK, terminalCopy{Text: text, Lines: count, Kind: ref.Kind, Screen: screen, Dropped: dropped})
}

// coderTranscript asks the coder that owns the session for its recorded
// conversation. A coder that keeps no record answers with the screen, which is
// the same fallback and the same wording the activity reading uses.
func (s *Server) coderTranscript(id string, messages int) (coder.Transcript, bool) {
	for _, m := range s.coders {
		if _, err := m.ResolveRunning(id); err != nil {
			continue
		}
		recorded, err := m.Transcript(id, messages, coder.TranscriptCap)
		if err != nil {
			return coder.Transcript{}, false
		}
		return recorded, true
	}
	return coder.Transcript{}, false
}
