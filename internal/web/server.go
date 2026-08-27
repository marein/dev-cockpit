package web

import (
	"context"
	"embed"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/gzip"
	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/backup"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/editorintelligence"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/hostinfo"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/pluginhost"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/push"
	"github.com/marein/dev-cockpit/internal/restore"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/update"
	"github.com/marein/dev-cockpit/internal/voice"
	"github.com/marein/dev-cockpit/internal/web/render"
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
	// quickOpen keeps one file path index per project so the quick open palette
	// can reach every file without walking the tree on every keystroke.
	quickOpen    *filesystem.QuickOpenCache
	notifier     *notify.Service
	bus          *eventbus.Bus
	settings     *settings.Store
	pusher       *push.Service
	restorer     *restore.Service
	version      string
	updater      *update.Updater
	backups      *backup.Service
	assets       staticAssetManifest
	loginLimiter rateLimiter
	termTheme    terminalTheme
	// gitWatchers keeps the editor's per-project git poller alive only while a
	// client says it is watching.
	gitWatchers *gitWatchers
	// gitWrites serializes the editor's git writes per working copy. The
	// editor's own lock is one page's, and a working copy has more than one
	// page on it.
	gitWrites *gitWrites
	// commitDrafts is the commit panel's unsent state per project, message and
	// picked paths, so another device takes the panel over where this one left
	// it.
	commitDrafts *commitDrafts
	// lineComments is the editor's line comments per project, one note per
	// file line, so a pass over the code survives a reload and a device
	// switch.
	lineComments *lineComments
	// askpassBroker and askpassScript are the bridge a user-triggered git
	// action may ask the browser through; nil keeps every prompt failing
	// fast, which is also what the tests run with.
	askpassBroker *askpass.Broker
	askpassScript string
	// gitPromptNoticed is which projects' standing askpass questions have an
	// unread entry in the notification center right now, guarded by its own
	// mutex because the broker's change hook fires from git's helpers.
	gitPromptNoticedMu sync.Mutex
	gitPromptNoticed   map[string]bool
	// host reads load, memory and disk. It is read from the event stream, so
	// an idle cockpit with no browser on it reads nothing at all.
	host *hostinfo.Cache
	// docker is the one connection to the daemon, its cache feeds the
	// container chips on the projects page and the action handlers.
	docker *docker.Service
	// intel owns the language server connections behind the editor's code
	// navigation.
	intel *editorintelligence.Service
	// voice owns the speech engine containers behind the assistant's talk
	// button and spoken answers.
	voice *voice.Service
	// audioBusy holds the answers being spoken right now, one entry per
	// answer and voice, so concurrent asks share one synthesis instead of
	// paying for two; guarded by audioMu.
	audioMu   sync.Mutex
	audioBusy map[string]*spokenAnswer
	// plugins carry what the compiled in plugins added at ConfigureServe,
	// empty on a plain build. The router mounts their subtrees, the templates
	// render their modules and slot markup.
	plugins []*pluginhost.Serve
	// createProject is the one project creation path, shared with the plugin
	// facade; see NewProjectCreator.
	createProject func(ctx context.Context, name string) (string, error)
	// lspWalks caches the per project language detection walk, guarded by
	// lspWalksMu; see lspWalkLanguages.
	lspWalksMu sync.Mutex
	lspWalks   map[string]lspWalkEntry
	// deletes are the project deletions that run past their request, the ones
	// that bring compose stacks down first.
	deletes *projectDeletes
	handler http.Handler
}

// localCallKey marks a request that arrived on the local socket, see
// LocalHandler.
type localCallKeyType struct{}

var localCallKey localCallKeyType

// NewServer constructs a Server serving the given coders. bus is the app
// wide event stream; it is built by the caller because the plugins configure
// before this server exists, against the same bus.
func NewServer(cfg config.Config, coders []*coder.Manager, shells *shell.Shells, conversations *assistant.Service, workspace *assistant.Workspace, watcher *assistant.Watcher, projects *project.Repository, notifier *notify.Service, settingsStore *settings.Store, pusher *push.Service, restorer *restore.Service, backups *backup.Service, dockerService *docker.Service, intel *editorintelligence.Service, voiceService *voice.Service, plugins []*pluginhost.Serve, bus *eventbus.Bus, version, updateFeedURL, updateFeedFormat string, devBuild bool) (*Server, error) {
	if len(coders) == 0 {
		return nil, fmt.Errorf("at least one coder is required")
	}
	assets, err := newStaticAssetManifest(plugins)
	if err != nil {
		return nil, err
	}
	// An unknown update feed format is a build configuration error and fails
	// the start; only a binary that cannot resolve its own path degrades to a
	// server without self-update.
	feedFormat, err := update.ParseFeedFormat(updateFeedFormat)
	if err != nil {
		return nil, err
	}
	updater, err := update.New(version, updateFeedURL, feedFormat, devBuild)
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
		createProject: NewProjectCreator(projects, bus),
		quickOpen:     filesystem.NewQuickOpenCache(),
		notifier:      notifier,
		bus:           bus,
		settings:      settingsStore,
		pusher:        pusher,
		restorer:      restorer,
		version:       version,
		updater:       updater,
		backups:       backups,
		assets:        assets,
		gitWatchers:   newGitWatchers(),
		gitWrites:     newGitWrites(),
		commitDrafts:  newCommitDrafts(cfg.StateDir),
		lineComments:  newLineComments(cfg.StateDir),
		host:          hostinfo.NewCache(cfg.ProjectsRoot, hostSampleTTL),
		docker:        dockerService,
		intel:         intel,
		voice:         voiceService,
		plugins:       plugins,
		deletes:       newProjectDeletes(cfg.StateDir),
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
	// The docker cache moved: every open projects page pulls its own fresh
	// render, the same way it follows the terminals.
	dockerService.OnChange(func() {
		s.bus.Publish(eventbus.Event{Type: "docker"})
	})
	dockerService.OnComposeDone(s.composeDone)
	// A project's indexing picture moved (a server preparing, announcing
	// progress, dying, closing): every open editor of the project pulls the
	// status itself, the same way it follows the git event. No poll stands
	// behind the indicator.
	intel.OnChange(func(project string) {
		s.bus.Publish(eventbus.Event{Type: "lsp", Data: map[string]string{"project": project}})
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
	r.SetHTMLTemplate(render.HTMLTemplate(s.assets.assetPath, s.version, s.assets.digest, s.plugins))
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

// SetAskpass wires the bridge a user-triggered git action may ask the
// browser through. Without it every prompt keeps failing fast. The broker's
// change hook becomes the gitprompt event, which is how a parked, answered
// or expired question reaches every open page at once, and it keeps the
// notification center in step, which is how a question reaches somebody with
// no page open at all.
//
// The boot sweep marks every unread git question entry read first: the
// broker's questions live in memory, so after a restart none stands, and an
// entry a killed process left unread would claim a question forever.
func (s *Server) SetAskpass(broker *askpass.Broker, script string) {
	for target := range s.notifier.UnreadTargets() {
		if notify.IsGitPromptTarget(target) {
			s.notifier.MarkTargetRead(target)
		}
	}
	s.gitPromptNoticed = map[string]bool{}
	s.askpassBroker = broker
	s.askpassScript = script
	// The hook goes on last and reads the broker it was built from, not the
	// field: a question parked between the two lines would have found the field
	// still empty, and the field is written here while the broker's helper
	// goroutines read it, which is a race whether or not it is ever lost.
	broker.OnChange = func() {
		s.publishGitPrompt()
		s.reconcileGitPromptNews(broker)
	}
}

// reconcileGitPromptNews keeps the notification center in step with the
// standing askpass questions: a project whose first question was just parked
// gets an unread entry, a project whose questions are gone gets it read
// again. The tracking is per project like the bridge itself, so ssh asking a
// second time inside one action writes news again only after the first
// question left, which is exactly when the person did not answer it in time.
//
// Only the questions of an external caller count (askpass.Question.External).
// Those were typed in a terminal or by a coding agent, so nobody is looking at
// a page and the question has to leave the app to be seen at all, push
// channels included. A question from the editor's own git surface is the
// opposite case: somebody started it on a page and that page is showing the
// dialog, so news about it would ring for something the person is already
// looking at.
//
// Reading the questions and writing the entries is one step under one lock,
// and that is what makes it right rather than tidy. Two hooks fire at the same
// moment often enough — one for a question that was just parked, one for the
// answer that took it away — and outside the lock the older of the two can
// write its entry after the newer one cleared it. The bell would then claim a
// question that no longer stands, forever, because the map that clears it no
// longer holds the project, and the push channels would carry it to a phone
// two seconds later. Nothing the broker calls takes this lock, and the broker
// calls its hook outside its own locks, so this order has no other side.
func (s *Server) reconcileGitPromptNews(broker *askpass.Broker) {
	s.gitPromptNoticedMu.Lock()
	defer s.gitPromptNoticedMu.Unlock()
	standing := map[string]bool{}
	for _, q := range broker.Questions() {
		if !q.External {
			continue
		}
		standing[q.Project] = true
	}
	for project := range standing {
		if !s.gitPromptNoticed[project] {
			s.gitPromptNoticed[project] = true
			s.notifier.Add(notify.GitPromptTarget(project))
		}
	}
	for project := range s.gitPromptNoticed {
		if !standing[project] {
			delete(s.gitPromptNoticed, project)
			s.notifier.MarkTargetRead(notify.GitPromptTarget(project))
		}
	}
}
