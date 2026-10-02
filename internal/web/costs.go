package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/cost"
	"github.com/marein/dev-cockpit/internal/cost/price"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// SetCosts hands the server the cost books and the price list they price
// with. A server without them shows no cost item and answers the cost page
// as empty.
func (s *Server) SetCosts(costs *cost.Service, prices *price.Book) {
	s.costs, s.prices = costs, prices
}

// CostZone is the zone the cost books cut days and months in: where the
// user said they sit, else this server's own.
func CostZone(store *settings.Store) *time.Location {
	name, _ := AssistantTimezone(store)
	if loc, err := assistant.LoadZone(name); err == nil {
		return loc
	}
	return time.Local
}

func (s *Server) costZone() *time.Location { return CostZone(s.settings) }

// costQuery reads the books for a state of the page: the report and, under
// a filter, the same span without it.
func (s *Server) costQuery(c *gin.Context) (render.CostState, cost.Report, cost.Report) {
	now := time.Now().In(s.costZone())
	state := render.ParseCostState(c.Request.URL.Query(), now)
	if s.costs == nil {
		r := cost.Report{Now: now}
		return state, r, r
	}
	span := state.Span(now)
	r := s.costs.Query(now.Location(), span)
	all := r
	if span.Filter != (cost.Filter{}) {
		span.Filter = cost.Filter{}
		all = s.costs.Query(now.Location(), span)
	}
	return state, r, all
}

func (s *Server) costBoard(c *gin.Context) render.CostBoard {
	state, r, all := s.costQuery(c)
	return render.NewCostBoard(r, all, state, s.priceStatus())
}

func (s *Server) costStatus() render.CostStatus {
	now := time.Now().In(s.costZone())
	if s.costs == nil {
		return render.NewCostStatus(cost.Report{Now: now})
	}
	return render.NewCostStatus(s.costs.Query(now.Location(), cost.Span{From: cost.DayStart(now)}))
}

// handleCosts renders the cost page, and answers a local caller the numbers
// as JSON.
func (s *Server) handleCosts(c *gin.Context) {
	if wantsJSON(c.Request) {
		state, report, _ := s.costQuery(c)
		projects := make([]gin.H, 0, len(report.Projects))
		for _, p := range report.Projects {
			if !p.Assistants {
				projects = append(projects, gin.H{"project": p.Project, "usd": p.USD})
				continue
			}
			names := make([]gin.H, 0, len(report.Assistants))
			for _, a := range report.Assistants {
				names = append(names, gin.H{"name": a.Name, "usd": a.USD})
			}
			projects = append(projects, gin.H{"assistants": names, "usd": p.USD})
		}
		c.JSON(http.StatusOK, gin.H{
			"note":     render.CostNote,
			"today":    report.Today,
			"week":     report.Week,
			"month":    report.Month,
			"burn":     report.Burn,
			"range":    state.Range,
			"from":     report.Since,
			"to":       report.Until,
			"total":    report.Total,
			"projects": projects,
			"unpriced": report.Unpriced,
		})
		return
	}
	c.HTML(http.StatusOK, "costs.gohtml", render.CostsData{
		Page:  s.page(c, "Costs", "costs"),
		Board: s.costBoard(c),
	})
}

func (s *Server) handleCostsBoard(c *gin.Context) {
	c.HTML(http.StatusOK, "costs_board.gohtml", s.costBoard(c))
}

func (s *Server) handleCostStatus(c *gin.Context) {
	c.HTML(http.StatusOK, "cost_status.gohtml", render.Page{Cost: s.costStatus()})
}

func (s *Server) priceStatus() string {
	if s.prices == nil {
		return ""
	}
	return render.CostPrices(s.prices.Status(), s.costZone())
}

// handleSettingsCosts shows the refresh switch and where the prices stand.
func (s *Server) handleSettingsCosts(c *gin.Context) {
	status := price.Status{Enabled: s.settings.Get(price.RefreshSettingKey) != "off"}
	if s.prices != nil {
		status = s.prices.Status()
	}
	var rates []cost.ModelRate
	if s.costs != nil {
		rates = s.costs.ModelRates()
	}
	data := render.NewSettingsCosts(status, rates, cost.Retention(s.settings.Get(cost.RetentionSettingKey)), time.Now().In(s.costZone()))
	data.Page = s.page(c, "Settings", "settings")
	data.SettingsNav = s.settingsNav("costs")
	c.HTML(http.StatusOK, "settings_costs.gohtml", data)
}

// handleSettingsCostsSave stores the switch as "on" or "off", so a later
// default flip cannot turn a refresh back on that somebody switched off.
// The months are stored as given, refused below the minimum.
func (s *Server) handleSettingsCostsSave(c *gin.Context) {
	months, err := strconv.Atoi(strings.TrimSpace(c.PostForm("retention")))
	if err != nil || months < cost.MinRetention {
		s.redirectWithAnchoredFlash(c, "/settings/costs", "settings-costs", "", fmt.Sprintf("Keep at least %d months.", cost.MinRetention))
		return
	}
	value := "off"
	if c.PostForm("price_refresh") == "on" {
		value = "on"
	}
	s.settings.Set(price.RefreshSettingKey, value)
	s.settings.Set(cost.RetentionSettingKey, strconv.Itoa(months))
	s.redirectWithAnchoredFlash(c, "/settings/costs", "settings-costs", "Settings saved.", "")
}
