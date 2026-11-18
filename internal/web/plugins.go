package web

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/pluginhost"
	"github.com/marein/dev-cockpit/internal/web/render"
)

const (
	settingsPluginsPath   = "/settings/plugins"
	settingsPluginNewPath = settingsPluginsPath + "/new"
)

// CSRFField is the form field of the CSRF token, stripped from requests
// proxied to an external plugin.
const CSRFField = csrfFieldName

// pluginsLocalRefusal answers the local socket: a plugin runs code as the
// user and in the cockpit page, which no assistant may set up.
const pluginsLocalRefusal = "Plugins can only be changed from the browser."

// servePlugins resolves the plugin of a /plugins/<id>/ address at request
// time and serves its subtree. The page of a plugin that is not loaded
// renders through servePluginAway, anything else without a plugin that
// serves HTTP answers 404.
func (s *Server) servePlugins(c *gin.Context) {
	id := c.Param("id")
	p := s.plugins.Lookup(id)
	if p == nil && c.Request.Method == http.MethodGet && c.Request.URL.Path == pluginhost.PageURL(id) && s.servePluginAway(c, id) {
		return
	}
	if p == nil || !pluginhost.ServesHTTP(p) {
		s.handleNotFound(c)
		return
	}
	s.servePlugin(c, p)
}

// servePluginAway renders the page of a plugin that is not loaded, disabled,
// broken, removed or switched off, with the notice of its live region: a
// reload shows what the open page showed, and the region brings the plugin
// back once it runs. An unknown id gets the page under 404. False without a
// host or for an invalid id.
func (s *Server) servePluginAway(c *gin.Context, id string) bool {
	host := s.plugins.Host()
	if host == nil || !pluginhost.ValidID(id) {
		return false
	}
	title, code := id, http.StatusNotFound
	if status, ok := host.Status(id); ok {
		title, code = status.Name, http.StatusOK
		if status.PageTitle != "" {
			title = status.PageTitle
		}
	}
	c.HTML(code, "plugin_page.gohtml", render.PluginPageData{Page: s.page(c, title, pluginhost.PageTab(id)), Region: s.pluginRegion(id)})
	return true
}

// servePlugin serves one plugin's subtree: assets and starter from the
// manifest, the page in the cockpit's layout, the rest through the plugin's
// handler, for an external plugin its process. The manifest goes first, so
// the plugin's handler never shadows its assets or the starter.
func (s *Server) servePlugin(c *gin.Context, p *pluginhost.Serve) {
	base := "/plugins/" + p.ID()
	if asset, ok := s.assets.lookup(c.Request.URL.Path); ok {
		serveStaticAsset(c, asset)
		return
	}
	if page := p.Page(); page != nil && c.Request.Method == http.MethodGet && c.Request.URL.Path == pluginhost.PageURL(p.ID()) {
		data := render.PluginPageData{Page: s.page(c, page.Title, pluginhost.PageTab(p.ID()))}
		if p.Live() {
			data.Region = s.pluginRegion(p.ID())
		} else {
			data.Body = template.HTML(page.HTML)
		}
		c.HTML(http.StatusOK, "plugin_page.gohtml", data)
		return
	}
	if strings.HasPrefix(c.Request.URL.Path, base+"/assets/") || p.Handler() == nil {
		c.Status(http.StatusNotFound)
		return
	}
	http.StripPrefix(base, p.Handler()).ServeHTTP(c.Writer, c.Request)
}

// pluginRegion is the data of a plugin's page region, Status nil for a
// plugin the host does not know.
func (s *Server) pluginRegion(id string) *render.PluginRegionData {
	data := &render.PluginRegionData{Plugin: id}
	if host := s.plugins.Host(); host != nil {
		if status, ok := host.Status(id); ok {
			data.Status = &status
		}
	}
	return data
}

// handlePluginRegion renders one live region again with the template the
// page used.
func (s *Server) handlePluginRegion(c *gin.Context) {
	region := c.Param("region")
	tab := c.Query("tab")
	if len(tab) > 80 {
		tab = ""
	}
	switch {
	case region == "rail":
		c.HTML(http.StatusOK, "plugin_region_rail", render.PluginRegionData{Tab: tab})
	case region == "cockpit":
		c.HTML(http.StatusOK, "plugin_region_cockpit", render.PluginRegionData{Tab: tab})
	case strings.HasPrefix(region, "slot-") && pluginhost.SlotDefined(strings.TrimPrefix(region, "slot-")):
		c.HTML(http.StatusOK, "plugin_region_slot", render.PluginRegionData{Slot: strings.TrimPrefix(region, "slot-")})
	case strings.HasPrefix(region, "page-"):
		id := strings.TrimPrefix(region, "page-")
		p := s.plugins.Lookup(id)
		if s.plugins.Host() == nil || !pluginhost.ValidID(id) || p != nil && (!p.Live() || p.Page() == nil) {
			c.Status(http.StatusNotFound)
			return
		}
		c.HTML(http.StatusOK, "plugin_region_page", s.pluginRegion(id))
	default:
		c.Status(http.StatusNotFound)
	}
}

// PluginHooks connect the plugin host to this server: a live plugin's assets
// join or leave the manifest, and open pages learn that the set changed.
func (s *Server) PluginHooks() pluginhost.Hooks {
	return pluginhost.Hooks{
		Prepare: func(p *pluginhost.Serve) error {
			if err := s.assets.prepareLive(p); err != nil {
				return errors.New("Its assets could not be read.")
			}
			return nil
		},
		Release: s.assets.releaseLive,
		Changed: s.publishPlugins,
	}
}

// publishPlugins sends the version a live region compares before it pulls.
func (s *Server) publishPlugins() {
	s.bus.Publish(pluginsEvent(s.plugins))
}

func pluginsEvent(plugins *pluginhost.Set) eventbus.Event {
	return eventbus.Event{Type: "plugins", Data: map[string]string{"version": plugins.Version()}}
}

// handleSettingsPlugins renders Settings › Plugins. A pull (pullHeader) gets
// the rows alone, so it does not consume the flash of a post still landing.
func (s *Server) handleSettingsPlugins(c *gin.Context) {
	if c.GetHeader(pullHeader) != "" {
		data := render.SettingsPluginsData{Page: render.Page{CSRFToken: s.csrfToken(c)}}
		if host := s.plugins.Host(); host != nil {
			data.Plugins = render.PluginRows(host.List())
		}
		c.HTML(http.StatusOK, "plugin_list.gohtml", data)
		return
	}
	s.renderSettingsPlugins(c, http.StatusOK, render.SettingsPluginsData{})
}

// renderSettingsPlugins renders Settings › Plugins under code, a refusal
// without RefusedID over the whole page.
func (s *Server) renderSettingsPlugins(c *gin.Context, code int, data render.SettingsPluginsData) {
	data.Page = s.page(c, "Settings", "settings")
	if data.Refused != "" && data.RefusedID == "" {
		data.Page.Flash = render.Flash{Message: data.Refused, Level: "error"}
	}
	data.SettingsNav = s.settingsNav("plugins")
	if host := s.plugins.Host(); host != nil {
		data.Folder = host.Dir()
		data.Plugins = render.PluginRows(host.List())
	}
	c.HTML(code, "settings_plugins.gohtml", data)
}

// handleSettingsPluginLog renders the last lines a plugin's process wrote.
func (s *Server) handleSettingsPluginLog(c *gin.Context) {
	host := s.plugins.Host()
	if host == nil {
		s.handleNotFound(c)
		return
	}
	lines, ok := host.Log(c.Param("id"))
	status, _ := host.Status(c.Param("id"))
	if !ok {
		s.handleNotFound(c)
		return
	}
	c.HTML(http.StatusOK, "settings_plugin_log.gohtml", render.PluginLogData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("plugins"),
		Name:        status.Name,
		ID:          status.ID,
		Lines:       lines,
	})
}

// pluginActions maps each row action to the flash of its success.
var pluginActions = map[string]string{
	"enable":   "Plugin enabled.",
	"disable":  "Plugin disabled.",
	"restart":  "Plugin restarted.",
	"remove":   "Plugin removed.",
	"update":   "Plugin updated.",
	"auto-on":  "Automatic updates on.",
	"auto-off": "Automatic updates off.",
}

// pluginUpdateTimeout bounds an update or a check, downloads included. It
// runs on its own context, so a request that goes away midway leaves no half
// update.
const pluginUpdateTimeout = 6 * time.Minute

// handleSettingsPluginsSave runs one action of the page. A refused install
// renders the page again with the path kept, a replace of another source's
// plugin asks first, a refused row action shows at its row.
func (s *Server) handleSettingsPluginsSave(c *gin.Context) {
	if s.localCall(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": pluginsLocalRefusal})
		return
	}
	host := s.plugins.Host()
	if host == nil {
		s.handleNotFound(c)
		return
	}
	action := c.PostForm("form")
	if action == "check" {
		ctx, cancel := context.WithTimeout(context.Background(), pluginUpdateTimeout)
		defer cancel()
		host.CheckUpdates(ctx)
		s.pluginsDone(c, "", "Updates checked.")
		return
	}
	if action == "install" {
		path := c.PostForm("path")
		id, replaced, err := host.Install(path, c.PostForm("replace") != "")
		var ask *pluginhost.ReplaceError
		if errors.As(err, &ask) {
			s.renderSettingsPlugins(c, http.StatusConflict, render.SettingsPluginsData{Path: path, Replace: ask.Error()})
			return
		}
		if err != nil {
			message := "The plugin could not be installed."
			if pluginhost.IsInstallError(err) {
				message = err.Error()
			}
			s.renderSettingsPlugins(c, http.StatusUnprocessableEntity, render.SettingsPluginsData{Path: path, PathError: message})
			return
		}
		done := "Plugin installed."
		if replaced {
			done = "Plugin updated."
		}
		s.pluginsDone(c, id, done)
		return
	}
	done, ok := pluginActions[action]
	if !ok {
		s.renderSettingsPlugins(c, http.StatusBadRequest, render.SettingsPluginsData{Refused: "Unknown action."})
		return
	}
	id := c.PostForm("id")
	var err error
	switch action {
	case "enable", "disable":
		err = host.SetEnabled(id, action == "enable")
	case "restart":
		err = host.Restart(id)
	case "remove":
		err = host.Remove(id)
	case "update":
		ctx, cancel := context.WithTimeout(context.Background(), pluginUpdateTimeout)
		defer cancel()
		err = host.Update(ctx, id, c.PostForm("version"))
	case "auto-on", "auto-off":
		err = host.SetAuto(id, action == "auto-on")
	}
	switch {
	case errors.Is(err, pluginhost.ErrUnknownPlugin):
		s.renderSettingsPlugins(c, http.StatusNotFound, render.SettingsPluginsData{Refused: err.Error()})
	case pluginhost.IsInstallError(err):
		s.renderSettingsPlugins(c, http.StatusConflict, render.SettingsPluginsData{Refused: err.Error(), RefusedID: id})
	case err != nil:
		s.renderSettingsPlugins(c, http.StatusInternalServerError, render.SettingsPluginsData{Refused: "The plugin could not be changed, writing the plugins folder failed."})
	case action == "remove":
		s.pluginsDone(c, "", done)
	default:
		s.pluginsDone(c, id, done)
	}
}

// pluginsDone flashes message and lands on the plugin's row, a dialog
// through the location it answers.
func (s *Server) pluginsDone(c *gin.Context, id, message string) {
	location := settingsPluginsPath
	if id != "" {
		location += "#plugin-" + id
	}
	sess := ginsessions.Default(c)
	putFlash(sess, message, "success")
	if inFormModal(c) {
		if err := sess.Save(); err != nil {
			s.renderError(c, http.StatusInternalServerError, "Something went wrong", saveSessionErrorMessage)
			return
		}
		c.JSON(http.StatusOK, gin.H{"location": location})
		return
	}
	s.finishRedirect(c, sess, location)
}

// handleSettingsPluginNew renders the install from a repository.
func (s *Server) handleSettingsPluginNew(c *gin.Context) {
	if s.plugins.Host() == nil {
		s.handleNotFound(c)
		return
	}
	s.renderPluginRepository(c, http.StatusOK, render.PluginRepositoryData{})
}

func (s *Server) renderPluginRepository(c *gin.Context, code int, data render.PluginRepositoryData) {
	data.Page = s.page(c, "Settings", "settings")
	data.SettingsNav = s.settingsNav("plugins")
	data.Modal = inFormModal(c)
	if data.Modal {
		c.HTML(code, "plugin_repository.gohtml", data)
		return
	}
	c.HTML(code, "settings_plugin_new.gohtml", data)
}

// handleSettingsPluginNewSave looks up a repository and offers its newest
// release, or installs a confirmed offer. An install that would replace a
// plugin the offer did not name asks again.
func (s *Server) handleSettingsPluginNewSave(c *gin.Context) {
	if s.localCall(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": pluginsLocalRefusal})
		return
	}
	host := s.plugins.Host()
	if host == nil {
		s.handleNotFound(c)
		return
	}
	raw, forge := c.PostForm("url"), c.PostForm("forge")
	if c.PostForm("form") != "install" {
		offer, err := host.Look(c.Request.Context(), raw, forge)
		if err != nil {
			s.refusePluginRepository(c, raw, forge, err, false)
			return
		}
		s.renderPluginRepository(c, http.StatusOK, render.PluginRepositoryData{URL: raw, Forge: offer.Repository.Forge, Offer: offer})
		return
	}
	id, err := host.Adopt(c.PostForm("token"), c.PostForm("auto") != "", c.PostForm("replaces"))
	var ask *pluginhost.ReplaceError
	switch {
	case errors.As(err, &ask):
		s.renderPluginRepository(c, http.StatusConflict, render.PluginRepositoryData{URL: raw, Forge: forge, Offer: ask.Offer, Ask: ask.Error()})
	case err != nil:
		s.refusePluginRepository(c, raw, forge, err, true)
	default:
		s.pluginsDone(c, id, "Plugin installed.")
	}
}

// refusePluginRepository answers a refused look or install. A refused
// install spent its offer, so the dialog gets the address form again with
// the reason above it instead of a button that can only fail.
func (s *Server) refusePluginRepository(c *gin.Context, raw, forge string, err error, spent bool) {
	message := "The plugin could not be installed."
	if pluginhost.IsInstallError(err) {
		message = err.Error()
	}
	if inFormModal(c) && !spent || !inFormModal(c) && wantsJSON(c.Request) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": message})
		return
	}
	data := render.PluginRepositoryData{URL: raw, Forge: forge, URLError: message}
	if spent {
		data.URLError, data.Refused = "", message
	}
	s.renderPluginRepository(c, http.StatusUnprocessableEntity, data)
}
