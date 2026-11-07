package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The assistant's instructions do not repeat what a command's flags do: they
// send a turn to `--help`. So the help has to carry what the instructions
// used to say, the way past every cap and cut, or the pointer leads nowhere.
// What is read is the whole help, the prose and the flag table together,
// because that is the one output a turn gets: a fact the flag line already
// carries is carried, and saying it again above the table is the doubling
// this test must not ask for.
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
			[]string{"`--assistant all` lists every assistant's", "narrow the list before the cap", "`--full` prints the criteria whole", "`--all` lifts the cap",
				// A closed job stands as its line alone, so the way back to
				// its criterion and its report is in the help.
				"`job-show <terminal>` brings the criterion and the last report back whole",
				"gives a closed job those two lines back"}},
		{"job-show", nil, []string{"nothing cut", "whoever owns it"}},
		{"coder-activity", []string{"entries", "full"}, []string{"capped by default", "`--full` lifts the cap", "`--entries`"}},
		{"terminal-screen", []string{"lines"}, []string{"an id from `status`"}},
		{"assistant-list", []string{"contains"}, []string{"capped at the first ones", "`--contains`"}},
		{"assistant-show", []string{"entries", "full", "contains"},
			[]string{"capped by default", "`--entries 1 --full` is the whole last message",
				"narrows before the cap", "reaches messages the capped reading never shows",
				"how many messages carry the word and how many the thread holds"}},
		{"line-comment-list", []string{"path", "contains", "outdated", "open"},
			[]string{"all before the cap", "`--path` may repeat", "* stays inside a path segment and ** crosses them", "marked outdated",
				// The instructions say only that an outdated note lost its
				// anchor; how a quote is judged and when it follows its line
				// is read here.
				"Every read holds the quotes against the files", "follows it to the new line on its own",
				"the project comments, threads on the whole project, which come first", "`--path` and `--outdated` leave the project comments out"}},
		{"line-comment-add", []string{"project-wide", "reply"}, []string{"a project comment", "a thread on the whole project without a file and a line", "a project comment's included"}},
		{"line-comment-resolve", []string{"reopen"}, []string{"`forge-thread-resolve`"}},
		{"line-comment-remove", []string{"path", "outdated"},
			[]string{"`--outdated` combines with `--path`", "* stays inside a path segment and ** crosses them", "`--path` and `--outdated` never take one"}},
		// The instructions carry the rules of a compose run; what a stack is,
		// what a command that asks first does, how long it waits, where the
		// question is turned off and how the output is capped is read here.
		{"compose-list", nil, []string{"one per directory with a compose file", "the id `compose-start` takes", "its intent (start, stop, restart, build, pull, purge or command)", "proxy routes first and published ports after", "whether it asks the user first", "Reads only, changes nothing"}},
		{"compose-start", []string{"stack"}, []string{"prints the run id at once and never waits", "do not wait, sleep or poll `compose-show`", "Every command starts this way, whatever its id", "the user is notified by it", "`--stack` names the stack by its label",
			"waits for the user's approval", "carries no run id", "a note in your thread then names the run id",
			"When they deny it, let half an hour pass or the cockpit restarts while it waits, nothing runs", "Do not start it again while it waits",
			"off for every assistant", "Compose actions approval", "Settings › Assistants › Approvals; you cannot",
			"With the question off the command starts like any other and prints its run id",
			"compose-done", "compose-failed", "compose-declined", "compose-ended", "refuses a second one"}},
		{"project-worktree-new", []string{"from", "name", "fast-forward"}, []string{"named <project>-<branch>", "origin/feature", "A failed script fails the command, the worktree stands"}},
		{"project-worktree-list", nil, []string{"lists the worktrees of its main project", "Reads only, changes nothing"}},
		{"project-worktree-script-show", nil, []string{"or say there is none", "time bound and environment", "Reads only, changes nothing"}},
		{"project-worktree-script-set", nil, []string{"Replace the whole worktree post script", "`project-worktree-script-show` first", "read it, its directory, time bound and environment with", "Empty content deletes", "waits for the user's approval", "Worktree post script approval"}},
		{"compose-show", []string{"lines"}, []string{"capped at the tail", "`--lines` sets how many", "0 prints everything", "Reads only, changes nothing"}},
		// The instructions name forge-show and what it reads; which head it
		// reads, the cap of the jobs and the token it needs are read here.
		{"forge-show", []string{"branch", "all", "log", "job", "lines", "threads", "fresh"}, []string{"never the local HEAD", "the open change request from that branch, else one merged or closed at the pushed head",
			"check runs and commit statuses combined", "the local branch or the change request's source stands elsewhere",
			"capped at the first ones; `--all` lists every one", "Needs the host's token", "Reads only, changes nothing",
			// What the poll read answers in place of the forge, and how to
			// ask the forge anyway.
			"no longer than 30 seconds ago answers", "`--fresh` always asks it",
			// What a log holds, whose logs --log reads and how a job without
			// one shows are read here, the instructions name the flag alone.
			"the first 3 jobs that failed the pipeline", "colours and control sequences taken out",
			"`--job` the log of one job instead", "is marked `no log`", "the last 100 lines unless `--lines`",
			// Which threads --threads reads, how the lists stand and that
			// they always come from the forge are read here.
			"`--threads` adds the review threads, read from the forge on every call", "file, line, side, whether outdated",
			"the forge's own system notes left out", "first 30 threads, the unresolved first",
			"the id `forge-thread-reply` and `forge-thread-resolve` take",
			// Which jobs cannot run again is marked here, how a retry goes
			// is the retry's own help.
			"one the forge cannot run again `no retry`"}},
		// The instructions name the retry and that it asks first; which jobs
		// run again on each forge, what is refused, the token it needs and
		// where the question is turned off are read here.
		{"forge-pipeline-retry", []string{"branch", "job"}, []string{"the branch HEAD is on unless `--branch` names another", "an id `forge-show` lists",
			"Only a failed pipeline or job runs again", "its failed and canceled jobs", "each failed trigger job is retried on its own",
			"every failed GitHub Actions workflow run of the pushed head", "A commit status and a check run of another app cannot run again",
			"A branch pushed again since, another pipeline or a job that ran again is refused", "the scope api on GitLab", "Actions read and write",
			"waits for the user's approval", "do not run it again while it waits", "Pipeline retry approval", "Settings › Assistants › Approvals; you cannot",
			"Without the question the jobs run at once", "forge-pipeline-succeeded"}},
		// The instructions name the open and that it asks first; what it is
		// prefilled with, where it merges, what is refused, the token it
		// needs and where the question is turned off are read here.
		{"forge-change-new", []string{"branch", "target", "title", "body", "ready"}, []string{"the branch HEAD is on unless `--branch` names another",
			"merges into the default branch unless `--target`", "opens as a draft unless `--ready`", "made of the commits the pushed head carries since the target",
			"the oldest one's subject over a list of every subject", "A branch that carries no commit gets no title", "a branch not pushed is refused",
			"gets no second one, the answer names the open one", "the scope api on GitLab", "Pull requests read and write",
			"waits for the user's approval", "do not run it again while it waits", "Change request approval", "Settings › Assistants › Approvals; you cannot",
			"Without the question it opens at once"}},
		// The instructions name the publish and that it asks first; which
		// comments publish, where their lines have to stand, what becomes of
		// them, the token it needs and where the question is turned off are
		// read here.
		{"forge-comment-publish", []string{"all"}, []string{"the branch HEAD is on", "the ids `line-comment-list` prints", "with `--all` every comment the user or you wrote",
			"and project comments as general comments of it", "A review thread of the forge, a guest's comment and one with replies do not publish", "has to stand in the pushed head", "a line not pushed is refused, push it first",
			"an outdated comment no push carries", "one the change request's diff shows", "else the whole call is refused",
			"becomes that review thread among the line comments", "a project comment that general thread among the project comments", "the scope api on GitLab", "Pull requests read and write",
			"waits for the user's approval", "do not run it again while it waits", "Review comment approval", "Settings › Assistants › Approvals; you cannot",
			"Without the question they are published at once"}},
		// The instructions name the writes on a thread and that they ask
		// first; which thread they take, what is refused, the token they
		// need and where the question is turned off are read here.
		{"forge-thread-reply", []string{"branch", "resolve"}, []string{"the branch HEAD is on unless `--branch` names another", "the thread id `forge-show --threads` and `line-comment-list` print and a forge-review-comment event names",
			"`fixed in abc123`", "at most 16 KiB", "`--resolve` resolves the thread after the reply", "on its diff or a general one", "A comment of GitHub's conversation is no thread and is refused",
			"shows it once that review is submitted", "the scope api on GitLab", "Pull requests read and write", "waits for the user's approval",
			"do not run it again while it waits", "Review thread approval", "Settings › Assistants › Approvals; you cannot", "Without the question it is posted at once"}},
		{"forge-thread-resolve", []string{"branch"}, []string{"the branch HEAD is on unless `--branch` names another", "the thread id `forge-show --threads` and `line-comment-list` print and a forge-review-comment event names",
			"A thread resolved already and one the forge does not resolve are refused, a GitLab comment that started no thread and a comment of GitHub's conversation among them", "`forge-thread-reply --resolve`, one question for both",
			"the Developer role", "the scope api on GitLab", "Pull requests read and write",
			"waits for the user's approval", "do not run it again while it waits", "Review thread approval", "Settings › Assistants › Approvals; you cannot",
			"Without the question it is resolved at once"}},
		{"compose-stop", nil, []string{"ends as cancelled", "does not wait for that end", "has no run yet and nothing to stop", "already over is refused"}},
		{"trigger-new", []string{"name", "terminal", "all", "project", "branch", "cron", "tz", "task", "once", "until", "batch", "model"},
			[]string{"session of its own", "Answer NOTHING on the first line", "job-done", "coder-news", "cron",
				// A compose run's end is an event like a job's, so the help
				// names its kinds where it names the others.
				"compose-done", "compose-ended",
				// A pipeline's end is read off the poll of the checked out
				// branch, once per way it ends, and forge-show reads the rest.
				"forge-pipeline-succeeded", "forge-pipeline-failed", "forge-pipeline-ended", "takes `--project`", "the branch a project has checked out",
				"once per way it ends, across restarts", "one that ended before the first read never",
				"narrows it to one branch while it is checked out", "`forge-show` reads the rest",
				// A review comment is read off the same poll, once per note
				// by somebody else, and on an idle branch only at its slow pace.
				"forge-review-comment", "every 5 minutes on an idle branch", "never on a note of the token's account", "one standing at the first read",
				"`forge-show --threads` reads every thread",
				// The bounds are the flag table's, so the prose says only what
				// no flag line does: that a schedule ignores a window.
				"end the trigger after its first turn", "expiry as a span from now", "it stands until removed",
				"how long to collect events into one turn", "`--batch` on a schedule is ignored and the answer says",
				// There is one coder event and no narrower one, which is what
				// keeps a turn from looking for the one that ended alone.
				"every signal a coder sends", "there is no narrower coder event",
				"may be repeated for several", "`--all` makes them a barrier", "A barrier belongs on job-closed", "deleted while a barrier waits counts as arrived",
				// The rule for a name is the instructions'. What the help owes
				// is the cap, because a refusal costs a call.
				"`--name` is optional, at most",
				// The assistant default is read when the reaction starts:
				// the Triggers pick at the ring, else Same as chat.
				"the assistant default, evaluated when it fires", "your Triggers pick at the ring where one stands, else Same as chat",
				// A schedule is a wall clock somewhere, so the zone is a field
				// of it and never an offset, and what moves the stored one is
				// its own command.
				"`--tz` ", "an IANA name like Europe/Berlin", "never an offset",
				"the schedule takes the stored zone", "`timezone-set` is what moves the stored one",
				// What a changeover does is looked up when somebody asks, so
				// it stands here and not in the file every turn carries,
				// which forbids working a time out in the first place.
				"the schedule falls out once", "the schedule fires twice"}},
		{"coder-stop", nil, []string{"a trigger on this terminal stands", "the session keeps its identifier"}},
		// The rule a start of an assistant runs under stands in the
		// instructions and again here, read right before the call.
		{"assistant-new", []string{"coder", "name", "message"}, []string{"the way the page's New does", "nothing of yours",
			"reads nothing of your thread", "Prints the new id and its name",
			"only when the user asked for exactly that, never on your own idea",
			"a trigger or a sequel whose task the user asked for counts as their word", "One call starts one assistant"}},
		// A prompt into a running assistant stands under the same rule, and
		// the asking turn has to know it reads as the user's own there.
		{"assistant-send-prompt", nil, []string{"another one or yourself", "the way the user types into its composer",
			"nothing marks it as yours", "waits in that assistant's queue", "the text has to stand alone",
			"Prints which assistant got it and whether it answers now or waits",
			"only when the user asked for exactly that, never on your own idea",
			"a trigger or a sequel whose task the user asked for counts as their word"}},
		// A delete takes the whole assistant, its triggers with it, and this
		// is the one place somebody reads before running it. The instructions
		// name the same three, because an assistant that answers "your
		// schedules survive" is wrong on both surfaces at once.
		{"assistant-delete", nil, []string{"its jobs and its triggers are gone",
			"handed back to the user, named in the outcome", "waits for the user's approval", "never waits for the decision",
			"unless the assistant deleted is you", "do not run the delete again while it waits",
			"Assistant delete approval", "Settings › Assistants › Approvals; you cannot", "With the question off the delete runs at once",
			"Several assistants in one call are one question, one approval and one note", "a failure of one does not stop the others",
			"names what went and each failure with its reason", "With the question off each one prints its own outcome"}},
		{"coder-delete", nil, []string{"an open job of it is closed with that reason", "fires once for the deletion",
			// A trigger that can never fire again goes with the terminal,
			// which the instructions used to say and now delegate here.
			"one that had no other terminal left is removed", "the outcome says how many went",
			"waits for the user's approval", "never waits for the decision", "do not run the delete again while it waits",
			"Coder delete approval", "Settings › Assistants › Approvals; you cannot", "With the question off the delete runs at once and prints what it deleted",
			"Several coders in one call are one question, one approval and one note", "a failure of one does not stop the others",
			"names what went and each failure with its reason", "With the question off each one prints its own outcome"}},
		// A project delete asks the user first, like a compose command that
		// asks first: the instructions carry the rule, what the answer says,
		// how the question ends and where it is turned off is read here.
		{"project-delete", nil, []string{"takes those with it, and the outcome names them", "only run this when the user asked for exactly this project",
			"waits for the user's approval", "never waits for the decision", "When they deny it, let half an hour pass or the cockpit restarts while it waits, the project stays",
			"a note lands in your thread with the outcome", "do not run the delete again while it waits",
			"off for every assistant", "Project delete approval", "Settings › Assistants › Approvals; you cannot", "With the question off the delete runs at once and prints what it deleted", "once the project is gone, its containers brought down first",
			"Several projects in one call are one question, one approval and one note", "a failure of one does not stop the others",
			"names what went and each failure with its reason", "With the question off each one prints its own outcome"}},
		{"coder-new", []string{"prompt", "model", "done-when", "then"},
			[]string{"--then is the sequel", "wired in this call", "self contained", "It needs --done-when",
				// The sequel's expiry is the job's, so a chain cannot die of
				// two defaults drifting apart in two files.
				"it expires with that job"}},
		{"trigger-list", []string{"assistant", "contains", "since", "all", "full"},
			[]string{"`--assistant all` lists every assistant's", "next tick", "its name where it has one",
				// A tick is shown in the zone its schedule is read in, so the
				// line saying so is what keeps nobody converting it by hand.
				"in the zone that schedule is read in",
				// The one list that was neither capped nor cut is both now,
				// and the flags are the way past each of them.
				"the spent ones are capped at the recent ones", "narrow the list before the cap",
				// A spent trigger stands as short as a closed job, so the way
				// back to its task and its bounds is in the help.
				"the way a closed job stands in `job-list`", "gives a spent trigger its task and its bounds back",
				// "What happened yesterday" is a question about this list, so
				// the flag that answers it says which moments it counts.
				"`--since` is what \"what happened yesterday\" asks for",
				"changed by every fire, every end and every edit",
				"`--full` prints the tasks whole", "`--all` lifts the cap"}},
		{"trigger-edit", []string{"name", "terminal", "all", "project", "branch", "cron", "tz", "task", "once", "until", "batch", "model"},
			[]string{"only the flags you name change anything, everything else " +
				"stands", "the name with `--name` (an empty one takes the name away", "What cannot is the event", "done or expired is spent and is refused",
				"one that stays keeps what it reached", "a reaction that runs right now, which keeps the task " +
					"it was given", "Only your own", "The answer names what changed",
				"Moving the schedule or its zone works the next tick out again",
				"neither moves the stored zone, `timezone-set` is what does",
				"`--model default` clears it back to the assistant default, evaluated when it fires",
				"(an empty one takes it back to whichever is checked out)"}},
		{"trigger-delete", nil, []string{"Only your own", "`trigger-edit`"}},
		// The zone a schedule falls back to is moved by its own verb and by
		// nothing else, which is the line the help has to draw: `--tz` is one
		// schedule's zone, this is where the user sits.
		{"timezone-set", nil, []string{"only when the user says where they are", "never to record a zone you worked out",
			"pass `--tz` to", "it moves nothing here", "every schedule carries the zone it was made with"}},
		// Which clock a schedule is read on is a question of its own, so it
		// has a verb of its own instead of costing a whole listing.
		{"timezone-get", nil, []string{"whether anybody stored it", "which zone this server runs in", "Reads only, changes nothing"}},
		// The instructions send a turn here for the names --model takes
		// instead of listing any, so the help has to say what a row carries.
		{"model-list", nil, []string{"marked cli or added", "the defaults set for it", "without a coder, every installed one", "Reads only, changes nothing"}},
		// The instructions send a turn here for its own models instead of
		// carrying them, so the help says what the three lines are.
		{"assistant-models-get", nil, []string{"the ring, the coder's own default or the CLI's default", "same as chat, evaluated when the turn starts", "your Triggers pick at the ring where one stands", "needs --as", "Reads only, changes nothing", "`assistant-models-set` is what moves a pick"}},
		// The instructions say when a turn may move a pick and that the
		// answer is quoted; what a flag does and what `default` means is
		// read here.
		{"assistant-models-set", []string{"chat", "checks", "triggers"},
			[]string{"only the flags you name change anything", "`default` clears a pick back to its empty entry",
				"Same as chat for the checks and the triggers", "out of `model-list`", "a refused name changes nothing",
				"the sentence the ring's save shows", "needs --as"}},
	} {
		cmd := find(want.command)
		for _, flag := range want.flags {
			if cmd.Flags().Lookup(flag) == nil {
				t.Fatalf("%s has no --%s flag, which the instructions send a turn to --help for", want.command, flag)
			}
		}
		help := cmd.Long + "\n" + cmd.UsageString()
		for _, says := range want.says {
			if !strings.Contains(help, says) {
				t.Fatalf("the help of %s does not say %q:\n%s", want.command, says, help)
			}
		}
	}
}
