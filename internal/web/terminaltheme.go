package web

import (
	"net/http"
	"regexp"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/tmux"
)

// terminalTheme holds the web terminal's current colors, posted by the attach
// page on load and on every theme change. Mirroring them onto the tmux panes
// lets TUIs detect the scheme: tmux answers a pane program's OSC 11 background
// query from the pane style. Held in memory only, the next attach re-posts it
// after a server restart.
type terminalTheme struct {
	mu sync.Mutex
	bg string
	fg string
}

var themeColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type terminalThemeForm struct {
	Background string `json:"bg" form:"bg"`
	Foreground string `json:"fg" form:"fg"`
}

func (s *Server) handleTerminalTheme(c *gin.Context) {
	var form terminalThemeForm
	if err := c.ShouldBind(&form); err != nil ||
		!themeColorPattern.MatchString(form.Background) ||
		!themeColorPattern.MatchString(form.Foreground) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid theme colors."})
		return
	}
	s.updateTerminalTheme(form.Background, form.Foreground)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// updateTerminalTheme stores the terminal colors and mirrors them onto the
// running sessions when they change. The colors ride every terminal request
// (the theme POST, the resize POST, the stream connect query), so a client
// asserts its scheme on every path, not only the dedicated POST; a reconnect
// or a resize on a device with a different scheme recovers on its own.
// Invalid or empty colors are ignored, so the resize and stream callers can
// pass through whatever the client sent without validating first.
func (s *Server) updateTerminalTheme(bg, fg string) {
	if !themeColorPattern.MatchString(bg) || !themeColorPattern.MatchString(fg) {
		return
	}
	s.termTheme.mu.Lock()
	defer s.termTheme.mu.Unlock()
	if s.termTheme.bg == bg && s.termTheme.fg == fg {
		return
	}
	s.termTheme.bg = bg
	s.termTheme.fg = fg
	s.applyTerminalThemeLocked()
}

// styleSessionPane paints a fresh session's pane with the current theme, so a
// coder started afterwards detects the right scheme on its startup query.
// Best effort: theme signaling never fails a create or resume.
func (s *Server) styleSessionPane(name string) {
	s.termTheme.mu.Lock()
	defer s.termTheme.mu.Unlock()
	if s.termTheme.bg == "" {
		return
	}
	_ = tmux.New().SetPaneStyle(name, paneStyle(s.termTheme.bg, s.termTheme.fg))
}

// applyTerminalThemeLocked restyles every running session and additionally
// sends claude the mode 2031 color scheme report (CSI ? 997 ; 1/2 n), which
// claude subscribes to, so a running claude switches its theme live. The
// report goes to claude coder sessions and to shell panes running the
// interactive claude TUI (foreground command claude on the alternate screen,
// a manual claude run inside a cockpit shell). Other programs, including
// non interactive runs like `claude -p`, never get it injected, whoever did
// not enable the mode would read the bytes as keyboard input. The whole
// change costs two tmux spawns (one list-panes, one batched apply),
// independent of the session count. A failed apply clears the stored theme,
// so the next attach post re-applies instead of being swallowed by the
// dedupe check.
func (s *Server) applyTerminalThemeLocked() {
	client := tmux.New()
	style := paneStyle(s.termTheme.bg, s.termTheme.fg)
	report := []byte("\x1b[?997;1n")
	if lightColor(s.termTheme.bg) {
		report = []byte("\x1b[?997;2n")
	}
	foregrounds := client.PaneForegrounds()
	var themes []tmux.PaneTheme
	for i := range s.coders {
		notify := s.coders[i].ID() == "claude"
		for _, r := range s.coders[i].Snapshot().Running {
			t := tmux.PaneTheme{Name: r.Identifier, Style: style}
			if notify {
				t.Report = report
			}
			themes = append(themes, t)
		}
	}
	for _, sh := range s.shells.List() {
		t := tmux.PaneTheme{Name: sh.Identifier, Style: style}
		if fg := foregrounds[sh.Identifier]; fg.Command == "claude" && fg.AltScreen {
			t.Report = report
		}
		themes = append(themes, t)
	}
	if err := client.ApplyPaneThemes(themes); err != nil {
		s.termTheme.bg = ""
		s.termTheme.fg = ""
	}
}

func paneStyle(bg, fg string) string { return "bg=" + bg + ",fg=" + fg }

// lightColor reports whether a #rrggbb color reads as light, by perceived
// luminance above half.
func lightColor(hex string) bool {
	r, _ := strconv.ParseInt(hex[1:3], 16, 32)
	g, _ := strconv.ParseInt(hex[3:5], 16, 32)
	b, _ := strconv.ParseInt(hex[5:7], 16, 32)
	return 299*r+587*g+114*b > 127500
}
