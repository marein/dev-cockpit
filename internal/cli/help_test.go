package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The assistant's instructions do not repeat what a command's flags do: they
// send a turn to `--help`. So the help has to carry what the instructions
// used to say, the way past every cap and cut, or the pointer leads nowhere.
func TestTheHelpCarriesWhatTheInstructionsDelegate(t *testing.T) {
	group := newAssistantCommand()
	find := func(name string) *cobra.Command {
		t.Helper()
		for _, cmd := range group.Commands() {
			if cmd.Name() == name {
				return cmd
			}
		}
		t.Fatalf("no %s command in the assistant group", name)
		return nil
	}
	for _, want := range []struct {
		command string
		flags   []string
		says    []string
	}{
		{"status", []string{"all"}, []string{"capped at the recent ones", "`--all` lists every one"}},
		{"job-list", []string{"contains", "state", "since", "full", "all", "assistant"},
			[]string{"`--assistant all` lists every assistant's", "narrow the list before the cap", "`--full` prints the criteria whole", "`--all` lifts the cap"}},
		{"job-show", nil, []string{"nothing cut", "whoever owns it"}},
		{"coder-activity", []string{"entries", "full"}, []string{"capped by default", "`--full` lifts the cap", "`--entries`"}},
		{"terminal-screen", []string{"lines"}, []string{"an id from `status`"}},
		{"assistant-list", []string{"contains"}, []string{"capped at the first ones", "`--contains`"}},
		{"assistant-show", []string{"entries", "full"}, []string{"capped by default", "`--entries 1 --full` is the whole last message"}},
		{"line-comment-list", []string{"path", "contains", "outdated"},
			[]string{"all before the cap", "`--path` may repeat", "* stays inside a path segment and ** crosses them", "marked outdated"}},
		{"line-comment-remove", []string{"path", "outdated"},
			[]string{"`--outdated` combines with `--path`", "* stays inside a path segment and ** crosses them"}},
		{"subscription-new", []string{"terminal", "all", "cron", "task", "once", "until", "max-per-hour", "batch"},
			[]string{"session of its own", "Answer NOTHING on the first line", "`--once` ends it after its first turn", "`--until` is the expiry", "`--max-per-hour` caps the turns", "`--batch` is the window", "job-done", "coder-asks", "cron",
				"may be repeated for several", "`--all` makes them a barrier", "A barrier belongs on job-closed", "deleted while a barrier waits counts as arrived"}},
		{"coder-stop", nil, []string{"a subscription on this terminal stands"}},
		{"coder-delete", []string{"yes"}, []string{"an open job of it is closed with that reason", "fires once for the deletion", "the answer says how many went"}},
		{"coder-new", []string{"prompt", "done-when", "then"},
			[]string{"--then is the sequel", "wired in this call", "self contained", "It needs --done-when"}},
		{"subscription-list", []string{"assistant"}, []string{"`--assistant all` lists every assistant's", "next tick"}},
		{"subscription-edit", []string{"terminal", "all", "cron", "task", "once", "until", "max-per-hour", "batch"},
			[]string{"only the flags you name change anything, everything else " +
				"stands", "What cannot is the event", "done or expired is spent and is refused",
				"one that stays keeps what it reached", "a reaction that runs right now, which keeps the task " +
					"it was given", "Only your own", "The answer names what changed"}},
		{"subscription-delete", nil, []string{"Only your own", "`subscription-edit`"}},
	} {
		cmd := find(want.command)
		for _, flag := range want.flags {
			if cmd.Flags().Lookup(flag) == nil {
				t.Fatalf("%s has no --%s flag, which the instructions send a turn to --help for", want.command, flag)
			}
		}
		for _, says := range want.says {
			if !strings.Contains(cmd.Long, says) {
				t.Fatalf("the help of %s does not say %q:\n%s", want.command, says, cmd.Long)
			}
		}
	}
}
