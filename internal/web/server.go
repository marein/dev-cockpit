package web

import (
	"context"
	"embed"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-contrib/gzip"
	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/assistant"
	"github.com/local/dev-cockpit/internal/auth"
	"github.com/local/dev-cockpit/internal/backup"
	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/eventbus"
	"github.com/local/dev-cockpit/internal/notify"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/push"
	"github.com/local/dev-cockpit/internal/restore"
	"github.com/local/dev-cockpit/internal/settings"
	"github.com/local/dev-cockpit/internal/shell"
	"github.com/local/dev-cockpit/internal/update"
	"github.com/local/dev-cockpit/internal/web/render"
)

//go:embed static
var staticAssets embed.FS

// Server wires HTTP handling against the domain services.
type Server struct {
	cfg    config.Config
	coders []*coder.Manager
	shells *shell.Shells
	// conversations drives the cockpit's own conversations, assistant owns
	// their workspace and memory.
	conversations *assistant.Service
	assistant     *assistant.Workspace
	// watcher owns the steered jobs: what the assistant keeps an eye on and
	// what wakes it.
	watcher  *assistant.Watcher
	projects *project.Repository
	notifier *notify.Service
	bus      *eventbus.Bus
	settings *settings.Store
	// auth holds the alternative sign in methods. It is built here instead of
	// being passed in because nothing outside the web layer reads it: the
	// files only ever decide what a login page offers.
	auth         *auth.Store
	pusher       *push.Service
	restorer     *restore.Service
	version      string
	updater      *update.Updater
	backups      *backup.Service
	assets       staticAssetManifest
	loginLimiter rateLimiter
	termTheme    terminalTheme
	handler      http.Handler
}

// localCallKey marks a request that arrived on the local socket, see
// LocalHandler.
type localCallKeyType struct{}

var localCallKey localCallKeyType

// NewServer constructs a Server serving the given coders.
func NewServer(cfg config.Config, coders []*coder.Manager, shells *shell.Shells, conversations *assistant.Service, workspace *assistant.Workspace, watcher *assistant.Watcher, projects *project.Repository, notifier *notify.Service, settingsStore *settings.Store, pusher *push.Service, restorer *restore.Service, backups *backup.Service, version string) (*Server, error) {
	if len(coders) == 0 {
		return nil, fmt.Errorf("at least one coder is required")
	}
	assets, err := newStaticAssetManifest()
	if err != nil {
		return nil, err
	}
	updater, err := update.New(version)
	if err != nil {
		log.Printf("self-update disabled: %v", err)
		updater = nil
	}
	s := &Server{
		cfg:           cfg,
		coders:        coders,
		shells:        shells,
		conversations: conversations,
		assistant:     workspace,
		watcher:       watcher,
		projects:      projects,
		notifier:      notifier,
		bus:           eventbus.New(),
		settings:      settingsStore,
		auth:          auth.New(cfg.AuthDir),
		pusher:        pusher,
		restorer:      restorer,
		version:       version,
		updater:       updater,
		backups:       backups,
		assets:        assets,
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
	// A job beginning or ending is ownership changing on a terminal, and the
	// steered mark sits in server rendered fragments. The plain terminals
	// event is what makes every surface pull them, the same way it follows a
	// terminal that starts or stops; the restore snapshot is untouched, no
	// terminal changed.
	watcher.OnStateChange(func(project string) {
		s.bus.Publish(eventbus.Event{Type: "terminals", Data: map[string]string{"project": project}})
	})
	return s, nil
}

// Handler returns the fully-wired HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// LocalHandler serves the same routes for callers on this machine, over the
// socket in the state directory (see internal/localapi). Reaching that socket is
// the whole credential: it lives in a directory only the owner of this process
// may enter, so a caller that can open it is already allowed to change what the
// cockpit holds. The request is marked here, at the door, and that marker is
// what the session check and the CSRF check read.
func (s *Server) LocalHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), localCallKey, true)))
	})
}

func (s *Server) newHandler() (http.Handler, error) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.HandleMethodNotAllowed = true
	r.SetHTMLTemplate(render.HTMLTemplate(s.assets.assetPath, s.version, s.assets.digest))
	if err := r.SetTrustedProxies(s.cfg.TrustedProxies); err != nil {
		return nil, fmt.Errorf("set trusted proxies: %w", err)
	}
	r.Use(gin.Logger(), gin.CustomRecovery(s.recoveryHandler))
	// No WithMinLength: gin-contrib/gzip (<= v1.2.6) drops its buffered prefix
	// when a single write of minLength+ bytes arrives while the buffer is still
	// below the threshold, truncating responses whose head is small chunks
	// followed by one large one (e.g. the quicknav fragment with an inline SVG).
	r.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithCustomShouldCompressFn(shouldGzip)))

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
		// The backup import legitimately carries whole host archives (it can
		// hold the complete projects directory), that one authenticated
		// route runs without a request cap.
		if c.Request.Method == http.MethodPost && c.Request.URL.Path == "/settings/backup" {
			c.Next()
			return
		}
		if c.Request.ContentLength > s.cfg.MaxRequestBodySize {
			s.renderError(c, http.StatusRequestEntityTooLarge, "File too large",
				"The upload exceeds the maximum allowed size.")
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
		strings.Contains(req.Header.Get("Accept"), "text/event-stream") ||
		strings.HasSuffix(req.URL.Path, "/download") ||
		// Already compressed or byte ranged: the editor's tar.gz would be gzipped
		// a second time, and the raw endpoint serves images, video and audio.
		strings.HasSuffix(req.URL.Path, "/editor/archive") ||
		strings.HasSuffix(req.URL.Path, "/editor/raw") ||
		// The assistant serves images, audio and video from here, byte ranged.
		strings.Contains(req.URL.Path, "/assistant/") && strings.Contains(req.URL.Path, "/media/") {
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
