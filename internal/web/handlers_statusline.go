package web

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/coder/claude/statusline"
	"github.com/local/dev-cockpit/internal/web/render"
)

// statusLineSection is the coder section this page is, the same word the
// sidebar keeps when somebody switches the coder.
const statusLineSection = "statusline"

// coderHasStatusLine reports whether a coder carries the status line section.
// It is claude's own surface: nothing in the other coder's CLI takes a command
// that renders a line, so the tab is not offered there and neither are its
// routes.
func coderHasStatusLine(co *coder.Manager) bool { return co.ID() == statusline.CoderID }

// statusLineConfig answers what the page shows: the switch and the list. Never
// answered, or a value nothing can read, is the default configuration, which
// is off with the default line offered on the form, so an install that never
// opened this page keeps whatever status line claude's own settings ask for.
func (s *Server) statusLineConfig() statusline.Config {
	raw, set := s.settings.Lookup(statusline.SettingKey)
	if !set {
		return statusline.DefaultConfig()
	}
	config, err := statusline.Decode(raw)
	if err != nil {
		return statusline.DefaultConfig()
	}
	config.Entries = statusline.Normalize(config.Entries)
	return config
}

func (s *Server) handleStatusLine(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		config := s.statusLineConfig()
		c.HTML(http.StatusOK, "statusline_form.gohtml", render.StatusLineData{
			Page:        s.page(c, s.coderTitle(co, "Status line"), "settings"),
			SettingsNav: s.coderSettingsNav("coder", co, statusLineSection),
			Base:        s.coderBase(co),
			Enabled:     config.Enabled,
			Rows:        render.StatusLineRows(config.Entries),
			Groups:      render.StatusLineGroups(),
			Colors:      statusline.Colors,
			ValuesJSON:  render.StatusLineValuesJSON(),
			ColorsJSON:  render.StatusLineColorsJSON(),
		})
	}
}

// handleStatusLineSave takes the whole page in one form: the switch and the
// list. The script is written before the answer is stored, so a save that
// could not write leaves the setting as it was instead of pointing claude at a
// file that is not there. Off writes no script and keeps the list, which is
// what makes switching back on cost nothing.
func (s *Server) handleStatusLineSave(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		target := s.coderBase(co) + "/" + statusLineSection
		entries, err := statusLineEntriesFromForm(c)
		if err != nil {
			s.redirectWithAnchoredFlash(c, target, "settings-statusline", "", err.Error())
			return
		}
		config := statusline.Config{Enabled: c.PostForm("enabled") == "on", Entries: entries}
		if config.Enabled {
			err = statusline.Apply(s.cfg.StateDir, config.Entries)
		} else {
			err = statusline.Clear(s.cfg.StateDir)
		}
		if err != nil {
			s.redirectWithAnchoredFlash(c, target, "settings-statusline", "", "The status line script could not be written: "+err.Error())
			return
		}
		s.settings.Set(statusline.SettingKey, statusline.Encode(config))
		message := "Saved. The cockpit sets no status line, so claude's own setting applies."
		if config.Enabled {
			message = "Saved. Coders started from now on carry this line."
		}
		s.redirectWithAnchoredFlash(c, target, "settings-statusline", message, "")
	}
}

// statusLineEntriesFromForm reads the list off the form, in the order the rows
// stand in it, which is the order of the line. Every row posts every field,
// whichever kind it is, so the columns stay aligned after a drag; the bounds
// are the one thing a row has a variable number of, and they travel as one
// flat list plus the count each row carries, so a row takes exactly its own.
func statusLineEntriesFromForm(c *gin.Context) ([]statusline.Entry, error) {
	kinds := c.PostFormArray("entry_kind")
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
		count, _ := strconv.Atoi(strings.TrimSpace(at(counts, i)))
		for j := 0; j < count && taken < len(boundValues); j++ {
			raw := strings.TrimSpace(boundValues[taken])
			color := at(boundColors, taken)
			taken++
			// A bound somebody added and left empty is no bound.
			if raw == "" {
				continue
			}
			// ParseFloat also takes NaN and the infinities, which no bound can
			// be compared against and which json.Marshal refuses outright: the
			// whole stored answer would come back empty, list and switch with
			// it.
			bound, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsNaN(bound) || math.IsInf(bound, 0) {
				return nil, fmt.Errorf("A color bound is a number, %q is not.", raw)
			}
			entry.Thresholds = append(entry.Thresholds, statusline.Threshold{At: bound, Color: color})
		}
		out = append(out, entry)
	}
	return statusline.Normalize(out), nil
}
