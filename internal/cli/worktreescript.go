package cli

import (
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/spf13/cobra"
)

const worktreeScriptPath = "/settings/projects/worktrees"

func newProjectWorktreeScriptShowCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "project-worktree-script-show",
		Short: "Print the worktree post script",
		Long: "Print the worktree post script from Settings › Projects, the one script every new " +
			"worktree runs, or say there is none, then one line with its directory, time bound and " +
			"environment. Reads only, changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := localapi.Dial(opts.stateDir, opts.assistantID)
			if err != nil {
				return err
			}
			answer, err := client.GetJSON(worktreeScriptPath, actionTimeout)
			if err != nil {
				return err
			}
			script, _ := answer["script"].(string)
			if script == "" {
				script = "there is no worktree post script\n"
			}
			_, err = io.WriteString(cmd.OutOrStdout(), script+project.PostScriptSummary()+"\n")
			return err
		},
	}
}

func newProjectWorktreeScriptSetCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "project-worktree-script-set [file]",
		Short: "Replace the worktree post script, once the user approves",
		Long: "Replace the whole worktree post script with the content of file, or of stdin without " +
			"one; read it, its directory, time bound and environment with " +
			"`project-worktree-script-show` first. The first line names the interpreter, a script " +
			"without it is refused; unless the user asks for another language, write a bash script " +
			"starting with #!/usr/bin/env bash. Empty content deletes the script. The write asks the " +
			"user first: the answer says `waits for the user's approval` at once, the user sees the " +
			"new content and the script changes when they approve. Either way a note lands in your " +
			"thread with the outcome; do not run it again while it waits. The user can turn the " +
			"question off with the Worktree post script approval under Settings › Assistants › " +
			"Approvals; you cannot. With the question off the script is written at once.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var content []byte
			var err error
			if len(args) == 1 {
				content, err = os.ReadFile(args[0])
			} else {
				content, err = io.ReadAll(cmd.InOrStdin())
			}
			if err != nil {
				return err
			}
			return runProjectWorktreeScriptSet(cmd.OutOrStdout(), *opts, string(content))
		},
	}
}

func runProjectWorktreeScriptSet(out io.Writer, opts inspectOptions, content string) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.PostForm(worktreeScriptPath, url.Values{"post_script": {content}}, actionTimeout)
	if err != nil {
		return err
	}
	if jsonBool(answer["pending"]) {
		_, err = io.WriteString(out, approvalLine(answer))
		return err
	}
	_, err = fmt.Fprintln(out, text(answer["saved"]))
	return err
}
