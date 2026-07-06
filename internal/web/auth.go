package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/web/render"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionUserKey  = "user"
	flashMessageKey = "flash_message"
	flashLevelKey   = "flash_level"
	flashProjectKey = "flash_project"
	csrfTokenKey    = "csrf_token"
	csrfFieldName   = "csrf_token"

	saveSessionErrorMessage = "We couldn't save your session. Please try again."
	sessionExpiredMessage   = "Your session has expired. Reload the page to sign in again."
)

type loginForm struct {
	Username string `form:"username" binding:"required"`
	Password string `form:"password" binding:"required"`
	Next     string `form:"next"`
}

func (s *Server) handleLoginGet(c *gin.Context) {
	if s.authenticated(c) {
		c.Redirect(http.StatusSeeOther, "/")
		return
	}
	c.HTML(http.StatusOK, "login.gohtml", render.LoginData{
		Page: s.page(c, "Login", ""),
		Next: safeRedirectPath(c.Query("next")),
	})
}

func (s *Server) handleLoginPost(c *gin.Context) {
	ip := c.ClientIP()
	// Preserve the post-login destination across failed attempts.
	next := safeRedirectPath(c.PostForm("next"))
	loginPath := "/login"
	if next != "/" {
		loginPath = "/login?next=" + url.QueryEscape(next)
	}
	if ok, retry := s.loginLimiter.allow(ip); !ok {
		s.respondBlocked(c, loginPath, retry)
		return
	}

	var form loginForm
	if !s.decodeForm(c, &form, loginPath) {
		return
	}
	form.Username = strings.TrimSpace(form.Username)
	if form.Username != s.cfg.AuthUsername || bcrypt.CompareHashAndPassword([]byte(s.cfg.AuthPasswordHash), []byte(form.Password)) != nil {
		if justBlocked := s.loginLimiter.fail(ip); justBlocked {
			s.respondBlocked(c, loginPath, s.cfg.LoginRateBlock)
			return
		}
		s.redirectWithFlash(c, loginPath, "", "Invalid username or password.")
		return
	}
	s.loginLimiter.reset(ip)
	sess := ginsessions.Default(c)
	sess.Clear()
	sess.Set(sessionUserKey, form.Username)
	if err := sess.Save(); err != nil {
		s.renderError(c, http.StatusInternalServerError, "Something went wrong", saveSessionErrorMessage)
		return
	}
	c.Redirect(http.StatusSeeOther, next)
}

// respondBlocked sends the rate-limit flash and a Retry-After header.
func (s *Server) respondBlocked(c *gin.Context, loginPath string, retry time.Duration) {
	secs := int((retry + time.Second - 1) / time.Second)
	c.Header("Retry-After", strconv.Itoa(secs))
	s.redirectWithFlash(c, loginPath, "", fmt.Sprintf("Too many failed attempts. Try again in %d seconds.", secs))
}

func (s *Server) handleLogout(c *gin.Context) {
	sess := ginsessions.Default(c)
	sess.Clear()
	if err := sess.Save(); err != nil {
		s.renderError(c, http.StatusInternalServerError, "Something went wrong", saveSessionErrorMessage)
		return
	}
	c.Redirect(http.StatusSeeOther, "/login")
}

func (s *Server) requireAuth(c *gin.Context) {
	if s.authenticated(c) {
		c.Next()
		return
	}
	if c.Request.Method == http.MethodGet && !wantsJSON(c.Request) {
		c.Redirect(http.StatusSeeOther, "/login?next="+c.Request.URL.EscapedPath())
		c.Abort()
		return
	}
	// Programmatic callers (the editor's fetches, the upload XHR) get the same
	// JSON {error} contract as every other error — keyed off the same wantsJSON
	// predicate — so they can show a clean toast instead of choking on an HTML
	// login page or a plain-text "Unauthorized" body.
	s.renderError(c, http.StatusUnauthorized, "Session expired", sessionExpiredMessage)
}

func (s *Server) authenticated(c *gin.Context) bool {
	user, _ := ginsessions.Default(c).Get(sessionUserKey).(string)
	return user == s.cfg.AuthUsername
}

func (s *Server) page(c *gin.Context, title, activeTab string) render.Page {
	sess := ginsessions.Default(c)
	message, _ := sess.Get(flashMessageKey).(string)
	level, _ := sess.Get(flashLevelKey).(string)
	flashProject, _ := sess.Get(flashProjectKey).(string)
	if message != "" || level != "" || flashProject != "" {
		sess.Delete(flashMessageKey)
		sess.Delete(flashLevelKey)
		sess.Delete(flashProjectKey)
		_ = sess.Save()
	}
	user, _ := sess.Get(sessionUserKey).(string)
	token := s.csrfToken(c)
	return render.Page{
		Title:        title,
		ActiveTab:    activeTab,
		Flash:        render.Flash{Message: message, Level: level},
		FlashProject: flashProject,
		CSRFToken:    token,
		User:         user,
		MultiCoder:   s.multiCoder(),
		QuickNav:     s.quicknav(c),
	}
}

func (s *Server) redirectWithFlash(c *gin.Context, location, message, errMsg string) {
	sess := ginsessions.Default(c)
	switch {
	case message != "":
		sess.Set(flashMessageKey, message)
		sess.Set(flashLevelKey, "success")
	case errMsg != "":
		sess.Set(flashMessageKey, errMsg)
		sess.Set(flashLevelKey, "error")
	}
	if err := sess.Save(); err != nil {
		s.renderError(c, http.StatusInternalServerError, "Something went wrong", saveSessionErrorMessage)
		return
	}
	c.Redirect(http.StatusSeeOther, location)
}

// redirectWithProjectFlash flashes a message anchored to a project: the projects
// page renders it inside that project's card and scrolls there via the anchor.
// With an empty project it degrades to a normal top-of-page flash on /projects.
func (s *Server) redirectWithProjectFlash(c *gin.Context, project, message, errMsg string) {
	if project == "" {
		s.redirectWithFlash(c, "/projects", message, errMsg)
		return
	}
	sess := ginsessions.Default(c)
	switch {
	case message != "":
		sess.Set(flashMessageKey, message)
		sess.Set(flashLevelKey, "success")
	case errMsg != "":
		sess.Set(flashMessageKey, errMsg)
		sess.Set(flashLevelKey, "error")
	}
	sess.Set(flashProjectKey, project)
	if err := sess.Save(); err != nil {
		s.renderError(c, http.StatusInternalServerError, "Something went wrong", saveSessionErrorMessage)
		return
	}
	c.Redirect(http.StatusSeeOther, "/projects#project-"+project)
}

func safeRedirectPath(path string) string {
	if path == "" || path[0] != '/' {
		return "/"
	}
	// Browsers normalise backslashes to forward slashes and strip tab/CR/LF for
	// http(s) URLs, so /\host and /<tab>/host resolve off-site even though they
	// are not //host. Go's url parser disagrees, so reject these byte sequences
	// outright rather than relying on net/url to flag them.
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '\\', '\t', '\n', '\r':
			return "/"
		}
	}
	if strings.HasPrefix(path, "//") {
		return "/"
	}
	return path
}

// formReturn resolves a safe in-app URL to return to from a create form's
// Cancel, taken from the ?return query and defaulting to the projects page.
func (s *Server) formReturn(c *gin.Context) string {
	if r := safeRedirectPath(strings.TrimSpace(c.Query("return"))); r != "/" {
		return r
	}
	return "/projects"
}

func (s *Server) csrfMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := s.csrfToken(c)
		if isUnsafeMethod(c.Request.Method) {
			got := c.GetHeader("X-CSRF-Token")
			if got == "" {
				got = c.PostForm(csrfFieldName)
			}
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				s.renderError(c, http.StatusForbidden, "Forbidden",
					"Your session expired or the form is no longer valid. Reload the page and try again.")
				return
			}
		}
		c.Next()
	}
}

func (s *Server) csrfToken(c *gin.Context) string {
	sess := ginsessions.Default(c)
	if token, _ := sess.Get(csrfTokenKey).(string); token != "" {
		return token
	}
	token := randomURLToken()
	sess.Set(csrfTokenKey, token)
	_ = sess.Save()
	return token
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

func randomURLToken() string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("generate random token: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}
