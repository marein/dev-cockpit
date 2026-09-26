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
		{"line-comment-list", []string{"path", "contains", "outdated"},
			[]string{"all before the cap", "`--path` may repeat", "* stays inside a path segment and ** crosses them", "marked outdated",
				// The instructions say only that an outdated note lost its
				// anchor; how a quote is judged and when it follows its line
				// is read here.
				"Every read holds the quotes against the files", "follows it to the new line on its own"}},
		{"line-comment-remove", []string{"path", "outdated"},
			[]string{"`--outdated` combines with `--path`", "* stays inside a path segment and ** crosses them"}},
		// The instructions carry the rules of a compose run; what a stack is,
		// what a parked run does, how long it waits, where the question is
		// turned off and how the output is capped is read here.
		{"compose-list", nil, []string{"one per directory with a compose file", "the id `compose-start` takes", "whether it asks the user first", "Reads only, changes nothing"}},
		{"compose-start", []string{"stack"}, []string{"prints the run id at once and never waits", "do not wait, sleep or poll `compose-show`", "Every command starts this way, whatever its id", "the user is notified by it", "`--stack` names the stack by its label",
			"waits for the user's approval", "ends declined when they deny it or let half an hour pass", "restarts while it waits", "Do not start it again while it waits",
			"off for every assistant", "Compose actions approval", "Settings › Assistants › Approvals; you cannot",
			"compose-done", "compose-failed", "compose-declined", "compose-ended", "refuses a second one"}},
		{"compose-show", []string{"lines"}, []string{"capped at the tail", "`--lines` sets how many", "0 prints everything", "Reads only, changes nothing"}},
		{"compose-stop", nil, []string{"ends as cancelled", "does not wait for that end", "waits for the user's approval is declined", "already over is refused"}},
		{"trigger-new", []string{"name", "terminal", "all", "cron", "tz", "task", "once", "until", "batch", "model"},
			[]string{"session of its own", "Answer NOTHING on the first line", "job-done", "coder-news", "cron",
				// A compose run's end is an event like a job's, so the help
				// names its kinds where it names the others.
				"compose-done", "compose-ended",
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
		// A delete takes the whole assistant, its triggers with it, and this
		// is the one place somebody reads before running it. The instructions
		// name the same three, because an assistant that answers "your
		// schedules survive" is wrong on both surfaces at once.
		{"assistant-delete", []string{"yes"}, []string{"its jobs and its triggers are gone",
			"handed back to the user, named in the answer", "You cannot delete yourself"}},
		{"coder-delete", []string{"yes"}, []string{"an open job of it is closed with that reason", "fires once for the deletion",
			// A trigger that can never fire again goes with the terminal,
			// which the instructions used to say and now delegate here.
			"one that had no other terminal left is removed", "the answer says how many went"}},
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
		{"trigger-edit", []string{"name", "terminal", "all", "cron", "tz", "task", "once", "until", "batch", "model"},
			[]string{"only the flags you name change anything, everything else " +
				"stands", "the name with `--name` (an empty one takes the name away", "What cannot is the event", "done or expired is spent and is refused",
				"one that stays keeps what it reached", "a reaction that runs right now, which keeps the task " +
					"it was given", "Only your own", "The answer names what changed",
				"Moving the schedule or its zone works the next tick out again",
				"neither moves the stored zone, `timezone-set` is what does",
				"`--model default` clears it back to the assistant default, evaluated when it fires"}},
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
