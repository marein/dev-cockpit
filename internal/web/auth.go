package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
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
	csrfTokenKey    = "csrf_token"
	csrfFieldName   = "csrf_token"
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
	if ok, retry := s.loginLimiter.allow(ip); !ok {
		s.respondBlocked(c, retry)
		return
	}

	var form loginForm
	if !s.decodeForm(c, &form, "/login") {
		return
	}
	form.Username = strings.TrimSpace(form.Username)
	if form.Username != s.cfg.AuthUsername || bcrypt.CompareHashAndPassword([]byte(s.cfg.AuthPasswordHash), []byte(form.Password)) != nil {
		if justBlocked := s.loginLimiter.fail(ip); justBlocked {
			s.respondBlocked(c, s.cfg.LoginRateBlock)
			return
		}
		s.redirectWithFlash(c, "/login", "", "Invalid username or password.")
		return
	}
	s.loginLimiter.reset(ip)
	sess := ginsessions.Default(c)
	sess.Clear()
	sess.Set(sessionUserKey, form.Username)
	if err := sess.Save(); err != nil {
		c.String(http.StatusInternalServerError, "Internal Server Error")
		return
	}
	c.Redirect(http.StatusSeeOther, safeRedirectPath(form.Next))
}

// respondBlocked sends the rate-limit flash and a Retry-After header.
func (s *Server) respondBlocked(c *gin.Context, retry time.Duration) {
	secs := int((retry + time.Second - 1) / time.Second)
	c.Header("Retry-After", strconv.Itoa(secs))
	s.redirectWithFlash(c, "/login", "", fmt.Sprintf("Too many failed attempts. Try again in %d seconds.", secs))
}

func (s *Server) handleLogout(c *gin.Context) {
	sess := ginsessions.Default(c)
	sess.Clear()
	if err := sess.Save(); err != nil {
		c.String(http.StatusInternalServerError, "Internal Server Error")
		return
	}
	c.Redirect(http.StatusSeeOther, "/login")
}

func (s *Server) requireAuth(c *gin.Context) {
	if s.authenticated(c) {
		c.Next()
		return
	}
	if c.Request.Method == http.MethodGet && acceptsHTML(c.Request) {
		c.Redirect(http.StatusSeeOther, "/login?next="+c.Request.URL.EscapedPath())
		c.Abort()
		return
	}
	c.String(http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized))
	c.Abort()
}

func (s *Server) authenticated(c *gin.Context) bool {
	user, _ := ginsessions.Default(c).Get(sessionUserKey).(string)
	return user == s.cfg.AuthUsername
}

func (s *Server) page(c *gin.Context, title, activeTab string) render.Page {
	sess := ginsessions.Default(c)
	message, _ := sess.Get(flashMessageKey).(string)
	level, _ := sess.Get(flashLevelKey).(string)
	if message != "" || level != "" {
		sess.Delete(flashMessageKey)
		sess.Delete(flashLevelKey)
		_ = sess.Save()
	}
	user, _ := sess.Get(sessionUserKey).(string)
	token := s.csrfToken(c)
	return render.Page{
		Title:     title,
		ActiveTab: activeTab,
		Flash:     render.Flash{Message: message, Level: level},
		CSRFToken: token,
		User:      user,
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
		c.String(http.StatusInternalServerError, "Internal Server Error")
		return
	}
	c.Redirect(http.StatusSeeOther, location)
}

func acceptsHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return accept == "" || strings.Contains(accept, "text/html") || strings.Contains(accept, "*/*")
}

func safeRedirectPath(path string) string {
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "/"
	}
	return path
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
				c.String(http.StatusForbidden, "Forbidden")
				c.Abort()
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
