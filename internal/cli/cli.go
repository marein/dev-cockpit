// Package cli is the dev-cockpit CLI. Importers never see it: the distro
// package is the thin facade a custom distribution builds on, and the
// shipped binary is the thin main in cmd/dev-cockpit.
package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/backup"
	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
	codercopilot "github.com/marein/dev-cockpit/internal/coder/copilot"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/cost"
	"github.com/marein/dev-cockpit/internal/cost/price"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/editorintelligence"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/forge"
	forgegithub "github.com/marein/dev-cockpit/internal/forge/github"
	forgegitlab "github.com/marein/dev-cockpit/internal/forge/gitlab"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/markdown"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/ollama"
	"github.com/marein/dev-cockpit/internal/pluginhost"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/push"
	"github.com/marein/dev-cockpit/internal/recent"
	"github.com/marein/dev-cockpit/internal/restore"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/tmux"
	"github.com/marein/dev-cockpit/internal/update"
	"github.com/marein/dev-cockpit/internal/voice"
	"github.com/marein/dev-cockpit/internal/web"
	"github.com/marein/dev-cockpit/plugin"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
)

// uploadGrace is how long an upload may wait for the message that carries it
// before the reaper treats it as abandoned.
const uploadGrace = time.Hour

const (
	ollamaCatalogURL    = "https://ollama.com/api/tags"
	ollamaCatalogURLEnv = "DEV_COCKPIT_OLLAMA_CATALOG_URL"
)

// version is the release tag, handed in through Main. A build where it
// stayed exactly "dev" is a dev build, the only kind that honors the
// DEV_COCKPIT_UPDATE_API_URL override; any other value counts as a release.
var version = "dev"

// repoURL is the web page of the repository this build belongs to, a full
// URL, handed in through Main. It is where the app's source link points.
var repoURL = "https://github.com/marein/dev-cockpit"

// updateFeedURL is the release feed this build checks for updates, a full
// URL taken exactly as given, handed in through Main. Only a dev build
// (version stayed "dev") honors the DEV_COCKPIT_UPDATE_API_URL override on
// top of it.
var updateFeedURL = "https://api.github.com/repos/marein/dev-cockpit/releases?per_page=100"

// updateFeedFormat names the JSON dialect of the release feed, github or
// gitlab, handed in through Main. An unknown value fails every invocation
// instead of guessing a mapping, see the check in Main.
var updateFeedFormat = "github"

// servePlugins are the compiled in plugins as ordered named pairs, handed in
// through Main. distro.Main validated the wiring before it delegates; the
// plugins themselves configure at serve start, see runServe.
var servePlugins []plugin.Named[plugin.ServePlugin]

type serveOptions struct {
	config.Options
}

// resolveVersion returns the injected release version, or for local builds
// falls back to the VCS revision Go stamps into the binary automatically.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	var rev, suffix string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				suffix = "-dirty"
			}
		}
	}
	if rev == "" {
		return version
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return "dev-" + rev + suffix
}

// Build mirrors distro.Build. A zero field keeps this package's compiled
// default.
type Build struct {
	Version          string
	RepoURL          string
	UpdateFeedURL    string
	UpdateFeedFormat string
	ServePlugins     []plugin.Named[plugin.ServePlugin]
}

// Main runs the dev-cockpit CLI and exits the process on an error.
func Main(b Build) {
	if b.Version != "" {
		version = b.Version
	}
	if b.RepoURL != "" {
		repoURL = b.RepoURL
	}
	if b.UpdateFeedURL != "" {
		updateFeedURL = b.UpdateFeedURL
	}
	if b.UpdateFeedFormat != "" {
		updateFeedFormat = b.UpdateFeedFormat
	}
	servePlugins = b.ServePlugins
	// The update feed format is validated before cobra runs: a binary built
	// with an unknown feed format must fail every invocation, --version
	// included, because the smoke test a running updater gives a downloaded
	// binary before the swap is exactly that call, and its exit code is the
	// whole contract. Inside cobra would be too late, the version template
	// prints before any PersistentPreRun hook.
	if _, err := update.ParseFeedFormat(updateFeedFormat); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "dev-cockpit",
		Short:         "Manage and serve dev-cockpit",
		Version:       resolveVersion(),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return errors.New("command required")
		},
	}
	cmd.SetVersionTemplate(versionTemplate(repoURL, updateFeedFormat, updateFeedURL))
	cmd.AddCommand(newServeCommand(), newHashPasswordCommand(), newGitCommand(), newAssistantCommand(), newDockerCommand(), newClaudeCommand(), newCodexCommand(), newOpencodeCommand(), newRunDetachedCommand(), newChildGuardCommand(), newAskpassCommand())
	return cmd
}

// versionTemplate builds the --version text: the version line, then the
// distribution below it, where the source lives and which feed updates come
// from, the values injected at build time. Nothing parses this output, the
// updater's smoke test only reads the exit code, so the format is for people.
// The keys are the exact build var names, so every line maps onto the
// -X main.<name> flag that sets it.
// Each injected value is embedded as a quoted template string literal via %q,
// so a value carrying template syntax such as {{ prints literally instead of
// breaking the parse, and --version keeps exiting zero.
func versionTemplate(repo, format, feed string) string {
	return fmt.Sprintf(
		"dev-cockpit version {{.Version}}\n  repoURL: {{%q}}\n  updateFeedFormat: {{%q}}\n  updateFeedURL: {{%q}}\n",
		repo, format, feed)
}

// defaultOptions are the serve defaults. The inspection commands start from
// the same set and override only the two directories they read, so a config
// value never differs between the server and its own status output.
func defaultOptions() config.Options {
	return config.Options{
		HTTPAddr:           config.DefaultHTTPAddr,
		ProjectsDir:        config.DefaultProjectsDir,
		StateDir:           config.DefaultStateDir,
		AuthUsername:       config.DefaultAuthUsername,
		AuthPasswordHash:   config.DefaultAuthPasswordHash,
		SessionCookieName:  config.DefaultSessionCookieName,
		SessionCookieKey:   config.DefaultSessionCookieKey,
		TrustedProxies:     config.DefaultTrustedProxies,
		TLSCertFile:        config.DefaultTLSCertFile,
		TLSKeyFile:         config.DefaultTLSKeyFile,
		MaxRequestBodySize: config.DefaultMaxRequestBody,
	}
}

func newServeCommand() *cobra.Command {
	opts := serveOptions{Options: defaultOptions()}
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the web server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(opts)
		},
	}

	flags := cmd.Flags()
	// TODO(v2.0.0): drop the --provider flag entirely.
	var deprecatedProvider string
	flags.StringVar(&deprecatedProvider, "provider", "", "ignored, the server serves every installed coder")
	_ = flags.MarkDeprecated("provider", "the server now serves every installed coder")
	flags.StringVar(&opts.HTTPAddr, "addr", opts.HTTPAddr, "HTTP address")
	flags.StringVar(&opts.ProjectsDir, "projects-dir", opts.ProjectsDir, "projects root directory")
	flags.StringVar(&opts.StateDir, "state-dir", opts.StateDir, "directory for dev-cockpit state files")
	flags.StringVar(&opts.AuthUsername, "auth-user", opts.AuthUsername, "auth username")
	flags.StringVar(&opts.AuthPasswordHash, "auth-password-hash", opts.AuthPasswordHash, "bcrypt hash for auth password")
	flags.StringVar(&opts.SessionCookieName, "session-cookie-name", opts.SessionCookieName, "session cookie name")
	flags.StringVar(&opts.SessionCookieKey, "session-cookie-key", opts.SessionCookieKey, "session cookie signing key")
	flags.StringVar(&opts.TrustedProxies, "trusted-proxies", opts.TrustedProxies, "comma-separated trusted proxy IPs or CIDRs")
	flags.StringVar(&opts.TLSCertFile, "tls-cert-file", opts.TLSCertFile, "TLS certificate file for HTTPS")
	flags.StringVar(&opts.TLSKeyFile, "tls-key-file", opts.TLSKeyFile, "TLS private key file for HTTPS")
	flags.Int64Var(&opts.MaxRequestBodySize, "max-request-body-size", opts.MaxRequestBodySize, "maximum request body size in bytes")
	return cmd
}

func runServe(opts serveOptions) error {
	cfg, err := config.Load(opts.Options)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if _, err := bcrypt.Cost([]byte(cfg.AuthPasswordHash)); err != nil {
		return fmt.Errorf("invalid --auth-password-hash: %w", err)
	}
	tmuxClient := tmux.New()
	projectRepo := project.NewRepository(cfg.ProjectsRoot, recent.New(filepath.Join(cfg.StateDir, "recent-projects.json"))).UseTags(project.NewTags(cfg.StateDir))
	// The plugins configure here, with the configuration loaded and before
	// anything listens: everything their Serve answers is real, and what they
	// add becomes part of the server built below. The bus exists this early
	// so the Projects facade delegates to the web UI's own creation path,
	// which announces on it; the server below is built on the same bus. The
	// assistants need the plugins' settings and memory first, so their
	// surface is bound once the server stands. A plugin that cannot
	// configure aborts the start with its id on the error.
	bus := eventbus.New()
	createProject, projectChanged := web.NewProjectCreator(projectRepo, bus), web.NewProjectChanged(bus)
	pluginAssistants := &pluginhost.Assistants{}
	serves, err := pluginhost.ConfigureServe(servePlugins, cfg.ProjectsRoot, cfg.StateDir, createProject, projectChanged, pluginAssistants)
	if err != nil {
		return err
	}
	// Sweep ends what a previous process left running, no plugin runs twice.
	// Scan builds every enabled external plugin's Serve without starting it
	// and configures the switched on optional ones. From here on every
	// surface reads the plugins through the set.
	pluginHost := pluginhost.NewHost(pluginhost.HostConfig{
		StateDir:       cfg.StateDir,
		ProjectsDir:    cfg.ProjectsRoot,
		Compiled:       serves,
		Wiring:         servePlugins,
		CreateProject:  createProject,
		ProjectChanged: projectChanged,
		Assistants:     pluginAssistants,
		CSRFField:      web.CSRFField,
		PluginAPI:      devPluginAPI(),
		CheckEvery:     devPluginCheckEvery(),
	})
	pluginHost.Sweep()
	pluginHost.Scan()
	plugins := pluginhost.NewSet(serves, pluginHost)
	// The settings store stands before the coders: every coder's model
	// repository keeps its added names in it, and the model defaults the
	// managers and the assistant read on every start come out of it.
	settingsStore := settings.New(filepath.Join(cfg.StateDir, "settings.json"))
	pluginhost.ApplySettings(plugins.Serves(), settingsStore)
	catalog := ollamaCatalogURL
	if version == "dev" {
		if override := os.Getenv(ollamaCatalogURLEnv); override != "" {
			catalog = override
		}
	}
	launcher := ollama.New(settingsStore, catalog, cfg.StateDir)
	go launcher.Run()
	registry := coder.NewRegistry(knownCoders(cfg.StateDir, settingsStore, launcher)...)
	selected, err := selectProviders(registry)
	if err != nil {
		return err
	}

	coders := make([]*coder.Manager, 0, len(selected))
	for _, c := range selected {
		manager := coder.NewManager(cfg, tmuxClient, c, projectRepo)
		if err := manager.StopIdleStreams(); err != nil {
			log.Printf("failed to stop idle terminal stream(s): %v", err)
		}
		// A session that picks no model starts on the coder's stored start
		// default, read on every start so the settings page applies at once.
		coderID := c.ID()
		manager.SetModelDefaults(func() assistant.ModelDefaults {
			return assistant.ModelDefaultsFor(settingsStore, coderID)
		})
		coders = append(coders, manager)
		// The model list of a coder that fetches it in the background starts
		// its first fetch here, at the serve start, so the first New coder
		// dialog and the first ring menu find it filled, whether or not the
		// coder can hold a conversation.
		coder.WarmModels(c)
	}
	// The assistant owns the browser side conversations. Its reservation filter
	// goes into every manager before the first snapshot, otherwise a
	// conversation's provider session would also be listed as a resumable coder.
	executable := clirun.Executable()
	conversations, assistantService, err := newAssistant(cfg.StateDir, assistantCoders{coders: selected, store: settingsStore}, assistant.Cockpit{
		Executable:  executable,
		StateDir:    cfg.StateDir,
		ProjectsDir: cfg.ProjectsRoot,
		Version:     resolveVersion(),
		RepoURL:     repoURL,
	}, plugins.Serves())
	if err != nil {
		return fmt.Errorf("failed to initialize the assistant: %w", err)
	}
	for _, m := range coders {
		coderID := m.ID()
		m.SetHidden(func(sessionID string) bool {
			return conversations.Reserved(coderID, sessionID)
		})
	}
	// The cockpit's own skill, the coder side of the git proxy, rendered from
	// this instance's configuration the way the assistant instructions are:
	// the next start rewrites it when the binary or the start flags moved. A
	// coder whose home refuses the write keeps running, the skill is help and
	// no requirement.
	instance := coder.CockpitInstance{
		Executable: executable,
		StateDir:   cfg.StateDir,
		Running:    cockpitServesFrom,
	}
	for _, c := range selected {
		if err := coder.EnsureManagedSkills(c.SkillRepository(), instance); err != nil {
			log.Printf("coder %s: the cockpit git skill could not be written: %v", c.ID(), err)
		}
	}
	syncClaudeStatusLine(cfg.StateDir, settingsStore)

	// The cost books stand before anything can delete a session: the startup
	// sweep below already removes check sessions, and each delete books what
	// the session spent last.
	prices := price.NewBook(cfg.StateDir, func() bool { return settingsStore.Get(price.RefreshSettingKey) != "off" })
	keepMonths := func() int { return cost.Retention(settingsStore.Get(cost.RetentionSettingKey)) }
	costs := cost.New(cfg.StateDir, costPlacer(assistantService, projectRepo),
		costSources(prices, costPlaces(conversations, assistantService, projectRepo), keepMonths)...)
	costs.SetZone(func() *time.Location { return web.CostZone(settingsStore) })
	costs.SetRetention(keepMonths)
	watchSessionDeletes(selected, costs)

	shells := shell.NewShells(cfg, tmuxClient, projectRepo, func() bool {
		return settingsStore.Get(shell.HistorySettingKey) == "on"
	})
	backups := backup.New(cfg.StateDir, cfg.ProjectsRoot, resolveVersion())

	// The one docker connection of the cockpit. It re-reads the setting on
	// every round, so a save on the settings page reaches it without a
	// restart. Built before the notifier, whose resolver names the last
	// compose run.
	dockerService := docker.NewService(cfg.StateDir, func() string {
		return settingsStore.Get(docker.HostSettingKey)
	})

	// The askpass broker exists before the notifier because the resolver names
	// a standing question's action out of it; its socket and helper stub are
	// wired further down with the server.
	askBroker := askpass.New(cfg.StateDir)

	resolveTarget := notifyResolver(coders, shells, conversations, projectRepo, backups, dockerService, askBroker)
	notifier := notify.NewService(notify.StorePath(cfg.StateDir), resolveTarget)
	// The push channels subscribe before any watcher starts, so an inbox
	// backlog ingested right after boot cannot slip past them.
	pushService, err := push.NewService(cfg.StateDir)
	if err != nil {
		return fmt.Errorf("failed to initialize push channels: %w", err)
	}
	pushService.Start(notifier)

	// The registry is built before the startup pass, which prunes the jobs of
	// terminals that are gone the same way it prunes their notifications. It
	// reaches every assistant's jobs, because a terminal that is gone is gone
	// for whoever was steering it.
	jobs := assistant.NewJobs(assistant.NewStore(cfg.StateDir))

	// The agent stands before the startup restore, whose sessions get their
	// socket like every other.
	agent := newSSHAgent(cfg.StateDir, settingsStore, coders, shells, conversations)

	// The startup pass runs before the watchers and the server, so restored
	// sessions are in place when the first page renders. Off by default, the
	// snapshot file itself is kept current regardless of the setting.
	restorer := restore.New(
		filepath.Join(cfg.StateDir, "terminal-restore.json"),
		func() bool { return settingsStore.Get(restore.SettingKey) == "on" },
		coders, shells, tmuxClient, notifier, jobs,
		func() []string {
			var names []string
			for _, p := range projectRepo.List() {
				names = append(names, p.Name)
			}
			return names
		},
	)
	restorer.SetLauncherReady(func(name string, timeout time.Duration) bool {
		return name != ollama.Executable || launcher.WaitReachable(timeout)
	})
	restorer.RunStartup()
	go restorer.RunPeriodic(30 * time.Second)

	// Restore has recreated its shells under their old ids by now, so the
	// startup reap keeps them and drops only the truly orphaned history files.
	shells.ReapHistory()
	go shells.RunHistoryReaper(10 * time.Minute)

	// A file is stored when it is picked, so a message that never got sent
	// leaves its upload behind. The grace period is generous, the only cost of
	// keeping one too long is disk.
	conversations.ReapUploads(uploadGrace)
	go conversations.RunUploadReaper(30*time.Minute, uploadGrace)

	// A coder that reports something wakes the assistant, but only for a job
	// somebody asked to be steered. The signal is the raw one the notification
	// center ingests, not the notification it makes of it: whether a job is
	// checked may not depend on whether a person has read their phone, and the
	// notification center's quiet window only holds while an entry is unread.
	// The job's own quiet window lives in the watcher.
	watcher := assistant.NewWatcher(
		conversations,
		jobs,
		coderSessions{coders: coders},
		// How many checks may run at once is a house rule, read again before
		// every check so the setting applies without a restart.
		func() int { return web.ConcurrentChecks(settingsStore) },
	)
	// The working marks. The tracker holds the shelves, everything below only
	// feeds it: the coders' hooks (wireWorkingMark), the record watchers and
	// the bells (watchWorkingMarks further down), and the session watchers'
	// raw screen movement for the coders that chose the fallback shelf.
	tracker := newWorkingMarks(cfg.StateDir, bus)
	// Editor intelligence owns the language server child processes. On a
	// stop signal they are shut down along the protocol before exit; the
	// self-update exec path needs no hook because the pipe ends close on
	// exec and the servers exit on stdin EOF. Containers a previous process
	// left behind, and caches of projects that left the disk, are swept in
	// the background; the first server start waits the sweep out, and the
	// root label keeps other live instances' servers untouched.
	intel := editorintelligence.New(cfg.ProjectsRoot, editorintelligence.CacheRoot(cfg.StateDir), func() string {
		return dockerService.State().Host
	})
	intel.SweepStale()
	// The voice service owns the speech engine containers the same way:
	// warmed on first use, stopped after the idle timeout, leftovers of a
	// previous process swept at boot behind the state root label.
	voiceService := voice.New(cfg.StateDir, func() string {
		return dockerService.State().Host
	}, settingsStore.Get, func(engineID string) {
		bus.Publish(eventbus.Event{Type: "voice-warming", Data: map[string]string{"engine": engineID}})
	})
	voiceService.SweepStale()
	stop := make(chan os.Signal, 2)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-stop
		log.Printf("received %s, shutting down", sig)
		// The graceful close is bounded (it aborts a running image build
		// first), and a second signal ends the wait the hard way.
		go func() {
			sig := <-stop
			log.Printf("received %s again, exiting now", sig)
			os.Exit(1)
		}()
		removeManagedSkills(selected, instance)
		pluginHost.Close()
		intel.Close()
		voiceService.Close()
		os.Exit(0)
	}()

	// version stays the raw build var here on purpose: only a build without an
	// injected release version is a dev build, and only a dev build may have
	// its release feed moved by the environment.
	srv, err := web.NewServer(cfg, coders, shells, conversations, assistantService, watcher, projectRepo, notifier, tracker, settingsStore, launcher, pushService, restorer, backups, dockerService, intel, voiceService, plugins, bus, resolveVersion(), updateFeedURL, updateFeedFormat, version == "dev")
	if err != nil {
		return fmt.Errorf("failed to initialize web server: %w", err)
	}
	// A plugin that loads while the cockpit runs gets its settings and memory
	// at load, as the start applied them above.
	pluginHooks := srv.PluginHooks()
	pluginHooks.Loaded = func(p *pluginhost.Serve) {
		pluginhost.ApplySettings([]*pluginhost.Serve{p}, settingsStore)
		applyPluginMemory(cfg.StateDir, assistantService, []*pluginhost.Serve{p})
	}
	pluginAssistants.Bind(srv.PluginAssistants())
	pluginHost.Start(pluginHooks)
	// A finished answer is normal cockpit news, and a change refreshes the open
	// lists. Neither hook may run while the service holds its lock, both are
	// called after it is released.
	conversations.SetHooks(srv.PublishConversations, notifier.Add)
	srv.SetPostScriptFailed(func(name string, run project.PostScriptRun) {
		info := notify.TargetInfo{Name: name, Project: name, URL: "/projects#project-" + name, Failed: true}
		info.Title, info.Detail = postScriptNews(name, run)
		notifier.AddResolved(notify.WorktreeTarget(name), info)
	})
	srv.SetGuestComment(func(note web.GuestComment) {
		info := notify.TargetInfo{Name: note.Project, Project: note.Project, URL: "/projects/" + url.PathEscape(note.Project) + "/editor?view=comments"}
		info.Title, info.Detail = commentNews(note)
		notifier.AddResolved(notify.CommentsTarget(note.Project), info)
	})
	conversations.SetRenderer(markdown.RenderGFM)
	notifier.SetSignal(watcher.Handle)
	wireWorkingMark(notifier, tracker, turnReaders(coders))
	// The same signal, with the hook name it arrived under, is a coder event the
	// assistants may react to: a Notification hook is a coder that asks a
	// question or wants a permission, every other name is a turn that ended. The
	// headline names the coder the way a notification does.
	events := conversations.Events()
	// The same events reach the plugins that subscribed to their source.
	events.OnPublish(func(ev assistant.CockpitEvent) {
		plugins.Publish(plugin.Event{Source: ev.Source, Kind: ev.Kind, Target: ev.Target, Time: ev.Time, Headline: ev.Headline, Body: ev.Body, Project: ev.Project})
	})
	events.SetCoderNamer(func(terminal string) (string, string) {
		info := resolveTarget(terminal)
		return info.Name, info.Project
	})
	notifier.SetEvent(func(targetID, hook string) {
		kind := assistant.CoderKindEnded
		if hook == "Notification" {
			kind = assistant.CoderKindAsks
		}
		events.Coder(targetID, kind)
	})
	// A coder somebody steers has an assistant looking at it, so its own news
	// stays quiet and that assistant's report is what the user hears. Whose
	// job it is does not matter here: what is silenced is the coder's own
	// news, and it is silenced because somebody is watching it. The
	// notification center knows nothing about jobs, it only asks this.
	notifier.SetSilent(func(targetID string) bool {
		job, ok := jobs.Find(targetID)
		return ok && job.State.Open()
	})
	// A codex coder started without a prompt takes codex's own id with its
	// first message and keeps its terminal. Its signals carry that id from
	// then on, so what was steered under the key follows it there.
	for _, m := range coders {
		m.OnMoved(watcher.TerminalMoved)
	}

	// Everything a turn needs exists from here on, so from here on a turn may
	// run. The order below is the whole rule: serve the local API, pick up what
	// the previous process left running, and only then start the things that can
	// ask for a new turn. A turn acts through this server's own local API, so one
	// that starts before the socket answers cannot do its job, and one that
	// starts before the hooks are set answers into nothing.
	//
	// The assistant answers from a coder with a shell, so it could write the
	// state files directly; the socket is the path that keeps the serve process
	// the only writer of its own state.
	localListener, err := localapi.Listen(cfg.StateDir)
	if err != nil {
		return fmt.Errorf("failed to open the local API socket: %w", err)
	}
	defer localListener.Close()
	go func() {
		if err := (&http.Server{Handler: srv.LocalHandler(), ConnContext: localapi.ConnContext}).Serve(localListener); err != nil {
			log.Printf("the local API stopped: %v", err)
		}
	}()

	// The askpass bridge's socket, next to the local API's, plus the helper
	// stub SSH_ASKPASS and GIT_ASKPASS of a user-triggered action point at.
	// Only the git handlers of the editor and the proxy ever hand its
	// environment to a call, everything else keeps failing prompts fast.
	askListener, err := askpass.Listen(cfg.StateDir)
	if err != nil {
		return fmt.Errorf("failed to open the askpass socket: %w", err)
	}
	defer askListener.Close()
	go func() {
		if err := (&http.Server{Handler: askBroker.Handler()}).Serve(askListener); err != nil {
			log.Printf("the askpass bridge stopped: %v", err)
		}
	}()
	askScript, err := askpass.WriteScript(cfg.StateDir)
	if err != nil {
		return fmt.Errorf("failed to write the askpass helper: %w", err)
	}
	srv.SetAskpass(askBroker, askScript)
	srv.SetSSHAgent(agent)
	costs.SetOnChange(func() { bus.Publish(eventbus.Event{Type: "costs"}) })
	srv.SetCosts(costs, prices)
	forgeService := forge.New(cfg.StateDir, []forge.Provider{forgegithub.Provider{}, forgegitlab.Provider{}}, forgegithub.Builtin(), forgegitlab.Builtin())
	srv.SetForge(forgeService)
	// The end of a pipeline of a bound project's checked out branch is an
	// event the assistants may react to, once per pipeline and final status,
	// and a failed one is news.
	forgeService.OnPipelineEnd(pipelineEnded(events.Pipeline, notifier))
	// The review threads of that branch's change request stand among the
	// project's line comment threads, kept in step by the poller, and a new
	// note somebody else wrote is an event, once per note.
	forgeService.OnReview(srv.ForgeReview, srv.ForgeKept)
	forgeService.OnReviewNote(func(n forge.ReviewNote) { events.ReviewComment(reviewReport(n)) })
	backups.SetRestored(keepForgeOverrides(forgeService, projectRepo))

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", cfg.HTTPAddr, err)
	}

	// The turns of the previous process are still writing into their files.
	// They are read on, and the checks among them go back to the watcher, which
	// is the one that knows their jobs; only then does the watcher decide about
	// the jobs whose check did not survive.
	adopted := conversations.Recover()
	watcher.Recover(adopted)
	sweepCheckSessions(coders, assistantService.IsWorkdir)
	// The triggers are on disk with everything they were in the middle of, so
	// the reactor only catches up on what happened while nobody was listening:
	// a job that closed in the gap, a cron tick that fell due, a batch window
	// that closed. After the turns above, because a turn it starts has to see
	// whether a chat turn is running.
	events.Recover()
	// The compose runs of the previous process are detached the same way and
	// keep going too. This puts their busy marks and their directory claims
	// back, and reports the ones that finished while nobody was there to hear
	// it. It runs before anything can start a run of its own.
	dockerService.Recover()
	// An approval waited on a question the last process held; nobody can
	// answer it now, so each is declined and its assistant reads why. Which
	// ones those are was read when the server was built, before the local API
	// served: an approval a surviving turn asked for since is this process's
	// own and keeps waiting.
	srv.RecoverApprovals()
	// A job is looked at even when nothing reports: a coder that stopped, that
	// ran out of room to think in or that waits on a question sends nothing at
	// all, and that is exactly the job that needs looking at. The pass reads what
	// the coder already wrote down and only buys a check when the picture says it
	// is worth one. It also ends the jobs whose time or budget is up, because a
	// job that goes quiet has no signal left to write that report with.
	go watcher.RunHeartbeat(0)
	go events.Run(0)

	for _, c := range selected {
		go notifier.RunInbox(notify.InboxDir(cfg.StateDir, c.ID()), c.ID(), time.Second)
	}
	go shells.RunCommandWatch(3*time.Second, func(shellID string) {
		notifier.Add(shellID)
	})
	// After the server is up, so the change callback is wired before the
	// first list can move the state. A machine without a daemon just idles.
	go dockerService.Run(context.Background())
	go agent.Run(context.Background(), agentRound)
	go costs.Run(costPollInterval, costOwners(coders, conversations))
	go prices.Run(context.Background())
	// The forge poller keeps the status of the bound projects' branches the
	// pages and the local API read, and tells them when one moved.
	go forgeService.Run(context.Background(), srv.ForgeWatches, srv.PublishForge)
	// The project deletions the last process was in the middle of. Their rows
	// already say they are working, that was read when the server was built;
	// this is the work behind them starting again, and it waits for the docker
	// connection above, which is why it comes after it and in its own goroutine.
	go srv.ResumeProjectDeletes()
	// A rename inside a coder only has to be handed to the same announcement
	// every other terminal change makes, and every surface follows it without
	// a page of its own asking anybody. The event names the project and
	// nothing else, the way a start or a stop does, so the moved name itself
	// travels no further than here: every surface pulls its own fragment and
	// reads it there.
	onRenamed := func(id, name, cwd string) { srv.PublishTerminals(projectRepo.ProjectNameFor(cwd)) }
	for _, m := range coders {
		if m.ID() == "copilot" {
			if err := codercopilot.EnsureBeepSetting(); err != nil {
				log.Printf("copilot beep setting: %v", err)
			}
		}
		watchWorkingMarks(m, tracker, onRenamed, notifier.Event)
	}

	return serveHTTP(&http.Server{Handler: srv.Handler()}, listener, cfg.TLSCertFile, cfg.TLSKeyFile)
}
