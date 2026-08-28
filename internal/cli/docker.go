package cli

import (
	"errors"

	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/spf13/cobra"
)

// newDockerCommand groups the helpers the cockpit's docker terminals run.
// Hidden like the assistant group: it is not a user interface, the log shells
// the cockpit spawns pipe through it, and typed explicitly it works.
func newDockerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "docker",
		Short:  "Helpers the cockpit's docker terminals run",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return errors.New("command required")
		},
	}
	cmd.AddCommand(newDockerLogFormatterCommand())
	return cmd
}

// newDockerLogFormatterCommand makes docker logs readable: it reads raw log
// lines on stdin and writes them formatted to stdout, line by line so a
// followed log streams live. Compose prefixed lines get a stable tint per
// service, every line a severity gutter, and with --grep only the matching
// lines pass, plus the context around them, the matches inverted.
func newDockerLogFormatterCommand() *cobra.Command {
	pattern := ""
	contextLines := 2
	cmd := &cobra.Command{
		Use:   "log-formatter",
		Short: "Format docker log lines from stdin",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return docker.FormatLogs(cmd.InOrStdin(), cmd.OutOrStdout(), pattern, contextLines)
		},
	}
	cmd.Flags().StringVar(&pattern, "grep", "", "case insensitive regex, only matching lines pass, with context around them")
	cmd.Flags().IntVar(&contextLines, "context", contextLines, "context lines around a --grep match")
	return cmd
}
