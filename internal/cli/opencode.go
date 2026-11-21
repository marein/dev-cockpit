package cli

import (
	"errors"
	"os"

	coderopencode "github.com/marein/dev-cockpit/internal/coder/opencode"
	"github.com/spf13/cobra"
)

// newOpencodeCommand groups what the cockpit's opencode runs need of its own
// binary. Hidden like the claude group: it is not a user interface.
func newOpencodeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    coderopencode.CompactHelper[0],
		Short:  "Helpers the cockpit's opencode runs",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return errors.New("command required")
		},
	}
	cmd.AddCommand(newOpencodeSessionCompactCommand())
	return cmd
}

// newOpencodeSessionCompactCommand is the process a compaction of an opencode
// assistant runs as, detached like a turn: its record on stdout is what the
// runner's parser reads, see coderopencode.CompactSession.
func newOpencodeSessionCompactCommand() *cobra.Command {
	return &cobra.Command{
		Use:           coderopencode.CompactHelper[1] + " -- <session>",
		Short:         "Compact one opencode session and print how it went",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		Run: func(cmd *cobra.Command, args []string) {
			os.Exit(coderopencode.CompactSession(args[0], os.Stdout))
		},
	}
}
