package web

import (
	"bytes"
	"errors"
	"image"
	_ "image/png"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/browserextension"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/terminal"
)

const browserExtensionAPIPath = apiPathPrefix + "/browser-extension"

const browserExtensionTokenKey = "browser-extension-token"

const (
	maxBrowserExtensionPictureBytes = 32 << 20
	maxBrowserExtensionPictureSide  = 8192
)

// A message for a coder travels as one tmux command, which tmux refuses from
// about 16 KB on.
const maxCoderPromptBytes = 15 << 10

// A refusal may carry a service's words.
const maxBrowserExtensionRefusal = 300

const browserExtensionTokenRefusal = "The token is wrong or revoked. Make a new one in Settings › Browser and paste it into the extension's setup."

// Outside the browser group, so no session cookie opens them and no CSRF
// token is asked: the token rides the Authorization header, which another
// origin cannot set without a preflight nothing here answers.
func (s *Server) registerBrowserExtensionRoutes(r *gin.Engine) {
	g := r.Group(browserExtensionAPIPath, s.openBrowserExtension)
	g.GET(browserExtensionHandshake, s.handleBrowserExtensionHandshake)
	g.GET("/targets", s.handleBrowserExtensionTargets)
	g.POST("/assistants", s.handleBrowserExtensionAssistantNew)
	g.POST("/pictures", s.handleBrowserExtensionPicture)
	g.POST("/send", s.handleBrowserExtensionSend)
}

// Read on every request, so a revoke acts on the next. The access log writes
// no header and the recovery dump masks this one.
func (s *Server) openBrowserExtension(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	auth := c.GetHeader("Authorization")
	if len(auth) > len("Bearer ") && strings.EqualFold(auth[:len("Bearer ")], "Bearer ") {
		if token, err := s.browserExtensionTokens.Lookup(strings.TrimSpace(auth[len("Bearer "):])); err == nil {
			c.Set(browserExtensionTokenKey, token)
			c.Next()
			return
		}
	}
	c.Header("WWW-Authenticate", `Bearer realm="dev-cockpit"`)
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": browserExtensionTokenRefusal})
}

func browserExtensionTokenOf(c *gin.Context) browserextension.Token {
	token, _ := c.MustGet(browserExtensionTokenKey).(browserextension.Token)
	return token
}

func browserExtensionRefuse(c *gin.Context, code int, err error) {
	c.AbortWithStatusJSON(code, gin.H{"error": cutSent(err.Error(), maxBrowserExtensionRefusal)})
}

type browserExtensionAssistant struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type browserExtensionCoder struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Project string `json:"project"`
}

type browserExtensionAssistantCoder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Every extension ever handed out reads this answer to tell whether it fits
// the cockpit, so its path and fields never change, they only grow.
const browserExtensionHandshake = "/version"

func (s *Server) handleBrowserExtensionHandshake(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"extension": browserextension.Version, "api": browserextension.APIVersions})
}

// No more than a picker needs.
func (s *Server) handleBrowserExtensionTargets(c *gin.Context) {
	assistants := []browserExtensionAssistant{}
	for _, entry := range s.assistants.Search("") {
		assistants = append(assistants, browserExtensionAssistant{ID: entry.ID, Name: entry.Title})
	}
	assistantCoders := []browserExtensionAssistantCoder{}
	for _, co := range s.assistantCoderOptions() {
		assistantCoders = append(assistantCoders, browserExtensionAssistantCoder{ID: co.ID, Name: co.Label})
	}
	c.JSON(http.StatusOK, gin.H{"assistants": assistants, "assistantCoders": assistantCoders, "coders": s.runningCoders()})
}

func (s *Server) runningCoders() []browserExtensionCoder {
	type running struct {
		browserExtensionCoder
		tabPos    int
		startedAt time.Time
	}
	all := []running{}
	for i := range s.coders {
		for _, r := range s.coders[i].Snapshot().Running {
			all = append(all, running{browserExtensionCoder{ID: r.Identifier, Name: r.Name, Project: s.projects.ProjectNameFor(r.CWD)}, r.TabPos, r.StartedAt})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		return byTabOrder(all[i].tabPos, all[j].tabPos, all[i].startedAt, all[j].startedAt, all[i].ID, all[j].ID)
	})
	out := make([]browserExtensionCoder, 0, len(all))
	for _, one := range all {
		out = append(out, one.browserExtensionCoder)
	}
	return out
}

// The panel names the coder it shows, so a host with several never gets one
// nobody chose.
func (s *Server) handleBrowserExtensionAssistantNew(c *gin.Context) {
	coderID := c.PostForm("coder")
	if coderID == "" {
		browserExtensionRefuse(c, http.StatusBadRequest, errBrowserExtensionNewWith)
		return
	}
	created, err := s.assistants.Create(coderID)
	if err != nil {
		browserExtensionRefuse(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": created.ID, "name": created.Title, "url": "/assistants/" + created.ID})
}

var (
	errBrowserExtensionTarget  = errors.New("Send to an assistant or a coder.")
	errBrowserExtensionNoCoder = errors.New("No such coder. Pick one from the list again.")
	errBrowserExtensionStopped = errors.New("That coder is not running. Pick a running one.")
	errBrowserExtensionPicture = errors.New("The picture is no PNG.")
	errBrowserExtensionLarge   = errors.New("The picture is larger than 32 MB or 8192 pixels on a side.")
	errBrowserExtensionMessage = errors.New("Type a note or pick something first.")
	errBrowserExtensionCard    = errors.New("A send to an assistant needs its card.")
	errBrowserExtensionCoder   = errors.New("That message is too long for a coder. Keep it under 15 KB.")
	errBrowserExtensionNewWith = errors.New("Pick the coder for the new assistant.")
)

func (s *Server) handleBrowserExtensionPicture(c *gin.Context) {
	header, err := c.FormFile("image")
	if err != nil {
		browserExtensionRefuse(c, http.StatusBadRequest, errBrowserExtensionPicture)
		return
	}
	if header.Size > maxBrowserExtensionPictureBytes {
		browserExtensionRefuse(c, http.StatusRequestEntityTooLarge, errBrowserExtensionLarge)
		return
	}
	src, err := header.Open()
	if err != nil {
		browserExtensionRefuse(c, http.StatusBadRequest, errBrowserExtensionPicture)
		return
	}
	data, err := io.ReadAll(io.LimitReader(src, maxBrowserExtensionPictureBytes+1))
	_ = src.Close()
	if err != nil {
		browserExtensionRefuse(c, http.StatusBadRequest, errBrowserExtensionPicture)
		return
	}
	if code, err := checkPNG(data); err != nil {
		browserExtensionRefuse(c, code, err)
		return
	}
	name := pictureFileName(header.Filename)
	id := c.PostForm("id")
	switch c.PostForm("to") {
	case "assistant":
		if code, err := s.browserExtensionAssistant(id); err != nil {
			browserExtensionRefuse(c, code, err)
			return
		}
		dir, err := s.assistants.UploadDir(id)
		if err != nil {
			browserExtensionRefuse(c, http.StatusBadRequest, err)
			return
		}
		saved, err := s.workspace.SaveUpload(dir, name, bytes.NewReader(data))
		if err != nil {
			browserExtensionRefuse(c, http.StatusInternalServerError, errors.New("The picture could not be saved."))
			return
		}
		c.JSON(http.StatusOK, gin.H{"file": saved.Name})
	case "coder":
		co, code, err := s.browserExtensionCoder(id)
		if err != nil {
			browserExtensionRefuse(c, code, err)
			return
		}
		saved, err := co.Coder().SessionRepository().SaveFile(id, name, bytes.NewReader(data))
		if errors.Is(err, coder.ErrNoSessionFiles) {
			browserExtensionRefuse(c, http.StatusConflict, err)
			return
		}
		if err != nil {
			browserExtensionRefuse(c, http.StatusInternalServerError, errors.New("The picture could not be saved."))
			return
		}
		c.JSON(http.StatusOK, gin.H{"file": saved.Path})
	default:
		browserExtensionRefuse(c, http.StatusBadRequest, errBrowserExtensionTarget)
	}
}

func checkPNG(data []byte) (int, error) {
	if len(data) > maxBrowserExtensionPictureBytes {
		return http.StatusRequestEntityTooLarge, errBrowserExtensionLarge
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "png" || config.Width < 1 || config.Height < 1 {
		return http.StatusBadRequest, errBrowserExtensionPicture
	}
	if config.Width > maxBrowserExtensionPictureSide || config.Height > maxBrowserExtensionPictureSide {
		return http.StatusRequestEntityTooLarge, errBrowserExtensionLarge
	}
	return 0, nil
}

func pictureFileName(sent string) string {
	base := path.Base(strings.ReplaceAll(sent, "\\", "/"))
	stem := strings.TrimSuffix(base, path.Ext(base))
	stem = strings.Map(func(r rune) rune {
		if r < utf8.RuneSelf && (r == '.' || r == '-' || r == '_' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9') {
			return r
		}
		return '-'
	}, stem)
	stem = strings.TrimLeft(stem, ".-")
	if len(stem) > 100 {
		stem = stem[:100]
	}
	if stem == "" {
		stem = "drawing"
	}
	return stem + ".png"
}

func (s *Server) handleBrowserExtensionSend(c *gin.Context) {
	id, message := c.PostForm("id"), c.PostForm("message")
	if strings.TrimSpace(message) == "" {
		browserExtensionRefuse(c, http.StatusBadRequest, errBrowserExtensionMessage)
		return
	}
	switch c.PostForm("to") {
	case "assistant":
		s.browserExtensionSendAssistant(c, id, message)
	case "coder":
		s.browserExtensionSendCoder(c, id, message)
	default:
		browserExtensionRefuse(c, http.StatusBadRequest, errBrowserExtensionTarget)
	}
}

func (s *Server) browserExtensionSendAssistant(c *gin.Context, id, message string) {
	if code, err := s.browserExtensionAssistant(id); err != nil {
		browserExtensionRefuse(c, code, err)
		return
	}
	raw := c.PostForm("card")
	if raw == "" {
		browserExtensionRefuse(c, http.StatusBadRequest, errBrowserExtensionCard)
		return
	}
	picked, err := assistant.ParsePicked(raw)
	if err != nil {
		browserExtensionRefuse(c, http.StatusBadRequest, err)
		return
	}
	attachments, err := s.assistantAttachments(id, c.PostFormArray("picture"))
	if err != nil {
		browserExtensionRefuse(c, http.StatusBadRequest, err)
		return
	}
	run, err := s.assistants.PromptPicked(id, message, picked, browserExtensionTokenOf(c).Name, attachments...)
	if err != nil {
		browserExtensionRefuse(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": "/assistants/" + id, "name": run.Title, "queued": run.Queued})
}

func (s *Server) browserExtensionSendCoder(c *gin.Context, id, message string) {
	if len(message) > maxCoderPromptBytes {
		browserExtensionRefuse(c, http.StatusBadRequest, errBrowserExtensionCoder)
		return
	}
	co, code, err := s.browserExtensionCoder(id)
	if err != nil {
		browserExtensionRefuse(c, code, err)
		return
	}
	if err := s.sendCoderInput(co, id, []terminal.Input{{Prompt: message}}); err != nil {
		if errors.Is(err, coder.ErrNotRunning) {
			browserExtensionRefuse(c, http.StatusGone, errBrowserExtensionStopped)
			return
		}
		browserExtensionRefuse(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": "/coders/" + id})
}

func (s *Server) browserExtensionAssistant(id string) (int, error) {
	if _, err := s.assistants.Get(id); err != nil {
		return http.StatusNotFound, errors.New("No such assistant. Pick one from the list again.")
	}
	if err := s.assistantWriteGuard(id); err != nil {
		return http.StatusConflict, err
	}
	return 0, nil
}

func (s *Server) browserExtensionCoder(id string) (*coder.Manager, int, error) {
	if id == "" || len(id) > 200 {
		return nil, http.StatusNotFound, errBrowserExtensionNoCoder
	}
	co, err := s.coderForInput(id)
	if errors.Is(err, coder.ErrNotRunning) {
		return nil, http.StatusGone, errBrowserExtensionStopped
	}
	if err != nil {
		return nil, http.StatusNotFound, errBrowserExtensionNoCoder
	}
	return co, 0, nil
}
