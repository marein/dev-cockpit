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

	"github.com/marein/dev-cockpit/internal/activity"
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

// newAssistantCommand groups everything the cockpit's own assistants run. They
// share the two directory flags, because every one of them has to reach the same
// cockpit: a machine can run several, and a command that read the default state
// directory would answer about the wrong one. The whole group is hidden from
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
	cmd.PersistentFlags().StringVar(&opts.assistantID, "as", "", "the assistant running this command, its id")
	// --as names which assistant is calling, and it sits on the group next to
	// the two directories because it is the same kind of thing: which cockpit,
	// and who inside it. Every assistant has instructions of its own now, and
	// they spell the flag into every command they list, the way they spell the
	// directories; a flag written into the instructions is not one a model has
	// to remember, it is copied with the command. Several assistants share this
	// group, so the id has to travel with the call and not with the process:
	// what a turn inherits is inherited by every shell below it, while the flag
	// stands on exactly the call it belongs to, readable in every log line. It
	// is --as and not --assistant, because job-list already carries --assistant
	// with another meaning, whose jobs to list. What needs it refuses without
	// it, see assistantCaller in the web package.
	// The names put the object first and the verb last, so the flat help list
	// groups by object on its own: every coder- command stands together, then
	// the assistants, the jobs, the projects.
	cmd.AddCommand(
		newStatusCommand(opts),
		newCoderCommand(opts), newActivityCommand(opts),
		newSendCommand(opts), newKeysCommand(opts),
		newSteerCommand(opts), newReleaseCommand(opts),
		newResumeCoderCommand(opts), newStopCoderCommand(opts), newDeleteCoderCommand(opts),
		newAssistantsCommand(opts), newAssistantCommandShow(opts), newDeleteAssistantCommand(opts),
		newJobsCommand(opts), newJobCommand(opts),
		newNotificationsCommand(opts),
		newTriggerNewCommand(opts), newTriggerListCommand(opts), newTriggerEditCommand(opts), newTriggerDeleteCommand(opts),
		newTimezoneGetCommand(opts), newTimezoneSetCommand(opts),
		newModelListCommand(opts), newAssistantModelsGetCommand(opts), newAssistantModelsSetCommand(opts),
		newProjectCommand(opts), newDeleteProjectCommand(opts),
		newComposeListCommand(opts), newComposeStartCommand(opts), newComposeShowCommand(opts), newComposeStopCommand(opts),
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
	// The settings store stands before the coders: every coder's model
	// repository keeps its added names in it, and the model defaults the
	// managers and the assistant read on every start come out of it.
	settingsStore := settings.New(filepath.Join(cfg.StateDir, "settings.json"))
	registry := coder.NewRegistry(codercopilot.New(settingsStore), coderclaude.New(notify.InboxDir(cfg.StateDir, "claude"), settingsStore), coderopencode.New(notify.InboxDir(cfg.StateDir, "opencode"), settingsStore))
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
	executable := runningExecutable()
	conversations, assistantService, err := assistant.New(cfg.StateDir, assistantCoders{coders: selected, store: settingsStore}, assistant.Cockpit{
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
		// How many checks may run at once is a house rule, read again before
		// every check so the setting applies without a restart.
		func() int { return web.ConcurrentChecks(settingsStore) },
	)
	// The working marks. The tracker holds the shelves, everything below only
	// feeds it: the record watchers report each coder's own account of its
	// turn (RunTurnWatch further down), the turn-end hooks and bells close it
	// (the SetSignal wrap), opencode's plugin opens it (SetTurnOpen), and the
	// session watchers deliver the raw screen movement the fallback shelf
	// reads. Every change goes out as one activity snapshot on the event bus,
	// the shape the icons decorate from.
	tracker := activity.NewTracker()
	tracker.SetOnChange(func(ids []string) {
		bus.Publish(eventbus.Event{Type: "activity", Data: map[string]any{"targets": ids}})
	})
	go tracker.Run(time.Second)
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
	srv, err := web.NewServer(cfg, coders, shells, conversations, assistantService, watcher, projectRepo, notifier, tracker, settingsStore, pushService, restorer, backups, dockerService, intel, voiceService, serves, bus, resolveVersion(), updateFeedURL, updateFeedFormat, version == "dev")
	if err != nil {
		return fmt.Errorf("failed to initialize web server: %w", err)
	}
	// A finished answer is normal cockpit news, and a change refreshes the open
	// lists. Neither hook may run while the service holds its lock, both are
	// called after it is released.
	conversations.SetHooks(srv.PublishConversations, notifier.Add)
	conversations.SetRenderer(markdown.RenderGFM)
	// The same raw signal closes the working mark's turn: a coder that
	// reports has stopped to say so, whether it finished or waits on a
	// question. The record watcher reopens it the moment the record moves
	// again.
	notifier.SetSignal(func(targetID string) {
		watcher.Handle(targetID)
		tracker.SetTurn(targetID, false, false, time.Now())
	})
	// The same signal, with the hook name it arrived under, is a coder event the
	// assistants may react to: a Notification hook is a coder that asks a
	// question or wants a permission, every other name is a turn that ended. The
	// headline names the coder the way a notification does.
	events := conversations.Events()
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
	// And the opposite fact from the one coder whose record arrives as push
	// events instead of a readable file.
	notifier.SetTurnOpen(func(targetID string) {
		tracker.SetTurn(targetID, true, false, time.Now())
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
	// A job is looked at even when nothing reports: a coder that stopped, that
	// ran out of room to think in or that waits on a question sends nothing at
	// all, and that is exactly the job that needs looking at. The pass reads what
	// the coder already wrote down and only buys a check when the picture says it
	// is worth one. It also ends the jobs whose time or budget is up, because a
	// job that goes quiet has no signal left to write that report with.
	go watcher.RunHeartbeat(0)
	go events.Run(0)

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
	// Every coder feeds the working marks with two watches. The turn watch
	// follows each session's own record and is the account the tracker
	// trusts first; its gone reports are what drop a dead session's mark.
	// The session watch delivers the raw screen output the fallback shelf
	// reads; only copilot's also listens for the bell, its turn-end signal,
	// where claude and opencode report through the inbox instead.
	//
	// The turn watch carries one thing that is not about working marks: it
	// reads the snapshot every second, and the snapshot holds the session
	// names the CLIs write themselves. A rename inside a coder therefore
	// only has to be handed to the same announcement every other terminal
	// change makes, and every surface follows it without a page of its own
	// asking anybody. The event names the project and nothing else, the way
	// a start or a stop does, so the moved name itself travels no further
	// than here: every surface pulls its own fragment and reads it there.
	onRenamed := func(id, name, cwd string) { srv.PublishTerminals(projectRepo.ProjectNameFor(cwd)) }
	for _, m := range coders {
		var onBell func(targetID string)
		if m.ID() == "copilot" {
			if err := codercopilot.EnsureBeepSetting(); err != nil {
				log.Printf("copilot beep setting: %v", err)
			}
			// A bell has no hook name; it rings for a turn that ended and
			// for a question alike, and it is read as the former.
			onBell = func(targetID string) {
				notifier.Event(targetID, "Bell")
			}
		}
		// A session entering the running set brings its coder's chosen
		// policy along, so the tracker never guesses what applies to it.
		profile := m.Coder().ActivityProfile()
		policy := activity.Policy{
			OpenTurnCap:        profile.OpenTurnCap,
			MovementStartGrace: profile.MovementStartGrace,
		}
		onSeen := func(id string, startedAt time.Time) { tracker.Configure(id, policy, startedAt) }
		go m.RunTurnWatch(time.Second, onSeen, tracker.SetTurn, tracker.Forget, onRenamed)
		go m.RunSessionWatch(3*time.Second, tracker.Output, onBell)
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
			info.Title, info.Detail = gitPromptNews(actionName)
			return info
		}
		if notify.IsApprovalTarget(targetID) {
			// The entry is written while the question stands, so the run's
			// standing question is the one it is about: who asks, what for
			// and where, and the run page it leads to. A question that
			// vanished in between keeps the generic words.
			key := askpass.ApprovalKey(notify.ApprovalTargetRun(targetID))
			info.Name = "Approval"
			info.URL = "/projects"
			who, what, project := "", "", ""
			for _, q := range askBroker.Questions() {
				if q.Key == key {
					who, what, project = q.Assistant, q.Action, q.Project
					if q.URL != "" {
						info.URL = q.URL
					}
					break
				}
			}
			info.Project = project
			info.Title, info.Detail = approvalNews(who, what, project)
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
		// The assistants come first: their provider sessions are hidden from
		// the coder lists, and they are the only surface with a fixed home, so
		// the lookup is cheap and cannot be shadowed by a coder of the same id.
		for _, entry := range conversations.List() {
			if entry.ID != targetID {
				continue
			}
			// One bell for all of them. The name is what this assistant is
			// called, cut to a label, and it opens the line below the title.
			// The link is that assistant's page, and the fragment names the
			// answer the page scrolls to. An assistant with nothing in it goes
			// through the same wording with an empty message, so there is one
			// sentence for the branch and not two.
			info.Name = assistantNewsName(entry)
			info.URL = "/assistants/" + entry.ID
			m, ok := conversations.LastAnswer(entry.ID)
			if ok {
				info.URL += "#message-" + m.ID
			}
			info.Title, info.Detail = assistantNews(info.Name, m)
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
					info.Title, info.Detail = coderNews(info.Name)
					return info
				}
			}
			for _, stored := range snap.Resumable {
				if stored.SessionID == targetID {
					info.Name = stored.Name
					info.Project = projects.ProjectNameFor(stored.CWD)
					info.URL = "/coders/" + stored.SessionID
					info.Title, info.Detail = coderNews(info.Name)
					return info
				}
			}
		}
		for _, sh := range shells.List() {
			if sh.Identifier == targetID {
				info.Name = sh.Name
				info.Project = projects.ProjectNameFor(sh.CWD)
				info.URL = "/shells/" + sh.Identifier
				info.Title, info.Detail = shellNews(info.Name)
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
// what happened, the line below it says what it happened to and, where
// something was written, what that was. The wording of all of them stands
// here, next to each other, because that is the only way it stays one
// wording. internal/notify writes the entries and classifies nothing, so
// nothing of this belongs there.

// coderNews is what a coder's signal says: that there is news, and the coder
// stands below it. Whether the coder finished its turn, asks a question or
// waits for a permission is what this cockpit deliberately does not classify,
// see internal/notify.
func coderNews(name string) (title, detail string) {
	return "Coder has news.", newsDetail(name, "")
}

// shellNews is what a shell's signal says. A shell reports when a foreground
// command ended, so that is what the title says, with the shell below it.
func shellNews(name string) (title, detail string) {
	return "Command finished.", newsDetail(name, "")
}

// backupNews is what a finished backup job says, the archive below how it
// went.
func backupNews(name string, ok bool) (title, detail string) {
	title = "Backup failed."
	if ok {
		title = "Backup ready."
	}
	return title, newsDetail(name, "")
}

// gitPromptNews is what a standing askpass question says: git is waiting for
// an answer, and the action it is waiting in stands below that. The entry is
// marked read again the moment the question no longer stands, however it went,
// so the title may speak in the present.
func gitPromptNews(action string) (title, detail string) {
	return "Git asks a question.", newsDetail(action, "")
}

// approvalNews is what a standing approval says: an assistant asks, and below
// it which one, and the command and the project it wants to run it in. The
// assistant's name is already a label where it is written (assistantNewsName),
// the command's label is what the settings page holds.
func approvalNews(who, action, project string) (title, detail string) {
	if who == "" {
		who = assistant.Name
	}
	if action == "" {
		action = "a compose command"
	}
	if project != "" {
		action += " in " + project
	}
	return "Assistant asks approval.", newsDetail(who, action)
}

// composeNews is what a finished docker compose run says: how it went, and
// the command that ran below it.
func composeNews(run docker.RunView) (title, detail string) {
	title = "Compose finished."
	if run.Failure != "" {
		title = "Compose failed."
	}
	name := run.Action
	if name == "" {
		name = "compose"
	}
	return title, newsDetail(name, "")
}

// assistantNewsName is the name a notification is rung under: what this
// assistant is called, and the surface's own word for one that has not been
// named yet. That name is the conversation's title, which is written from the
// first message somebody typed and is a whole paragraph as often as not, so it
// goes through the same cut a coder's session title does. A paragraph is
// unusable in every surface that shows it: the head of a notification's lower
// line, and the "Something new in ..." an unresolved entry falls back to.
func assistantNewsName(entry assistant.Summary) string {
	if title := strings.TrimSpace(entry.Title); title != "" && title != assistant.DefaultTitle {
		return coder.ShortTitle(title)
	}
	return assistant.Name
}

// assistantNews is what a notification about one assistant says, and it says
// it in two lines that divide the work. The title is the kind alone, one of
// ten fixed sentences: the first word tells a job from a compose run from a
// trigger from an answer, and the second tells the endings of each apart. The line below it
// names which one it was and then carries an excerpt of what was written, see
// newsDetail: an entry that only said that something happened would send the
// user into the thread to find out what.
//
// The identifier is the most precise thing there is: the job a report closed,
// the headline a reaction fired under, which is the trigger's name where the
// user gave it one. Where nothing narrower exists, an answer somebody asked
// for and a report from before the note carried a name, the assistant it came
// from stands there. That is the same rule and not an exception, and it lands
// the name in exactly the case where several assistants could be confused.
//
// A report's excerpt does not repeat the verdict its title already says: the
// report is stored without it, parseVerdict cuts the word off the answer
// before it is ever written down.
//
// The job's name comes from the message's own note, never from a lookup: this
// resolver runs before the job store exists, and a terminal that is steered
// again would hand back the successor's job.
func assistantNews(who string, m assistant.Message) (title, detail string) {
	ident := ""
	if m.Note != nil && m.Note.Source == assistant.NoteCheck {
		// A job ends in one of three states and the title says which one, in
		// the words of the JobState it was closed with.
		switch m.Note.Verdict {
		case string(assistant.VerdictDone):
			title, ident = "Job done.", m.Note.Name
		case string(assistant.VerdictBlocked):
			title, ident = "Job blocked.", m.Note.Name
		case string(assistant.VerdictExpired):
			title, ident = "Job expired.", m.Note.Name
		}
	}
	if m.Note != nil && m.Note.Source == assistant.NoteCompose {
		// A compose run of the assistant ends in one of three ways, in the
		// words of the note's own verdict, and the command names it.
		switch m.Note.Verdict {
		case assistant.ComposeKindDone:
			title = "Compose done."
		case assistant.ComposeKindFailed:
			title = "Compose failed."
		case assistant.ComposeKindDeclined:
			title = "Compose declined."
		}
		ident = m.Note.Name
	}
	unfinished := m.State == assistant.StateFailed || m.State == assistant.StateInterrupted
	// Nobody asked for this answer, so the title says a trigger fired rather
	// than that the assistant answered, and it says it for a turn that broke
	// off too: what is on the screen has to be recognizable as a reaction
	// either way.
	if title == "" && m.Origin != nil && m.Origin.Source == assistant.NoteEvent {
		title, ident = "Trigger fired.", m.Origin.Headline
		if unfinished {
			// The word the thread and the code use for a turn that stopped
			// before it was done.
			title = "Trigger broke off."
		}
	}
	if title == "" {
		title = "Answer ready."
		if unfinished {
			// Whatever was written before it broke off is still the best line
			// about what the turn was doing.
			title = "Answer broke off."
		}
	}
	// Nothing narrower than the assistant itself: an answer somebody asked
	// for, and a report from before the note carried a name.
	if strings.TrimSpace(ident) == "" {
		ident = who
	}
	return title, newsDetail(ident, answerExcerpt(m.Content))
}

// newsTitleRunes is the room a title has. Every title above is a fixed
// sentence, nothing is composed out of user text and nothing is ever cut, so
// this is no budget the code spends: it is the bound the wording is written
// to and the one the test pins. It is the narrowest of the three surfaces a
// title surfaces in, a phone's push, where iOS gives the title one line and
// writes the app's own name on the second, and a lock screen of this
// cockpit's own pushes ran out at exactly 32 runes with the system's mark
// among them. The bell's list and a toast hold 40 to 46, so a sentence that
// fits the push stands whole in all three. The wider two decided this line
// while it still ended in an identifier that could be shortened; a sentence
// cannot be shortened, so the narrowest surface decides it now.
const newsTitleRunes = 32

// newsDetail writes the lower line: the identifier of what this happened to,
// then what was written where there is a text of it. Every kind builds it,
// which is what makes one line read like the next.
//
// The identifier opens this line instead of closing the title because a phone
// gives the title one line and the body three or four: a name is read whole
// down here and cut up there, and what a push cuts off the end of this line
// is the tail of an excerpt, by construction the cheapest part of it. The
// kinds that write no text stop after the identifier, so their line is the
// name alone.
//
// Nothing is cut here. Every identifier is already a label where it is
// written: a coder's title through coder.ShortTitle, an assistant's the same
// way through assistantNewsName, a trigger's name bounded where it is typed.
func newsDetail(ident, excerpt string) string {
	ident = oneLine(ident)
	switch {
	case ident == "":
		return excerpt
	case excerpt == "":
		return ident
	}
	return ident + ": " + excerpt
}

// oneLine folds every line break and indent into one space: a title and an
// excerpt land on a single line wherever they surface.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// answerExcerpt turns an answer into the line a notification shows. It is the
// same preview the thread folds a note and a pushed answer under
// (assistant.PreviewRunes), so what brought a reader here and what stands in
// front of them when they arrive are the same words.
func answerExcerpt(content string) string {
	line, _ := markdown.Excerpt(content, assistant.PreviewRunes)
	return line
}

// assistantCoders adapts the installed coders to what the assistant needs,
// keeping internal/assistant free of any dependency on internal/coder.
// assistantCoders hands the assistant the coders that can answer a turn, each
// with the reading of its stored model defaults, the purpose fallback behind
// an assistant's own pick, read fresh on every turn out of the settings store.
type assistantCoders struct {
	coders []coder.Coder
	store  *settings.Store
}

func (c assistantCoders) Available() []assistant.CoderInfo {
	out := make([]assistant.CoderInfo, 0, len(c.coders))
	for _, co := range c.coders {
		runner := coder.AssistantRunnerFor(co)
		if runner == nil {
			continue
		}
		id := co.ID()
		out = append(out, assistant.CoderInfo{ID: id, Label: render.CoderLabel(id), Runner: runner, Defaults: func() assistant.ModelDefaults {
			return assistant.ModelDefaultsFor(c.store, id)
		}})
	}
	return out
}

// isStrayCheckSession decides what the startup sweep deletes. The name alone is
// user text, somebody may call a coder "cockpit check: whatever", and deleting
// it would take that coder's transcript with it. A check always runs in the
// workspace of the assistant whose job it is, so both have to hold.
func isStrayCheckSession(session coder.Session, ownWorkdir func(string) bool) bool {
	return assistant.IsCheckSession(session.Name) && ownWorkdir(session.CWD)
}

// coderSessions lets a check ask the coder of a job what that session last did.
// Which coder answers comes from the job, and how it answers is the coder's
// business: one that keeps a transcript reads it, one that keeps none falls
// back to the terminal picture. A job whose coder id is not among the installed
// ones (an old job, a coder that was removed) is offered to each of them, and
// the one that owns the session answers.
type coderSessions struct{ coders []*coder.Manager }

// toAssistantActivity maps the coder reading onto the assistant's own twin
// type, field by field on purpose: the two packages stay uncoupled, and the
// coder side may grow fields (the tool call phase) the job checks never read.
func toAssistantActivity(a coder.Activity) assistant.Activity {
	return assistant.Activity{Text: a.Text, Finished: a.Finished, Screen: a.Screen}
}

func (c coderSessions) Activity(coderID, terminal string) (assistant.Activity, error) {
	if coderID != "" {
		for _, m := range c.coders {
			if m.ID() == coderID {
				activity, err := m.Activity(terminal, 0, coder.ActivityBudget)
				return toAssistantActivity(activity), err
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
			return toAssistantActivity(activity), nil
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
// these names, a live check is always reserved and never listed. ownWorkdir
// says whether a directory is an assistant's workspace, the place every check
// runs in.
func sweepCheckSessions(coders []*coder.Manager, ownWorkdir func(string) bool) {
	for _, m := range coders {
		// A check that outlived the restart reserved its session again a moment
		// ago, and a cached snapshot from before that would offer it up here.
		m.Invalidate()
		for _, session := range m.Snapshot().Resumable {
			if !isStrayCheckSession(session, ownWorkdir) {
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
