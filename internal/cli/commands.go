package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/detach"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
)

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
		Run: func(cmd *cobra.Command, args []string) {
			if !askpass.Helper(args, os.Stdout) {
				os.Exit(1)
			}
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
		newAssistantsCommand(opts), newAssistantCommandShow(opts), newNewAssistantCommand(opts), newSendPromptAssistantCommand(opts), newDeleteAssistantCommand(opts),
		newJobsCommand(opts), newJobCommand(opts),
		newNotificationsCommand(opts),
		newTriggerNewCommand(opts), newTriggerListCommand(opts), newTriggerEditCommand(opts), newTriggerDeleteCommand(opts),
		newTimezoneGetCommand(opts), newTimezoneSetCommand(opts),
		newCostShowCommand(opts),
		newModelListCommand(opts), newAssistantModelsGetCommand(opts), newAssistantModelsSetCommand(opts),
		newProjectCommand(opts), newDeleteProjectCommand(opts), newProjectWorktreeNewCommand(opts), newProjectWorktreeListCommand(opts),
		newProjectWorktreeScriptShowCommand(opts), newProjectWorktreeScriptSetCommand(opts),
		newComposeListCommand(opts), newComposeStartCommand(opts), newComposeShowCommand(opts), newComposeStopCommand(opts),
		newLineCommentListCommand(opts), newLineCommentAddCommand(opts), newLineCommentResolveCommand(opts), newLineCommentRemoveCommand(opts),
		newForgeShowCommand(opts), newForgePipelineRetryCommand(opts), newForgeChangeNewCommand(opts), newForgeCommentPublishCommand(opts),
		newForgeThreadReplyCommand(opts), newForgeThreadResolveCommand(opts),
		newOutputCommand(opts), newToolApprovalServeCommand(opts),
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

// newChildGuardCommand is the guard clirun.DiesWithParent starts beside a
// child server where the kernel has no parent death signal.
func newChildGuardCommand() *cobra.Command {
	return &cobra.Command{
		Use:    clirun.GuardCommand[0],
		Short:  "End a child server's process group with its starter",
		Hidden: true,
		Args:   cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			os.Exit(clirun.Guard())
		},
	}
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
