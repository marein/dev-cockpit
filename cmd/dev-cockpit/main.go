// Command dev-cockpit runs the developer cockpit.
package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/local/dev-cockpit/internal/clirun"
	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/provider"
	providerclaude "github.com/local/dev-cockpit/internal/provider/claude"
	providercopilot "github.com/local/dev-cockpit/internal/provider/copilot"
	"github.com/local/dev-cockpit/internal/session"
	"github.com/local/dev-cockpit/internal/term"
	"github.com/local/dev-cockpit/internal/web"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
	xterm "golang.org/x/term"
)

type serveOptions struct {
	providerID string
	config.Options
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
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return errors.New("command required")
		},
	}
	cmd.AddCommand(newServeCommand(), newHashPasswordCommand(), newSessionAgentCommand(), newAttachCommand())
	return cmd
}

func newServeCommand() *cobra.Command {
	opts := serveOptions{
		providerID: config.DefaultProvider,
		Options: config.Options{
			HTTPAddr:           config.DefaultHTTPAddr,
			ProjectsDir:        config.DefaultProjectsDir,
			AuthUsername:       config.DefaultAuthUsername,
			AuthPasswordHash:   config.DefaultAuthPasswordHash,
			SessionCookieName:  config.DefaultSessionCookieName,
			SessionCookieKey:   config.DefaultSessionCookieKey,
			TrustedProxies:     config.DefaultTrustedProxies,
			TLSCertFile:        config.DefaultTLSCertFile,
			TLSKeyFile:         config.DefaultTLSKeyFile,
			ProjectWorkers:     config.DefaultProjectWorkers,
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
	flags.StringVar(&opts.providerID, "provider", opts.providerID, "coder provider")
	flags.StringVar(&opts.HTTPAddr, "addr", opts.HTTPAddr, "HTTP address")
	flags.StringVar(&opts.ProjectsDir, "projects-dir", opts.ProjectsDir, "projects root directory")
	flags.StringVar(&opts.AuthUsername, "auth-user", opts.AuthUsername, "auth username")
	flags.StringVar(&opts.AuthPasswordHash, "auth-password-hash", opts.AuthPasswordHash, "bcrypt hash for auth password")
	flags.StringVar(&opts.SessionCookieName, "session-cookie-name", opts.SessionCookieName, "session cookie name")
	flags.StringVar(&opts.SessionCookieKey, "session-cookie-key", opts.SessionCookieKey, "session cookie signing key")
	flags.StringVar(&opts.TrustedProxies, "trusted-proxies", opts.TrustedProxies, "comma-separated trusted proxy IPs or CIDRs")
	flags.StringVar(&opts.TLSCertFile, "tls-cert-file", opts.TLSCertFile, "TLS certificate file for HTTPS")
	flags.StringVar(&opts.TLSKeyFile, "tls-key-file", opts.TLSKeyFile, "TLS private key file for HTTPS")
	flags.IntVar(&opts.ProjectWorkers, "project-metadata-workers", opts.ProjectWorkers, "maximum concurrent project metadata workers")
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

func newSessionAgentCommand() *cobra.Command {
	var cfg term.AgentConfig
	var env []string
	cmd := &cobra.Command{
		Use:    "session-agent",
		Short:  "Run a session agent (internal; spawned by serve)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Env = parseEnv(env)
			return term.RunAgent(cfg)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&cfg.Provider, "provider", "", "owning provider id")
	flags.StringVar(&cfg.Key, "key", "", "session key")
	flags.StringVar(&cfg.Workdir, "workdir", "", "working directory")
	flags.StringVar(&cfg.Command, "command", "", "shell command to run in the PTY")
	flags.StringArrayVar(&env, "env", nil, "extra environment KEY=VALUE for the program")
	return cmd
}

func newAttachCommand() *cobra.Command {
	providerID := config.DefaultProvider
	cmd := &cobra.Command{
		Use:   "attach <session-key>",
		Short: "Attach the local terminal to a running session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return term.Attach(providerID, args[0])
		},
	}
	cmd.Flags().StringVar(&providerID, "provider", providerID, "coder provider")
	return cmd
}

func parseEnv(pairs []string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	out := make(map[string]string, len(pairs))
	for _, kv := range pairs {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

func runServe(opts serveOptions) error {
	cfg, err := config.Load(opts.Options)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if _, err := bcrypt.Cost([]byte(cfg.AuthPasswordHash)); err != nil {
		return fmt.Errorf("invalid --auth-password-hash: %w", err)
	}
	projectRepo := project.NewRepository(cfg.ProjectsRoot, cfg.ProjectMetadataConcurrency)
	registry := provider.NewRegistry(providercopilot.New(), providerclaude.New())
	selectedProvider := registry.ByID(opts.providerID)
	if selectedProvider == nil {
		return fmt.Errorf("unknown provider %q (available: %s)", opts.providerID, strings.Join(registry.IDs(), ", "))
	}
	if missing := clirun.MissingTools(selectedProvider.RequiredTools()); len(missing) > 0 {
		return fmt.Errorf("missing CLI tools: %v", missing)
	}

	termClient, err := term.NewClient(selectedProvider.ID())
	if err != nil {
		return fmt.Errorf("failed to initialize session runtime: %w", err)
	}
	termClient.Reap() // drop socket/pid files left by agents that died uncleanly

	sessions := session.NewSessions(cfg, termClient, selectedProvider, projectRepo)

	srv, err := web.NewServer(cfg, selectedProvider, sessions, projectRepo)
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

func runHashPassword(stdin *os.File, stdout, stderr io.Writer, cost int) error {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		return fmt.Errorf("bcrypt cost must be between %d and %d", bcrypt.MinCost, bcrypt.MaxCost)
	}
	fd := int(stdin.Fd())
	if !xterm.IsTerminal(fd) {
		return errors.New("hash-password requires an interactive terminal")
	}

	fmt.Fprint(stderr, "Password: ")
	password, err := xterm.ReadPassword(fd)
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
