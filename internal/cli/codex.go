package cli

import (
	"errors"
	"os"

	codercodex "github.com/marein/dev-cockpit/internal/coder/codex"
	"github.com/spf13/cobra"
)

// newCodexCommand groups what the cockpit's codex runs need of its own
// binary. Hidden like the claude group: it is not a user interface.
func newCodexCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    codercodex.CompactHelper[0],
		Short:  "Helpers the cockpit's codex runs",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return errors.New("command required")
		},
	}
	cmd.AddCommand(newCodexSessionCompactCommand())
	return cmd
}

// newCodexSessionCompactCommand is the process a compaction of a codex
// assistant runs as, detached like a turn: its record on stdout is what the
// runner's parser reads, see codercodex.CompactSession.
func newCodexSessionCompactCommand() *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:           codercodex.CompactHelper[1] + " [--model=<model>] -- <session>",
		Short:         "Compact one codex session and print how it went",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		Run: func(cmd *cobra.Command, args []string) {
			os.Exit(codercodex.CompactSession(args[0], model, os.Stdout))
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "the model the compaction runs on")
	return cmd
}
