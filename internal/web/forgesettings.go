package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// Settings › Forges is the list of hosts a project's origin binds to, built
// in and added. Adding a host, changing an added one and a host's token are
// forms of their own, each a dialog over the list and a page without
// scripts: Add host at settingsForgesNewPath, an added host's Edit at its id
// below the list, where a row's Test posts too and lands its outcome on the
// row, and the token at forgeTokenPath, which an add leads on to and a row
// opens. Its save tests the token and lands on the row the same way.

const (
	settingsForgesPath    = "/settings/forges"
	settingsForgesNewPath = settingsForgesPath + "/new"
)

// forgeLocalRefusal turns a change of the hosts over the local socket away:
// they decide which server a project's origin is taken for, which no
// assistant decides on its own.
const forgeLocalRefusal = "Forge hosts are set up on the settings page, not from a command."

// forgeBuiltinRefusal turns the Edit of a built in host away, whose kind,
// address and alias hosts are fixed.
const forgeBuiltinRefusal = "A built in host has nothing to edit, its token is set from its row."

// forgeHostPath is where a host's Edit stands.
func forgeHostPath(id string) string {
	return settingsForgesPath + "/" + url.PathEscape(id)
}

// forgeTokenPath is where a host's token is set, replaced and removed.
func forgeTokenPath(id string) string {
	return forgeHostPath(id) + "/token"
}

// forgeHostAnchor is the row of a host on the list, where its test lands.
func forgeHostAnchor(id string) string {
	return "forge-host-" + id
}

// forgeInput reads the host form.
func forgeInput(c *gin.Context) forge.Input {
	return forge.Input{
		Kind:     c.PostForm("kind"),
		WebURL:   c.PostForm("web_url"),
		Aliases:  forgeAliases(c.PostForm("aliases")),
		CABundle: c.PostForm("ca_bundle"),
	}
}

// forgeConnected words a test that went through.
func forgeConnected(conn forge.Connection) string {
	who := conn.Account
	if conn.Name != "" && conn.Name != conn.Account {
		who += " (" + conn.Name + ")"
	}
	return fmt.Sprintf("Connected to %s as %s.", conn.Server, who)
}

// forgeAliases reads the alias hosts field, names apart by spaces or commas.
func forgeAliases(field string) []string {
	return strings.FieldsFunc(field, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
}

func (s *Server) forgeRefuseLocal(c *gin.Context, refusal string) bool {
	if !s.localCall(c) {
		return false
	}
	c.JSON(http.StatusForbidden, gin.H{"error": refusal})
	return true
}

func (s *Server) handleSettingsForges(c *gin.Context) {
	s.renderSettingsForges(c, http.StatusOK, "", "")
}

// renderSettingsForges renders the list of hosts with code, a refusal of a
// row's Test on the row anchor names, or above the list where anchor is "".
func (s *Server) renderSettingsForges(c *gin.Context, code int, anchor, refusal string) {
	page := s.page(c, "Settings", "settings")
	hosts, err := s.forge.Hosts()
	if err != nil {
		page.Flash = render.Flash{Message: "The added hosts could not be read.", Level: "error"}
	}
	hints, err := s.forge.TokenHints()
	if err != nil {
		page.Flash = render.Flash{Message: err.Error(), Level: "error"}
	}
	if refusal != "" {
		page.Flash, page.FlashProject = render.Flash{Message: refusal, Level: "error"}, anchor
	}
	tested := s.forge.Tested()
	rows := make([]render.ForgeHostRow, 0, len(hosts))
	for _, h := range hosts {
		hint, token := hints[h.ID]
		row := render.ForgeHostRow{
			Anchor:    forgeHostAnchor(h.ID),
			Kind:      h.Kind.Label,
			Icon:      h.Kind.Icon,
			WebURL:    h.WebURL,
			Aliases:   h.Aliases,
			Builtin:   h.Builtin(),
			CABundle:  h.CABundle != "",
			Token:     token,
			TokenHint: hint,
			TokenURL:  forgeTokenPath(h.ID),
			EditURL:   forgeHostPath(h.ID),
		}
		if conn, ok := tested[h.ID]; ok && token {
			row.Tested = forgeConnected(conn)
		}
		rows = append(rows, row)
	}
	c.HTML(code, "settings_forges.gohtml", render.SettingsForgesData{
		Page:        page,
		SettingsNav: s.settingsNav("forges"),
		Hosts:       rows,
	})
}

func (s *Server) handleForgeHostNew(c *gin.Context) {
	s.renderForgeHost(c, forge.Host{}, nil, http.StatusOK, "")
}

func (s *Server) handleForgeHostEdit(c *gin.Context) {
	if h, ok := s.editableForgeHost(c); ok {
		s.renderForgeHost(c, h, nil, http.StatusOK, "")
	}
}

// editableForgeHost answers the added host the request names, or answers the
// refusal of a host that no longer stands or is built in: the dialog's save
// hears it as JSON, everything else gets the list under it.
func (s *Server) editableForgeHost(c *gin.Context) (forge.Host, bool) {
	h, err := s.forge.Host(c.Param("id"))
	refusal := forgeBuiltinRefusal
	switch {
	case err != nil:
		refusal = err.Error()
	case !h.Builtin():
		return h, true
	}
	if c.Request.Method == http.MethodPost && (inFormModal(c) || wantsJSON(c.Request)) {
		c.JSON(http.StatusNotFound, gin.H{"error": refusal})
	} else {
		s.renderSettingsForges(c, http.StatusNotFound, "", refusal)
	}
	return forge.Host{}, false
}

// forgeHostData is the form of h, a new host while it has no id, as it
// stands.
func (s *Server) forgeHostData(c *gin.Context, h forge.Host) render.ForgeHostFormData {
	data := render.ForgeHostFormData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("forges"),
		Modal:       inFormModal(c),
		Action:      settingsForgesNewPath,
		Title:       "Add host",
		WebURLMax:   forge.MaxWebURLLength,
		AliasesMax:  forge.MaxAliases,
		CABundleMax: forge.MaxCABundleBytes,
	}
	if h.ID != "" {
		data.ID = h.ID
		data.Action = forgeHostPath(h.ID)
		data.Title = h.WebURL
		data.WebURL = h.WebURL
		data.Aliases = strings.Join(h.Aliases, " ")
		data.CABundle = h.CABundle
	}
	return data
}

// renderForgeHost renders the form of h, a new host while it has no id, with
// code: filled with what was sent and under the refusal where a save was
// refused.
func (s *Server) renderForgeHost(c *gin.Context, h forge.Host, sent *forge.Input, code int, refusal string) {
	data := s.forgeHostData(c, h)
	kind := h.Kind.ID
	if sent != nil {
		kind, data.WebURL, data.Aliases, data.CABundle = sent.Kind, sent.WebURL, strings.Join(sent.Aliases, " "), sent.CABundle
	}
	for _, k := range s.forge.Kinds() {
		data.Kinds = append(data.Kinds, render.ForgeChoice{ID: k.ID, Label: k.Label, Picked: k.ID == kind})
	}
	s.htmlForgeHost(c, code, data, refusal)
}

// renderForgeToken renders the token view of h with code, under the refusal
// where its save or its test was refused. The token never comes back.
func (s *Server) renderForgeToken(c *gin.Context, h forge.Host, code int, refusal string) {
	data := s.forgeHostData(c, h)
	hints, err := s.forge.TokenHints()
	if err != nil {
		data.Page.Flash = render.Flash{Message: err.Error(), Level: "error"}
	}
	data.TokenStep = true
	data.Action = forgeTokenPath(h.ID)
	data.Anchor = forgeHostAnchor(h.ID)
	data.KindLabel = h.Kind.Label
	data.TokenHint, data.Token = hints[h.ID]
	data.TokenPage = s.forge.TokenPage(h)
	data.TokenMax = forge.MaxTokenLength
	s.htmlForgeHost(c, code, data, refusal)
}

// htmlForgeHost answers a forge form with code under the refusal, if any,
// the dialog the form alone.
func (s *Server) htmlForgeHost(c *gin.Context, code int, data render.ForgeHostFormData, refusal string) {
	if refusal != "" {
		data.Page.Flash = render.Flash{Message: refusal, Level: "error"}
	}
	switch {
	case !data.Modal:
		c.HTML(code, "forge_host.gohtml", data)
	case data.TokenStep:
		c.HTML(code, "forge_token_form.gohtml", data)
	default:
		c.HTML(code, "forge_host_form.gohtml", data)
	}
}

// refuseForgeHost answers a refused add or save of h, a new host while it
// has no id: the dialog hears why as JSON and keeps standing over what was
// typed, the page shows the form again under the reason with what was sent,
// or the list under it for a host that no longer stands.
func (s *Server) refuseForgeHost(c *gin.Context, h forge.Host, sent forge.Input, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, forge.ErrNotFound) {
		code = http.StatusNotFound
	}
	switch {
	case inFormModal(c) || wantsJSON(c.Request):
		c.JSON(code, gin.H{"error": err.Error()})
	case code == http.StatusNotFound:
		s.renderSettingsForges(c, code, "", err.Error())
	default:
		s.renderForgeHost(c, h, &sent, code, err.Error())
	}
}

// handleForgeHostCreate adds a host, which takes no token, and leads on to
// its token: the dialog swaps the form for it, the page lands on it.
func (s *Server) handleForgeHostCreate(c *gin.Context) {
	if s.forgeRefuseLocal(c, forgeLocalRefusal) {
		return
	}
	in := forgeInput(c)
	h, err := s.forge.Add(in)
	if err != nil {
		s.refuseForgeHost(c, forge.Host{}, in, err)
		return
	}
	s.publishProjects()
	if inFormModal(c) {
		s.renderForgeToken(c, h, http.StatusOK, "")
		return
	}
	s.redirectWithFlash(c, forgeTokenPath(h.ID), "Host added.", "")
}

// handleForgeToken renders the token view of a host, the list under why for
// a host that no longer stands.
func (s *Server) handleForgeToken(c *gin.Context) {
	h, err := s.forge.Host(c.Param("id"))
	if err != nil {
		s.renderSettingsForges(c, http.StatusNotFound, "", err.Error())
		return
	}
	s.renderForgeToken(c, h, http.StatusOK, "")
}

// handleForgeTokenSave keeps the token typed into a host's token view, an
// empty field the one there is, and tests it, or removes the token. A test
// that went through and a removal land on the host's row like the row's
// Test. A refused save keeps the view standing with why, a failed test
// answers the view again, in the dialog too, since it kept the token and
// the view standing names the one before.
func (s *Server) handleForgeTokenSave(c *gin.Context) {
	if s.forgeRefuseLocal(c, forgeLocalRefusal) {
		return
	}
	id := c.Param("id")
	remove := c.PostForm("token_remove") != ""
	code := http.StatusBadRequest
	h, err := s.forge.Host(id)
	if err == nil {
		err = s.forge.ChangeToken(id, c.PostForm("token"), remove)
	}
	if err == nil && remove {
		s.publishProjects()
		s.anchoredFlashResponse(c, settingsForgesPath, forgeHostAnchor(id), "Token removed.", "")
		return
	}
	kept := err == nil
	if kept {
		s.publishProjects()
		code = http.StatusConflict
		_, err = s.forge.Test(c.Request.Context(), id)
	}
	if errors.Is(err, forge.ErrNotFound) {
		code = http.StatusNotFound
	}
	asJSON := inFormModal(c) || wantsJSON(c.Request)
	switch {
	case err == nil:
		s.anchoredFlashResponse(c, settingsForgesPath, forgeHostAnchor(id), "The token works.", "")
	case code == http.StatusNotFound && !asJSON:
		s.renderSettingsForges(c, code, "", err.Error())
	case code != http.StatusNotFound && (!asJSON || kept && inFormModal(c)):
		s.renderForgeToken(c, h, code, err.Error())
	default:
		c.JSON(code, gin.H{"error": err.Error()})
	}
}

// handleForgeHostSave saves an added host's Edit, deletes it or tests a
// host's token. The rows of the projects page follow a change at once. A
// test that went through leads to the list, whose row shows what it found;
// one that failed shows the list with why on the row.
func (s *Server) handleForgeHostSave(c *gin.Context) {
	if s.forgeRefuseLocal(c, forgeLocalRefusal) {
		return
	}
	id := c.Param("id")
	switch c.PostForm("form") {
	case "save":
		h, ok := s.editableForgeHost(c)
		if !ok {
			return
		}
		in := forgeInput(c)
		if _, err := s.forge.Change(id, in); err != nil {
			s.refuseForgeHost(c, h, in, err)
			return
		}
		s.publishProjects()
		s.createLanded(c, settingsForgesPath, "Host saved.", "")
	case "delete":
		if err := s.forge.Delete(id); err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, forge.ErrNotFound) {
				code = http.StatusNotFound
			}
			s.renderSettingsForges(c, code, "", err.Error())
			return
		}
		s.publishProjects()
		s.createLanded(c, settingsForgesPath, "Host deleted.", "")
	case "test":
		_, err := s.forge.Test(c.Request.Context(), id)
		switch {
		case errors.Is(err, forge.ErrNotFound):
			s.renderSettingsForges(c, http.StatusNotFound, "", err.Error())
		case err != nil:
			s.renderSettingsForges(c, http.StatusConflict, forgeHostAnchor(id), err.Error())
		default:
			s.redirectWithAnchoredFlash(c, settingsForgesPath, forgeHostAnchor(id), "The token works.", "")
		}
	default:
		s.renderError(c, http.StatusBadRequest, "Unknown action", "That action isn't available on this page.")
	}
}
