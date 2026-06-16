package web

import (
	"embed"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-contrib/gzip"
	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/provider"
	"github.com/local/dev-cockpit/internal/session"
	"github.com/local/dev-cockpit/internal/web/render"
)

//go:embed static
var staticAssets embed.FS

// Server wires HTTP handling against the domain services.
type Server struct {
	cfg          config.Config
	provider     provider.Provider
	sessions     *session.Sessions
	projects     *project.Repository
	assets       staticAssetManifest
	loginLimiter rateLimiter
	handler      http.Handler
}

// NewServer constructs a Server.
func NewServer(cfg config.Config, selectedProvider provider.Provider, sessions *session.Sessions, projects *project.Repository) (*Server, error) {
	assets, err := newStaticAssetManifest()
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:      cfg,
		provider: selectedProvider,
		sessions: sessions,
		projects: projects,
		assets:   assets,
		loginLimiter: newLoggingLoginLimiter(
			newLoginLimiter(cfg.LoginRateMaxAttempts, cfg.LoginRateWindow, cfg.LoginRateBlock, time.Now),
			cfg.LoginRateBlock, cfg.LoginRateMaxAttempts,
		),
	}
	handler, err := s.newHandler()
	if err != nil {
		return nil, err
	}
	s.handler = handler
	return s, nil
}

// Handler returns the fully-wired HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) newHandler() (http.Handler, error) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.SetHTMLTemplate(render.HTMLTemplate(s.assets.assetPath))
	if err := r.SetTrustedProxies(s.cfg.TrustedProxies); err != nil {
		return nil, fmt.Errorf("set trusted proxies: %w", err)
	}
	r.Use(gin.Logger(), gin.Recovery())
	r.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithMinLength(1024), gzip.WithCustomShouldCompressFn(shouldGzip)))

	store := cookie.NewStore(s.cfg.AuthCookieKey)
	store.Options(s.sessionCookieOptions(false))
	r.Use(ginsessions.Sessions(s.cfg.AuthSessionCookie, store))
	r.Use(s.sessionCookieOptionsMiddleware())

	r.Use(s.bodyLimit())

	s.registerRoutes(r)
	return r, nil
}

func (s *Server) bodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > s.cfg.MaxRequestBodySize {
			c.String(http.StatusRequestEntityTooLarge, http.StatusText(http.StatusRequestEntityTooLarge))
			c.Abort()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, s.cfg.MaxRequestBodySize)
		c.Next()
	}
}

func shouldGzip(c *gin.Context) bool {
	req := c.Request

	if !strings.Contains(req.Header.Get("Accept-Encoding"), "gzip") ||
		strings.Contains(req.Header.Get("Connection"), "Upgrade") ||
		strings.Contains(req.Header.Get("Accept"), "text/event-stream") {
		return false
	}

	return true
}

func (s *Server) sessionCookieOptionsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ginsessions.Default(c).Options(s.sessionCookieOptions(requestIsSecure(c)))
		c.Next()
	}
}

func (s *Server) sessionCookieOptions(secure bool) ginsessions.Options {
	return ginsessions.Options{
		Path:     "/",
		MaxAge:   int(s.cfg.AuthSessionLifetime.Seconds()),
		Secure:   secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func requestIsSecure(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}

	proto := c.GetHeader("X-Forwarded-Proto")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i]
	}
	// ClientIP only differs from RemoteIP when Gin accepted the forwarding headers from a trusted proxy.
	return strings.EqualFold(strings.TrimSpace(proto), "https") && c.ClientIP() != c.RemoteIP()
}
