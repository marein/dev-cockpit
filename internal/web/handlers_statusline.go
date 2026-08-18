package web

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/coder/claude/statusline"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// statusLineSection is the coder section this page is, the same word the
// sidebar keeps when somebody switches the coder.
const statusLineSection = "statusline"

// coderHasStatusLine reports whether a coder carries the status line section.
// It is claude's own surface: nothing in the other coder's CLI takes a command
// that renders a line, so the tab is not offered there and neither are its
// routes.
func coderHasStatusLine(co *coder.Manager) bool { return co.ID() == statusline.CoderID }

// statusLineConfig answers what the page shows: the mode and the list. Never
// answered, or a value nothing can read, is the default configuration.
func (s *Server) statusLineConfig() statusline.Config {
	config, err := statusline.Decode(s.settings.Get(statusline.SettingKey))
	if err != nil {
		return statusline.DefaultConfig()
	}
	config.Entries = statusline.Normalize(config.Entries)
	return config
}

// handleStatusLine renders the page. ?preset= puts that preset's line into the
// list without saving it, so the preview shows what it would be and Save is
// the one step that takes it; the mode stays the saved one.
func (s *Server) handleStatusLine(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		config := s.statusLineConfig()
		entries := config.Entries
		if preset, ok := statusline.PresetByID(c.Query("preset")); ok {
			entries = preset.Entries()
		}
		base := s.coderBase(co) + "/" + statusLineSection
		c.HTML(http.StatusOK, "statusline_form.gohtml", render.StatusLineData{
			Page:        s.page(c, s.coderTitle(co, "Status line"), "settings"),
			SettingsNav: s.coderSettingsNav("coder", co, statusLineSection),
			Base:        s.coderBase(co),
			Modes:       render.StatusLineModes(config.Mode),
			State:       render.NewStatusLineState(base, config.Entries, entries, nil),
			Rows:        render.StatusLineRows(entries),
			Groups:      render.StatusLineGroups(),
			Colors:      statusline.Colors,
			ValuesJSON:  render.StatusLineValuesJSON(),
			ColorsJSON:  render.StatusLineColorsJSON(),
		})
	}
}

// handleStatusLineState answers what the page says about the list the form
// carries right now: the picker, and why the list cannot be saved if it
// cannot. It reads the rows with the save's own reader, so an edit is compared
// exactly the way the saved line is after a reload, and a list the save would
// refuse says so before anything is posted. Nothing is stored.
func (s *Server) handleStatusLineState(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		list, err := statusLineEntriesFromForm(c)
		base := s.coderBase(co) + "/" + statusLineSection
		c.HTML(http.StatusOK, "statusline_state.gohtml", render.NewStatusLineState(base, s.statusLineConfig().Entries, list, err))
	}
}

// statusLineLocalRefusal answers a save over the local socket. A command entry
// runs on every redraw of every claude the cockpit starts, so configuring the
// line is the user's act, like the compose commands.
const statusLineLocalRefusal = "The status line is the user's to configure, in the browser. Ask the user."

// handleStatusLineSave takes the whole page in one form: the mode and the
// list. The line is written in every mode, before the answer is stored, so a
// save that could not write leaves the setting as it was instead of pointing
// claude at a line that is not there. The mode is read when a session starts,
// so it reaches the sessions started afterwards; the line is read at every
// redraw, so it reaches every session that already shows it. Off removes
// nothing: a session started with the line keeps drawing it until it ends.
func (s *Server) handleStatusLineSave(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.localCall(c) {
			c.JSON(http.StatusForbidden, gin.H{"error": statusLineLocalRefusal})
			return
		}
		target := s.coderBase(co) + "/" + statusLineSection
		entries, err := statusLineEntriesFromForm(c)
		if err != nil {
			s.redirectWithAnchoredFlash(c, target, "settings-statusline", "", err.Error())
			return
		}
		config := statusline.Config{Mode: statusline.ParseMode(c.PostForm("mode")), Entries: entries}
		if err := statusline.Apply(s.cfg.StateDir, config.Entries); err != nil {
			s.redirectWithAnchoredFlash(c, target, "settings-statusline", "", "Could not write the status line: "+err.Error())
			return
		}
		s.settings.Set(statusline.SettingKey, statusline.Encode(config))
		s.redirectWithAnchoredFlash(c, target, "settings-statusline", "Settings saved.", "")
	}
}

// statusLineEntriesFromForm reads the list off the form, in the order the rows
// stand in it, which is the order of the line. Every row posts every field,
// whichever kind it is, so the columns stay aligned after a drag; the bounds
// are the one thing a row has a variable number of, and they travel as one
// flat list plus the count each row carries, so a row takes exactly its own.
// Every count and length is bounded, so a hand built request is refused
// instead of stored, and the one message quoting what was sent quotes no more
// than a bound may hold, which keeps it inside the session cookie the flash
// rides on.
func statusLineEntriesFromForm(c *gin.Context) ([]statusline.Entry, error) {
	kinds := c.PostFormArray("entry_kind")
	if len(kinds) > statusline.MaxEntries {
		return nil, fmt.Errorf("At most %d entries.", statusline.MaxEntries)
	}
	values := c.PostFormArray("entry_value")
	labels := c.PostFormArray("entry_label")
	labelColors := c.PostFormArray("entry_label_color")
	colors := c.PostFormArray("entry_color")
	texts := c.PostFormArray("entry_text")
	counts := c.PostFormArray("entry_thresholds")
	boundValues := c.PostFormArray("threshold_at")
	boundColors := c.PostFormArray("threshold_color")

	taken := 0
	out := []statusline.Entry{}
	for i := range kinds {
		entry := statusline.Entry{
			Kind:       statusline.Kind(strings.TrimSpace(at(kinds, i))),
			Value:      strings.TrimSpace(at(values, i)),
			Label:      strings.TrimSpace(at(labels, i)),
			LabelColor: strings.TrimSpace(at(labelColors, i)),
			Color:      strings.TrimSpace(at(colors, i)),
			Text:       strings.TrimSpace(at(texts, i)),
		}
		if utf8.RuneCountInString(entry.Label) > statusline.MaxLabelRunes {
			return nil, statusLineRowError{i, fmt.Errorf("At most %d characters per label.", statusline.MaxLabelRunes)}
		}
		if limit := entry.MaxText(); utf8.RuneCountInString(entry.Text) > limit {
			return nil, statusLineRowError{i, fmt.Errorf("At most %d characters per text.", limit)}
		}
		count, _ := strconv.Atoi(strings.TrimSpace(at(counts, i)))
		if count > statusline.MaxBounds {
			return nil, statusLineRowError{i, fmt.Errorf("At most %d bounds per entry.", statusline.MaxBounds)}
		}
		for j := 0; j < count && taken < len(boundValues); j++ {
			raw := strings.TrimSpace(boundValues[taken])
			color := at(boundColors, taken)
			taken++
			// A bound somebody added and left empty is no bound.
			if raw == "" {
				continue
			}
			if utf8.RuneCountInString(raw) > statusline.MaxBoundLength {
				return nil, statusLineRowError{i, fmt.Errorf("At most %d characters per bound.", statusline.MaxBoundLength)}
			}
			// ParseFloat also takes NaN and the infinities, which no bound can
			// be compared against and which json.Marshal refuses outright: the
			// whole stored answer would come back empty, list and mode with
			// it.
			bound, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsNaN(bound) || math.IsInf(bound, 0) {
				return nil, statusLineRowError{i, fmt.Errorf("Bound %q is no number.", raw)}
			}
			entry.Thresholds = append(entry.Thresholds, statusline.Threshold{At: bound, Color: color})
		}
		out = append(out, entry)
	}
	return statusline.Normalize(out), nil
}

// statusLineRowError is a refusal one row of the form caused, counted in the
// order the rows stand, so the page says it at that row.
type statusLineRowError struct {
	row int
	error
}

func (e statusLineRowError) EntryRow() int { return e.row }
