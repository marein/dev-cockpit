package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ExecResult is one proxied git call's answer, one to one: both streams as
// git wrote them, capped like every call of this package, and the exit code
// git ended with. A killed process answers the exec package's -1; the caller
// decides what to exit with for a code no shell could carry.
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Exec runs one git command line as it was typed, in the repository
// directory, for the cockpit's git proxy (`dev-cockpit git`). The arguments
// travel unchanged, nothing is injected in front of them, and output and exit
// code travel back as they came: an exit code, whatever it is, is git's
// answer and no error. What stays on is the safety net every call of this
// package runs under: no shell, the fail-fast prompt environment unless a
// bridge is attached (WithPrompt), the protocol whitelist, the breathing
// deadline while a question stands, and the group kill with the bounded pipe
// wait. The budget is the remote one, because push and pull are what the
// proxy exists for.
//
// The error is the runner's own no-answer case (ErrNoAnswer: deadline,
// dropped call, a process that never ran) and never a git refusal.
func (r *Repo) Exec(ctx context.Context, args []string) (ExecResult, error) {
	w := *r
	w.timeout = remoteTimeout
	res, err := w.exec(ctx, Subcommand(args), args, maxOutput)
	if err != nil {
		return ExecResult{}, err
	}
	return ExecResult{Stdout: res.stdout, Stderr: res.stderr, ExitCode: res.exitCode}, nil
}

// Subcommand is the word a proxied command line is about: its first argument
// when that is a subcommand, git itself otherwise. The failure sentences carry
// it, and the proxy's route names its askpass action with it, so the dialog
// says "push" and not the whole line.
//
// It reads the first argument and nothing behind it. Walking past an option to
// find a word further back cannot tell an option from an option's value, so
// `-c core.sshCommand=/tmp/x push` would name the action after the value: that
// word is what the dialog shows above ssh's line as this server's own truth,
// the one line there that is not the caller's, and it may not be a string the
// caller chose. CheckProxyArgs is what keeps a proxied line in that shape; this
// only falls back for a line that never went through it.
func Subcommand(args []string) string {
	if len(args) > 0 && isSubcommandWord(args[0]) {
		return args[0]
	}
	return "git"
}

// isSubcommandWord reports whether an argument is a plain git subcommand:
// lower case letters, and digits or dashes behind the first one. Every name
// git carries fits it, an option never does, and neither does anything that
// would read as something else in a dialog.
func isSubcommandWord(arg string) bool {
	if arg == "" {
		return false
	}
	for i, r := range arg {
		switch {
		case r >= 'a' && r <= 'z':
		case i > 0 && (r >= '0' && r <= '9' || r == '-'):
		default:
			return false
		}
	}
	return true
}

// programOptions are the transport's own program, on the operations this
// proxy exists for. They reach a local process through an ordinary looking
// remote: the allowed transports include file, so `--upload-pack=/tmp/x`
// against a path on this disk runs /tmp/x here, with the action's bridge
// environment.
var programOptions = []string{"--upload-pack", "--receive-pack"}

// CheckProxyArgs is the one rule a proxied command line has to keep: the
// caller's arguments may describe an operation, and they may never name a
// program for this server to run.
//
// It holds through one shape. The git subcommand comes first, its own options
// behind it, and the options of git itself, everything that stands in front of
// a subcommand, are not proxied at all. That list is where the whole danger
// sits and none of it is needed here: `-c core.sshCommand=…` and
// `-c credential.helper=…` point git at a program of the caller's choosing,
// which then inherits the askpass environment and can ask the person in the
// browser for the passphrase itself; `--exec-path` moves where git looks for
// its own subcommands; `-C` and `--git-dir` move the call out of the working
// copy the dialog names. Refusing the position instead of the option names is
// what makes that complete: every one of them is only valid in front of the
// subcommand, so a first argument that is a plain subcommand word leaves none
// of them a place to stand.
//
// Behind the subcommand it names programOptions, the transport's own program
// on the operations this proxy is for, and it names nothing else: what a
// subcommand does is the subcommand's, and a list trying to enumerate every
// git option that ends in a command would be a list that is never complete.
//
// That is the honest bound of this function. It is what the cockpit itself
// accepts, and it is no wall between the cockpit and a coder that means harm:
// a coder runs under the same user account as this server, so it can read the
// bridge token out of the git child's environment in /proc and ask the browser
// whatever it likes, and a repository it can write carries hooks that run on
// push. The user account is the trust boundary here. What this whole path is
// for is that the passphrase never travels into a coder session, and what this
// function keeps is that the caller's arguments cannot rename the action the
// dialog shows or point the call at a program of their own.
func CheckProxyArgs(args []string) error {
	if len(args) == 0 {
		return errors.New("No git command was given. The git subcommand comes first, for example push.")
	}
	if !isSubcommandWord(args[0]) {
		return fmt.Errorf("The git subcommand has to come first, and %q is no subcommand. "+
			"Options of git itself are not proxied: they can point git at another program or "+
			"another repository than the one the dialog names. Options of the subcommand belong behind it.", args[0])
	}
	for _, arg := range args {
		for _, option := range programOptions {
			if arg == option || strings.HasPrefix(arg, option+"=") {
				return fmt.Errorf("%s names a program to run and is not proxied. "+
					"A proxied call may describe a git operation and may not choose what this server executes.", option)
			}
		}
	}
	return nil
}
