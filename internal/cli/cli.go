// Package cli is the dev-cockpit CLI. Importers never see it: the distro
// package is the thin facade a custom distribution builds on, and the
// shipped binary is the thin main in cmd/dev-cockpit.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/backup"
	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
	coderclaude "github.com/marein/dev-cockpit/internal/coder/claude"
	codercopilot "github.com/marein/dev-cockpit/internal/coder/copilot"
	coderopencode "github.com/marein/dev-cockpit/internal/coder/opencode"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/detach"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/editorintelligence"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/markdown"
	"github.com/marein/dev-cockpit/internal/notify"
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
	"github.com/marein/dev-cockpit/internal/web/render"
	"github.com/marein/dev-cockpit/plugin"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
)

// uploadGrace is how long an upload may wait for the message that carries it
// before the reaper treats it as abandoned.
const uploadGrace = time.Hour

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
	cmd.AddCommand(newServeCommand(), newHashPasswordCommand(), newGitCommand(), newAssistantCommand(), newDockerCommand(), newRunDetachedCommand(), newAskpassCommand())
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

// newAskpassCommand is what SSH_ASKPASS and GIT_ASKPASS of a user-triggered
// git action point at, through the tiny stub the server writes next to its
// socket (ssh executes the askpass program without arguments of ours). It is
// nobody's interface: it reads the one-time token and the socket from its
// environment, reports the prompt line to the serve process, blocks until
// the person in the browser answered, and prints that answer. Without the
// bridge environment it fails like /bin/false, which is the fate of every
// call that is not allowed to ask.
func newAskpassCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "askpass [prompt]",
		Short:              "Answer one ssh or git prompt through the running cockpit",
		Hidden:             true,
		DisableFlagParsing: true,
		SilenceErrors:      true,
		SilenceUsage:       true,
		// A denial exits silently: whatever this command wrote would end up
		// in git's error message as if git had said it, and the server
		// already words the refusal.
		Run: func(cmd *cobra.Command, args []string) {
			socket := os.Getenv("DC_ASKPASS_SOCKET")
			token := os.Getenv("DC_ASKPASS_TOKEN")
			if socket == "" || token == "" {
				os.Exit(1)
			}
			prompt := ""
			if len(args) > 0 {
				prompt = strings.TrimSpace(strings.Join(args, " "))
			}
			answer, err := askpass.Ask(socket, token, prompt)
			if err != nil {
				os.Exit(1)
			}
			fmt.Println(answer)
		},
	}
}

// newAssistantCommand groups everything the cockpit's own assistant runs. They
// share the two directory flags, because every one of them has to reach the same
// instance: a machine can run several, and a command that read the default state
// directory would answer about the wrong cockpit. The whole group is hidden from
// the top level help, it is not a user interface, typed explicitly it works.
func newAssistantCommand() *cobra.Command {
	opts := &inspectOptions{stateDir: config.DefaultStateDir, projectsDir: config.DefaultProjectsDir}
	cmd := &cobra.Command{
		Use:    "assistant",
		Short:  "Commands the cockpit's AI assistant acts with",
		Hidden: true,
		Long: "The AI assistant built into dev-cockpit runs these commands to look at and steer " +
			"the running instance. They are not a user interface: names, flags and output are " +
			"tuned for the assistant and may change with any release. Use the web UI instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return errors.New("command required")
		},
	}
	cmd.PersistentFlags().StringVar(&opts.stateDir, "state-dir", opts.stateDir, "directory for dev-cockpit state files")
	cmd.PersistentFlags().StringVar(&opts.projectsDir, "projects-dir", opts.projectsDir, "projects root directory")
	// The names put the object first and the verb last, so the flat help list
	// groups by object on its own: every coder- command stands together, then
	// the conversations, the jobs, the projects.
	cmd.AddCommand(
		newStatusCommand(opts),
		newCoderCommand(opts), newActivityCommand(opts),
		newSendCommand(opts), newKeysCommand(opts),
		newSteerCommand(opts), newReleaseCommand(opts),
		newResumeCoderCommand(opts), newStopCoderCommand(opts), newDeleteCoderCommand(opts),
		newConversationsCommand(opts), newConversationCommand(opts),
		newJobsCommand(opts), newJobCommand(opts),
		newNotificationsCommand(opts),
		newProjectCommand(opts), newDeleteProjectCommand(opts),
		newLineCommentListCommand(opts), newLineCommentAddCommand(opts), newLineCommentRemoveCommand(opts),
		newOutputCommand(opts),
		newRunTurnCommand(),
	)
	return cmd
}

// newRunTurnCommand is where a turn's hold process used to live, and it has to
// stay reachable although this group may rename what it likes. That exemption
// rests on the caller being the freshly generated instruction text, always in
// step with the binary it names. This one command has a different caller: a
// server of the previous version that is still in memory while this binary is
// already on disk, and every turn it starts execs this binary with the argv it
// knows. That happens in the window between a self update swapping the binary
// and re-execing, after a re-exec that did not come off, and on any host where
// the binary is replaced before the service is restarted. Without this line the
// turn dies with an unknown command, and what the user reads is the coder's
// stderr, which points at the coder and not at us.
//
// It is the same process as `run-detached` behind an older name: the argv of an
// old caller carries no separator, and internal/detach reads that shape as the
// program alone. TODO(v2.0.0): drop it once no binary that starts turns this
// way can still be in memory.
func newRunTurnCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "run-turn <provider> [args...]",
		Short:              "Run one turn's provider and hold its lock (older name of run-detached)",
		Hidden:             true,
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			os.Exit(detach.Hold(args))
		},
	}
}

// newRunDetachedCommand is the process a detached run runs under, not a
// command for anyone to type: the server starts
// `dev-cockpit run-detached [--result <file>] [--timeout <d>] -- <program> ...`
// for every run that has to outlive it, an assistant turn and a compose run
// alike, and that process holds the run's lock while the program runs. It is
// nobody's interface, so it is hidden, and it stays callable because every such
// run hangs on it.
//
// Flag parsing is off: everything after the separator is the program's argv,
// including whatever a prompt carries, and none of it is ours to interpret.
// internal/detach reads the few arguments that are ours itself, which is also
// what lets the test binary stand in for this command.
func newRunDetachedCommand() *cobra.Command {
	return &cobra.Command{
		Use:                detach.HoldArgs[0] + " [--result <file>] [--timeout <duration>] -- <program> [args...]",
		Short:              "Run one detached program and hold its lock",
		Hidden:             true,
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			os.Exit(detach.Hold(args))
		},
	}
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

func newHashPasswordCommand() *cobra.Command {
	cost := bcrypt.DefaultCost
	cmd := &cobra.Command{
		Use:   "hash-password",
		Short: "Hash a password with bcrypt",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHashPassword(os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr(), cost)
		},
	}
	cmd.Flags().IntVar(&cost, "cost", cost, "bcrypt cost")
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
	projectRepo := project.NewRepository(cfg.ProjectsRoot, recent.New(filepath.Join(cfg.StateDir, "recent-projects.json")))
	// The plugins configure here, with the configuration loaded and before
	// anything listens: everything their Serve answers is real, and what they
	// add becomes part of the server built below. The bus exists this early
	// so the Projects facade delegates to the web UI's own creation path,
	// which announces on it; the server below is built on the same bus. A
	// plugin that cannot configure aborts the start with its id on the error.
	bus := eventbus.New()
	serves, err := pluginhost.ConfigureServe(servePlugins, cfg.ProjectsRoot, cfg.StateDir, web.NewProjectCreator(projectRepo, bus), web.NewProjectChanged(bus))
	if err != nil {
		return err
	}
	registry := coder.NewRegistry(codercopilot.New(), coderclaude.New(notify.InboxDir(cfg.StateDir, "claude")), coderopencode.New(notify.InboxDir(cfg.StateDir, "opencode")))
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
		coders = append(coders, manager)
	}
	// The assistant owns the browser side conversations. Its reservation filter
	// goes into every manager before the first snapshot, otherwise a
	// conversation's provider session would also be listed as a resumable coder.
	executable := runningExecutable()
	conversations, assistantService, err := assistant.New(cfg.StateDir, assistantCoders{coders: selected}, assistant.Cockpit{
		Executable:  executable,
		StateDir:    cfg.StateDir,
		ProjectsDir: cfg.ProjectsRoot,
		Version:     resolveVersion(),
		RepoURL:     repoURL,
	})
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

	settingsStore := settings.New(filepath.Join(cfg.StateDir, "settings.json"))
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

	notifier := notify.NewService(
		notify.StorePath(cfg.StateDir),
		notifyResolver(coders, shells, conversations, projectRepo, backups, dockerService, askBroker),
	)
	// The push channels subscribe before any watcher starts, so an inbox
	// backlog ingested right after boot cannot slip past them.
	pushService, err := push.NewService(cfg.StateDir)
	if err != nil {
		return fmt.Errorf("failed to initialize push channels: %w", err)
	}
	pushService.Start(notifier)

	// The store is built before the startup pass, which prunes the jobs of
	// terminals that are gone the same way it prunes their notifications.
	jobs := assistant.NewJobStore(cfg.StateDir)

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
	)
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
		intel.Close()
		voiceService.Close()
		os.Exit(0)
	}()

	// version stays the raw build var here on purpose: only a build without an
	// injected release version is a dev build, and only a dev build may have
	// its release feed moved by the environment.
	srv, err := web.NewServer(cfg, coders, shells, conversations, assistantService, watcher, projectRepo, notifier, settingsStore, pushService, restorer, backups, dockerService, intel, voiceService, serves, bus, resolveVersion(), updateFeedURL, updateFeedFormat, version == "dev")
	if err != nil {
		return fmt.Errorf("failed to initialize web server: %w", err)
	}
	// A finished answer is normal cockpit news, and a change refreshes the open
	// lists. Neither hook may run while the service holds its lock, both are
	// called after it is released.
	conversations.SetHooks(srv.PublishConversations, notifier.Add)
	conversations.SetRenderer(markdown.RenderGFM)
	notifier.SetSignal(watcher.Handle)
	// A coder somebody steers has the assistant looking at it, so its own news
	// stays quiet and the assistant's report is what the user hears. The
	// notification center knows nothing about jobs, it only asks this.
	notifier.SetSilent(func(targetID string) bool {
		job, ok := jobs.Get(targetID)
		return ok && job.State.Open()
	})

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
		if err := (&http.Server{Handler: srv.LocalHandler()}).Serve(localListener); err != nil {
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
	sweepCheckSessions(coders, assistantService.Workspace())
	// The compose runs of the previous process are detached the same way and
	// keep going too. This puts their busy marks and their directory claims
	// back, and reports the ones that finished while nobody was there to hear
	// it. It runs before anything can start a run of its own.
	dockerService.Recover()
	// A job is looked at even when nothing reports: a coder that stopped, that
	// ran out of room to think in or that waits on a question sends nothing at
	// all, and that is exactly the job that needs looking at. The pass reads what
	// the coder already wrote down and only buys a check when the picture says it
	// is worth one. It also ends the jobs whose time or budget is up, because a
	// job that goes quiet has no signal left to write that report with.
	go watcher.RunHeartbeat(0)

	for _, c := range selected {
		go notifier.RunInbox(notify.InboxDir(cfg.StateDir, c.ID()), time.Second)
	}
	go shells.RunCommandWatch(3*time.Second, func(shellID string) {
		notifier.Add(shellID)
	})
	// After the server is up, so the change callback is wired before the
	// first list can move the state. A machine without a daemon just idles.
	go dockerService.Run(context.Background())
	// The project deletions the last process was in the middle of. Their rows
	// already say they are working, that was read when the server was built;
	// this is the work behind them starting again, and it waits for the docker
	// connection above, which is why it comes after it and in its own goroutine.
	go srv.ResumeProjectDeletes()
	for _, m := range coders {
		if m.ID() != "copilot" {
			continue
		}
		if err := codercopilot.EnsureBeepSetting(); err != nil {
			log.Printf("copilot beep setting: %v", err)
		}
		go m.RunBellWatch(3*time.Second, func(targetID string) {
			notifier.Signal(targetID)
		})
	}

	server := &http.Server{Handler: srv.Handler()}
	if cfg.TLSCertFile != "" {
		log.Printf("listening on https://%s", cfg.HTTPAddr)
		return server.ServeTLS(listener, cfg.TLSCertFile, cfg.TLSKeyFile)
	}
	log.Printf("listening on http://%s", cfg.HTTPAddr)
	return server.Serve(listener)
}

// removeManagedSkills takes the cockpit's own skills off the disk on the way
// out, the counterpart of the start's EnsureManagedSkills. The skill points a
// coder at the local API socket of a running instance, so one left behind
// after the stop would send every coder down a path that cannot answer. It
// runs before the language servers are closed, because it is a few file
// removals while that close is bounded but not instant, and a second signal
// may end the process during it.
//
// It is deliberately not the only thing keeping the disk clean: a SIGKILL and
// the self-update's exec both walk past this, and both are covered by the
// start rewriting the skill anyway.
//
// The state directory says which skill is this instance's. A coder home is
// shared by every cockpit on the machine, and a throwaway stopping next to the
// real instance may not take the running instance's skill with it.
func removeManagedSkills(coders []coder.Coder, instance coder.CockpitInstance) {
	for _, c := range coders {
		if err := coder.RemoveManagedSkills(c.SkillRepository(), instance); err != nil {
			log.Printf("coder %s: the cockpit git skill could not be removed: %v", c.ID(), err)
		}
	}
}

// cockpitServesFrom answers whether some cockpit is still listening on the
// local API socket of a state directory. It is what keeps a second instance
// from taking the managed skill away from the one that is running, and it is a
// single connect with no retry on purpose: the answer is wanted at start, and
// localapi.Dial waits out a budget for a cockpit that may still be coming up,
// which is the opposite question.
func cockpitServesFrom(stateDir string) bool {
	conn, err := net.DialTimeout("unix", localapi.SocketPath(stateDir), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// runningExecutable is the absolute path of this binary, handed to the
// assistant so its inspection commands work regardless of PATH or of where
// the server was started from. A binary replaced underneath a running process
// (a self-update between the swap and the re-exec) reads back with a
// " (deleted)" marker, which would land in the generated instructions.
func runningExecutable() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(path, " (deleted)")
}

// selectProviders resolves which coders this instance serves: every registered
// coder whose CLI is installed.
func selectProviders(registry *coder.Registry) ([]coder.Coder, error) {
	if missing := clirun.MissingTools(tmux.RequiredTools); len(missing) > 0 {
		return nil, fmt.Errorf("missing CLI tools: %v", missing)
	}
	var selected []coder.Coder
	for _, p := range registry.All() {
		if missing := clirun.MissingTools(p.RequiredTools()); len(missing) > 0 {
			log.Printf("coder %s disabled, missing CLI tools: %v", p.ID(), missing)
			continue
		}
		selected = append(selected, p)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no coder CLI found (looked for: %s)", strings.Join(registry.IDs(), ", "))
	}
	return selected, nil
}

// notifyResolver enriches notifications with the name, project, and target
// page at ingest time, using the cached coder snapshots and shell list so a
// burst of events never rescans coder state.
func notifyResolver(coders []*coder.Manager, shells *shell.Shells, conversations *assistant.Service, projects *project.Repository, backups *backup.Service, dockerService *docker.Service, askBroker *askpass.Broker) notify.Resolver {
	return func(targetID string) notify.TargetInfo {
		info := notify.TargetInfo{}
		if notify.IsGitPromptTarget(targetID) {
			project := notify.GitPromptTargetProject(targetID)
			info.Name = "Git"
			info.Project = project
			// The dialog is app-wide, any signed-in page shows it, so the
			// entry leads home instead of to a page that may be gone.
			info.URL = "/projects"
			// The entry is written while the question stands, so the project's
			// running action is the one it is about; a question that vanished
			// in between keeps the generic word.
			actionName := "git"
			if action := askBroker.Find(project); action != nil {
				actionName = action.Name()
			}
			info.Title, info.Detail = gitPromptNews(actionName, project)
			return info
		}
		if notify.IsDockerTarget(targetID) {
			project := notify.DockerTargetProject(targetID)
			info.Name = "Compose"
			info.Project = project
			info.URL = "/projects"
			if project != "" {
				info.URL = "/projects#project-" + project
			}
			// The notification fires right after a run of that project
			// finished, so its newest finished run is the one it is about.
			// Asked per project, because another project may have finished a
			// run in the same moment.
			if run, ok := dockerService.LastComposeRun(project); ok {
				info.Title, info.Detail = composeNews(run)
				// A failure is no follow-up: right after a success the fresh
				// unread entry says everything went through, and the dedupe
				// window would swallow the one word the user is owed.
				info.Urgent = run.Failure != ""
				// The output of the run is what somebody wants after such a
				// notification, not the row it ran for.
				if run.ID != "" {
					info.URL = "/projects/" + url.PathEscape(project) + "/docker/runs/" + run.ID
				}
			}
			return info
		}
		// The assistant comes first: its conversations are hidden from the
		// coder lists, and it is the only surface with a fixed home, so the
		// lookup is cheap and cannot be shadowed by a coder of the same id.
		for _, entry := range conversations.List() {
			if entry.ID != targetID {
				continue
			}
			// There is one assistant, so its news never carries a conversation
			// title: the entry says what happened. The assistant has no pages,
			// the link opens the overlay via the query, and the fragment names
			// the answer the overlay scrolls to.
			info.Name = assistant.Name
			info.URL = "/projects?assistant=" + entry.ID
			info.Title = fmt.Sprintf("%s answered.", assistant.Name)
			if m, ok := conversations.LastAnswer(entry.ID); ok {
				info.URL += "#message-" + m.ID
				info.Title, info.Detail = assistantNews(m)
			}
			return info
		}
		if targetID == notify.BackupTarget {
			info.Name = "Backup"
			info.URL = "/settings/backup"
			// The notification fires right after a job finished, so the
			// newest finished entry is the one it is about.
			if b, ok := backups.LastFinished(); ok {
				info.Title, info.Detail = backupNews(b.Name, b.Done())
			}
			return info
		}
		for _, m := range coders {
			snap := m.Snapshot()
			for _, r := range snap.Running {
				if r.Identifier == targetID {
					info.Name = r.Name
					info.Project = projects.ProjectNameFor(r.CWD)
					info.URL = "/coders/" + r.Identifier
					info.Title, info.Detail = coderNews(info.Name, info.Project)
					return info
				}
			}
			for _, stored := range snap.Resumable {
				if stored.SessionID == targetID {
					info.Name = stored.Name
					info.Project = projects.ProjectNameFor(stored.CWD)
					info.URL = "/coders/" + stored.SessionID
					info.Title, info.Detail = coderNews(info.Name, info.Project)
					return info
				}
			}
		}
		for _, sh := range shells.List() {
			if sh.Identifier == targetID {
				info.Name = sh.Name
				info.Project = projects.ProjectNameFor(sh.CWD)
				info.URL = "/shells/" + sh.Identifier
				info.Title, info.Detail = shellNews(info.Name, info.Project)
				return info
			}
		}
		// A target nothing knows any more, a session deleted between the
		// signal and this lookup: there is no kind, so there is no sentence
		// either, and every surface falls back to "Something new in ...".
		return info
	}
}

// Every notification this cockpit writes reads the same way: the title says
// what happened, the line below it says which one it happened in. The wording
// of all of them stands here, next to each other, because that is the only way
// it stays one wording. internal/notify writes the entries and classifies
// nothing, so nothing of this belongs there.

// newsTarget writes that lower line for a named target: the name in quotes,
// because a name made of ordinary words would otherwise read as part of a
// sentence, and the project behind it when there is one.
func newsTarget(name, project string) string {
	name = strings.TrimSpace(name)
	project = strings.TrimSpace(project)
	if name == "" {
		return ""
	}
	if project == "" {
		return fmt.Sprintf("%q", name)
	}
	return fmt.Sprintf("%q - %s", name, project)
}

// coderNews is what a coder's signal says, and it says no more than that there
// is news: whether the coder finished its turn, asks a question or waits for a
// permission is what this cockpit deliberately does not classify, see
// internal/notify.
func coderNews(name, project string) (title, detail string) {
	return "Coder has news.", newsTarget(name, project)
}

// shellNews is what a shell's signal says. A shell reports when a foreground
// command ended, so that is what the title says.
func shellNews(name, project string) (title, detail string) {
	return "Command finished.", newsTarget(name, project)
}

// backupNews is what a finished backup job says. A backup belongs to no
// project, so its lower line is the archive name alone.
func backupNews(name string, ok bool) (title, detail string) {
	title = "Backup failed."
	if ok {
		title = "Backup ready."
	}
	return title, newsTarget(name, "")
}

// gitPromptNews is what a standing askpass question says: git is waiting for
// an answer, and the line below names the action and the project it runs in.
// The entry is marked read again the moment the question no longer stands,
// however it went, so the title may speak in the present.
func gitPromptNews(action, project string) (title, detail string) {
	return "Git asks a question.", newsTarget(action, project)
}

// composeNews is what a finished docker compose run says: the command that
// ran as the name, the project it ran for behind it.
func composeNews(run docker.RunView) (title, detail string) {
	title = "Compose finished."
	if run.Failure != "" {
		title = "Compose failed."
	}
	name := run.Action
	if name == "" {
		name = "compose"
	}
	return title, newsTarget(name, run.Project)
}

// assistantNews is what a notification about the assistant says: a report about
// a job says how the job ended and names it below, everything else says that an
// answer arrived and shows the first words of it, because that title says that
// something arrived and not what.
//
// The job's name and project come from the message's own note, never from a
// lookup: this resolver runs before the job store exists, and a terminal that is
// steered again would hand back the successor's job. A report from before the
// note carried them names what it can.
func assistantNews(m assistant.Message) (title, detail string) {
	if m.Wake != nil {
		// A job ends in one of three states and the title says which one, in
		// the words of the JobState it was closed with.
		switch m.Wake.Verdict {
		case string(assistant.VerdictDone):
			title = "Job done."
		case string(assistant.VerdictBlocked):
			title = "Job blocked."
		case string(assistant.VerdictExpired):
			title = "Job expired."
		}
		if title != "" {
			return title, newsTarget(m.Wake.Name, m.Wake.Project)
		}
	}
	if m.State == assistant.StateFailed || m.State == assistant.StateInterrupted {
		// Whatever was written before it broke off is still the best line
		// about what the turn was doing.
		return fmt.Sprintf("%s could not finish.", assistant.Name), answerExcerpt(m.Content)
	}
	return fmt.Sprintf("%s answered.", assistant.Name), answerExcerpt(m.Content)
}

// answerExcerptRunes is how much of an answer a notification carries: enough
// for the first sentence or two, short enough for a toast and for a phone's
// notification body.
const answerExcerptRunes = 140

// answerExcerpt turns an answer into the line a notification shows. The answer
// is Markdown written for a page, so the markup goes through the parser instead
// of a hand-rolled cut, and everything that was a line break or an indent
// becomes one space: this lands in a single line wherever it surfaces. The cut
// sits on a word boundary, a word torn in half reads as a rendering fault.
func answerExcerpt(content string) string {
	plain := strings.Join(strings.Fields(markdown.Plain(content)), " ")
	if utf8.RuneCountInString(plain) <= answerExcerptRunes {
		return plain
	}
	cut := plain
	runes := 0
	for i := range plain {
		if runes == answerExcerptRunes {
			cut = plain[:i]
			break
		}
		runes++
	}
	if space := strings.LastIndexByte(cut, ' '); space > 0 {
		cut = cut[:space]
	}
	return cut + "…"
}

// assistantCoders adapts the installed coders to what the assistant needs,
// keeping internal/assistant free of any dependency on internal/coder.
type assistantCoders struct{ coders []coder.Coder }

func (c assistantCoders) Available() []assistant.CoderInfo {
	out := make([]assistant.CoderInfo, 0, len(c.coders))
	for _, co := range c.coders {
		runner := coder.AssistantRunnerFor(co)
		if runner == nil {
			continue
		}
		id := co.ID()
		out = append(out, assistant.CoderInfo{ID: id, Label: render.CoderLabel(id), Runner: runner})
	}
	return out
}

// isStrayCheckSession decides what the startup sweep deletes. The name alone is
// user text, somebody may call a coder "cockpit check: whatever", and deleting
// it would take that coder's transcript with it. A check always runs in the
// assistant's own workspace, so both have to hold.
func isStrayCheckSession(session coder.Session, workspace string) bool {
	return assistant.IsCheckSession(session.Name) && sameDir(session.CWD, workspace)
}

// sameDir compares two directories the way the file system would, so a trailing
// slash or a relative spelling does not decide whether something is deleted.
func sameDir(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if resolved, err := filepath.Abs(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.Abs(b); err == nil {
		b = resolved
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// coderSessions lets a check ask the coder of a job what that session last did.
// Which coder answers comes from the job, and how it answers is the coder's
// business: one that keeps a transcript reads it, one that keeps none falls
// back to the terminal picture. A job whose coder id is not among the installed
// ones (an old job, a coder that was removed) is offered to each of them, and
// the one that owns the session answers.
type coderSessions struct{ coders []*coder.Manager }

func (c coderSessions) Activity(coderID, terminal string) (assistant.Activity, error) {
	if coderID != "" {
		for _, m := range c.coders {
			if m.ID() == coderID {
				activity, err := m.Activity(terminal, 0, coder.ActivityBudget)
				return assistant.Activity(activity), err
			}
		}
	}
	// No coder of that name is installed here, or the job never carried one:
	// ask them all, the one that owns the session answers. A job outlives the
	// set of installed coders, and answering "nobody answers" would give the
	// check a verdict about an error message instead of about the session.
	var lastErr error
	for _, m := range c.coders {
		activity, err := m.Activity(terminal, 0, coder.ActivityBudget)
		if err == nil {
			return assistant.Activity(activity), nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("No coder answers for %q.", terminal)
	}
	return assistant.Activity{}, lastErr
}

// Running reports whether the job's terminal is still a live session. A job
// asks before it spends anything, because a terminal that is gone is not a
// question for a coder, it is a fact the cockpit already holds.
func (c coderSessions) Running(coderID, terminal string) bool {
	for _, m := range c.coders {
		if coderID != "" && m.ID() != coderID {
			continue
		}
		if _, err := m.ResolveRunning(terminal); err == nil {
			return true
		}
	}
	return false
}

func runHashPassword(stdin *os.File, stdout, stderr io.Writer, cost int) error {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		return fmt.Errorf("bcrypt cost must be between %d and %d", bcrypt.MinCost, bcrypt.MaxCost)
	}
	fd := int(stdin.Fd())
	if !term.IsTerminal(fd) {
		return errors.New("hash-password requires an interactive terminal")
	}

	fmt.Fprint(stderr, "Password: ")
	password, err := term.ReadPassword(fd)
	fmt.Fprintln(stderr)
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}

	if len(password) == 0 {
		return errors.New("password must not be empty")
	}

	hash, err := bcrypt.GenerateFromPassword(password, cost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	fmt.Fprintln(stdout, string(hash))
	return nil
}

// sweepCheckSessions drops the provider sessions of checks that a killed
// process left behind. A check reserves its session for as long as it runs, and
// the reservation lives in that process, so a session it never got to drop
// becomes a resumable ghost coder at the next start. Nothing running answers to
// these names, a live check is always reserved and never listed.
func sweepCheckSessions(coders []*coder.Manager, workspace string) {
	for _, m := range coders {
		// A check that outlived the restart reserved its session again a moment
		// ago, and a cached snapshot from before that would offer it up here.
		m.Invalidate()
		for _, session := range m.Snapshot().Resumable {
			if !isStrayCheckSession(session, workspace) {
				continue
			}
			if _, err := m.DeleteResumable(session.SessionID); err != nil {
				log.Printf("assistant: a check session of %s stayed behind: %v", m.ID(), err)
				continue
			}
			log.Printf("assistant: dropped the check session %s a restart left behind", session.SessionID)
		}
		m.Invalidate()
	}
}
