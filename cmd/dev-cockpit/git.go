package main

import (
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/localapi"
	"github.com/spf13/cobra"
)

// gitProxyTimeout bounds one proxied call end to end. The server's own budget
// is minutes and breathes while a question of the askpass bridge stands, two
// more minutes per unanswered question, so this has to hold a push that asks
// a few times on a slow line. It is a safety net against a cockpit that never
// answers, not the working deadline.
const gitProxyTimeout = 30 * time.Minute

// newGitCommand is the cockpit's git proxy. A coder cannot answer an ssh
// passphrase prompt, and without an agent every remote operation asks one, so
// the command hands the whole line to the running cockpit: the operation runs
// in this process's own working directory, a question git or ssh asks reaches
// the browser as the app-wide dialog and as a notification, and the answer
// never touches this process. Output and exit code come back as git's own.
//
// It carries one flag and no more, which cockpit to reach. Where the call
// runs is the working directory and nothing else, and what that directory
// belongs to is the server's to answer: it holds the projects root and is the
// only place that value may live. A `--projects-dir` here would be a second
// copy of it, and the two disagreed the moment one of them was spelled
// `~/projects`.
func newGitCommand() *cobra.Command {
	stateDir := config.DefaultStateDir
	cmd := &cobra.Command{
		Use:   "git [--state-dir <dir>] <git arguments...>",
		Short: "Run git in the current directory through the running cockpit",
		Long: "Run one git command through the running cockpit, in the current directory, " +
			"the way plain git would. The arguments travel to git unchanged, " +
			"and output and exit code come back as git's own. The point of the detour: when " +
			"git or ssh asks a question, a passphrase, a credential, a host key confirmation, " +
			"the question appears as a dialog in the cockpit UI, phone included, and this " +
			"command waits until it is answered there. Use it for every git command that " +
			"reaches a remote, so a passphrase protected key works without an ssh agent; a " +
			"question nobody answers, or a cancelled one, fails the command with git's words. " +
			"The git subcommand comes first and its own options behind it; options of git " +
			"itself (-c, -C, --git-dir) are refused, they could point git somewhere other " +
			"than the directory the dialog names.",
		// Flag parsing is off and the one flag is read by hand, because
		// everything else on this line is git's and must reach the server
		// untouched. Left to cobra, an argument that merely looks like a flag
		// of ours ends the command with "unknown shorthand flag" — and that is
		// exactly the case that matters: `-c` and `-C` are what the server
		// refuses with a sentence saying why, and a coder reading cobra's
		// version instead would believe this command is broken.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
				return cmd.Help()
			}
			dir, rest, err := takeStateDir(stateDir, args)
			if err != nil {
				return err
			}
			code, err := runGitProxy(cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, rest)
			if err != nil {
				return err
			}
			if code != 0 {
				os.Exit(clampExit(code))
			}
			return nil
		},
	}
	// Registered for the help text alone; the value is read in RunE, see
	// DisableFlagParsing above.
	cmd.Flags().StringVar(&stateDir, "state-dir", stateDir, "directory for dev-cockpit state files")
	return cmd
}

// takeStateDir reads this command's one flag off the front of the line and
// answers the rest untouched. Only in front: everything from the first
// argument that is not this flag on belongs to git, `--state-dir` among a
// subcommand's own options included, which is git's to reject or accept.
func takeStateDir(fallback string, args []string) (dir string, rest []string, err error) {
	dir = fallback
	for len(args) > 0 {
		switch {
		case args[0] == "--state-dir":
			if len(args) < 2 {
				return "", nil, errors.New("--state-dir needs a directory.")
			}
			dir, args = args[1], args[2:]
		case strings.HasPrefix(args[0], "--state-dir="):
			dir, args = strings.TrimPrefix(args[0], "--state-dir="), args[1:]
		default:
			return dir, args, nil
		}
	}
	return dir, nil, nil
}

// runGitProxy sends the command line and this process's working directory to
// the running cockpit and hands back what git answered: both streams onto
// this process's own, and the exit code for the caller to end with. The error
// is everything that kept git from answering at all: no cockpit, a question
// that ran out or was cancelled, a line whose shape the server refuses, each
// in the sentence the server worded.
func runGitProxy(stdout, stderr io.Writer, stateDir string, args []string) (int, error) {
	// The raw working directory, as this process sees it. Where that is and
	// what it belongs to is the server's answer, never this command's.
	cwd, err := os.Getwd()
	if err != nil {
		return 0, err
	}
	client, err := localapi.Dial(stateDir)
	if err != nil {
		return 0, err
	}
	answer, err := client.PostJSON("/git", map[string]any{"cwd": cwd, "args": args}, gitProxyTimeout)
	if err != nil {
		return 0, err
	}
	out, errOut, code, ok := decodeGitAnswer(answer)
	if !ok {
		return 0, errors.New("The cockpit answered something this command cannot read.")
	}
	_, _ = stdout.Write(out)
	_, _ = stderr.Write(errOut)
	return code, nil
}

// clampExit keeps the exit code something a process can carry: the exec
// package answers -1 for a killed git, and a negative exit wraps around.
func clampExit(code int) int {
	if code < 0 || code > 255 {
		return 1
	}
	return code
}

// decodeGitAnswer reads the proxy route's answer: the exit code and both
// streams, base64 because git's output is bytes and not always UTF-8.
func decodeGitAnswer(answer map[string]any) (stdout, stderr []byte, code int, ok bool) {
	rawCode, ok := answer["exitCode"].(float64)
	if !ok {
		return nil, nil, 0, false
	}
	rawStdout, _ := answer["stdout"].(string)
	rawStderr, _ := answer["stderr"].(string)
	stdout, err := base64.StdEncoding.DecodeString(rawStdout)
	if err != nil {
		return nil, nil, 0, false
	}
	stderr, err = base64.StdEncoding.DecodeString(rawStderr)
	if err != nil {
		return nil, nil, 0, false
	}
	return stdout, stderr, int(rawCode), true
}
