package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/marein/dev-cockpit/internal/coder/claude/statusline"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/spf13/cobra"
)

// syncClaudeStatusLine writes the rendered line again from the stored answer,
// like the managed skill: a state directory that came out of a backup, or one
// whose entries changed shape with an update, carries a line that matches this
// version.
func syncClaudeStatusLine(stateDir string, store *settings.Store) {
	if err := statusline.Sync(stateDir, store.Get(statusline.SettingKey)); err != nil {
		log.Printf("the claude status line could not be written: %v", err)
	}
}

// newClaudeCommand groups what claude sessions the cockpit starts run of it.
// Hidden like the docker group: it is not a user interface. Its names and
// flags stand in the --settings of every running claude session, so they never
// change.
func newClaudeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "claude",
		Short:  "Helpers the cockpit's claude sessions run",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return errors.New("command required")
		},
	}
	cmd.AddCommand(newClaudeStatusLineCommand())
	return cmd
}

// newClaudeStatusLineCommand draws the status line: claude's status JSON on
// stdin, the line on stdout. It never fails and never says anything on
// stderr, because claude shows stdout as the line and a person reads whatever
// lands beside it: a line it cannot draw is an empty one.
func newClaudeStatusLineCommand() *cobra.Command {
	stateDir := config.DefaultStateDir
	cmd := &cobra.Command{
		Use:   "status-line",
		Short: "Draw claude's status line from its status JSON on stdin",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			stdin, _ := io.ReadAll(cmd.InOrStdin())
			dir, err := filesystem.ExpandHome(stateDir)
			entries, ok := statusline.Load(dir)
			if err != nil || !ok {
				fmt.Fprintln(cmd.OutOrStdout())
				return
			}
			home, _ := os.UserHomeDir()
			env := statusline.Env{
				Now:      time.Now(),
				Home:     home,
				CacheDir: statusline.Dir(dir),
				UsageURL: statusline.UsageURL,
			}
			fmt.Fprint(cmd.OutOrStdout(), statusline.Render(context.Background(), entries, stdin, env))
		},
	}
	cmd.Flags().StringVar(&stateDir, "state-dir", stateDir, "the state directory of the cockpit whose line this draws")
	return cmd
}
