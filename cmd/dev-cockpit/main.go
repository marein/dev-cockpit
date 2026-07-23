// Command dev-cockpit runs the tmux-backed developer cockpit.
package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/backup"
	"github.com/local/dev-cockpit/internal/clirun"
	"github.com/local/dev-cockpit/internal/coder"
	coderclaude "github.com/local/dev-cockpit/internal/coder/claude"
	codercopilot "github.com/local/dev-cockpit/internal/coder/copilot"
	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/notify"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/push"
	"github.com/local/dev-cockpit/internal/recent"
	"github.com/local/dev-cockpit/internal/restore"
	"github.com/local/dev-cockpit/internal/settings"
	"github.com/local/dev-cockpit/internal/shell"
	"github.com/local/dev-cockpit/internal/tmux"
	"github.com/local/dev-cockpit/internal/web"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
)

// version is the release tag, injected at build time via
// -ldflags "-X main.version=...". Empty/"dev" for non-release builds.
var version = "dev"

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

func main() {
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
	cmd.AddCommand(newServeCommand(), newHashPasswordCommand())
	return cmd
}

func newServeCommand() *cobra.Command {
	opts := serveOptions{
		Options: config.Options{
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
		},
	}
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
	registry := coder.NewRegistry(codercopilot.New(), coderclaude.New(notify.InboxDir(cfg.StateDir, "claude")))
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
	settingsStore := settings.New(filepath.Join(cfg.StateDir, "settings.json"))
	shells := shell.NewShells(cfg, tmuxClient, projectRepo, func() bool {
		return settingsStore.Get(shell.HistorySettingKey) == "on"
	})
	backups := backup.New(cfg.StateDir, cfg.ProjectsRoot, resolveVersion())

	notifier := notify.NewService(
		notify.StorePath(cfg.StateDir),
		notifyResolver(coders, shells, projectRepo, backups),
	)
	// The push channels subscribe before any watcher starts, so an inbox
	// backlog ingested right after boot cannot slip past them.
	pushService, err := push.NewService(cfg.StateDir)
	if err != nil {
		return fmt.Errorf("failed to initialize push channels: %w", err)
	}
	pushService.Start(notifier)

	// The startup pass runs before the watchers and the server, so restored
	// sessions are in place when the first page renders. Off by default, the
	// snapshot file itself is kept current regardless of the setting.
	restorer := restore.New(
		filepath.Join(cfg.StateDir, "terminal-restore.json"),
		func() bool { return settingsStore.Get(restore.SettingKey) == "on" },
		coders, shells, tmuxClient, notifier,
	)
	restorer.RunStartup()
	go restorer.RunPeriodic(30 * time.Second)

	// Restore has recreated its shells under their old ids by now, so the
	// startup reap keeps them and drops only the truly orphaned history files.
	shells.ReapHistory()
	go shells.RunHistoryReaper(10 * time.Minute)

	for _, c := range selected {
		go notifier.RunInbox(notify.InboxDir(cfg.StateDir, c.ID()), time.Second)
	}
	go shells.RunCommandWatch(3*time.Second, func(shellID string) {
		notifier.Add(shellID)
	})
	for _, m := range coders {
		if m.ID() != "copilot" {
			continue
		}
		if err := codercopilot.EnsureBeepSetting(); err != nil {
			log.Printf("copilot beep setting: %v", err)
		}
		go m.RunBellWatch(3*time.Second, func(targetID string) {
			notifier.Add(targetID)
		})
	}

	srv, err := web.NewServer(cfg, coders, shells, projectRepo, notifier, settingsStore, pushService, restorer, backups, resolveVersion())
	if err != nil {
		return fmt.Errorf("failed to initialize web server: %w", err)
	}
	if cfg.TLSCertFile != "" {
		log.Printf("listening on https://%s", cfg.HTTPAddr)
		return http.ListenAndServeTLS(cfg.HTTPAddr, cfg.TLSCertFile, cfg.TLSKeyFile, srv.Handler())
	}
	log.Printf("listening on http://%s", cfg.HTTPAddr)
	return http.ListenAndServe(cfg.HTTPAddr, srv.Handler())
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
func notifyResolver(coders []*coder.Manager, shells *shell.Shells, projects *project.Repository, backups *backup.Service) notify.Resolver {
	return func(targetID string) notify.TargetInfo {
		info := notify.TargetInfo{}
		if targetID == notify.BackupTarget {
			info.Name = "Backup"
			info.URL = "/settings/backup"
			// The notification fires right after a job finished, so the
			// newest finished entry is the one it is about.
			if b, ok := backups.LastFinished(); ok {
				if b.Done() {
					info.Title = fmt.Sprintf("Backup %q ready.", b.Name)
				} else {
					info.Title = fmt.Sprintf("Backup %q failed.", b.Name)
				}
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
					return info
				}
			}
			for _, stored := range snap.Resumable {
				if stored.SessionID == targetID {
					info.Name = stored.Name
					info.Project = projects.ProjectNameFor(stored.CWD)
					info.URL = "/coders/" + stored.SessionID
					return info
				}
			}
		}
		for _, sh := range shells.List() {
			if sh.Identifier == targetID {
				info.Name = sh.Name
				info.Project = projects.ProjectNameFor(sh.CWD)
				info.URL = "/shells/" + sh.Identifier
				return info
			}
		}
		return info
	}
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
