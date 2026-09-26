package cli

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/spf13/cobra"
)

// The compose commands are the assistant's hands on a project's stacks: the
// same configured entries the compose menu offers, run through the same
// route a click takes, so what an assistant can do is exactly what the
// settings allow and nothing more. A run is detached and answers its id at
// once; how it went reaches the assistant's thread as a note, and
// `compose-show` reads it any time. A command marked to ask first waits for
// the user, see the web layer's approvals.

// composeShowTail is how many lines of a run's output `compose-show` prints
// unless asked for more. The end of the output is where a failure says why.
const composeShowTail = 40

func newComposeListCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "compose-list <project>",
		Short: "A project's compose stacks and the commands that run on them",
		Long: "List the compose stacks of a project, one per directory with a compose file, " +
			"each with its containers and where its newest run stands, then the configured " +
			"commands with the id `compose-start` takes, its command line and whether it asks the " +
			"user first. A project without a compose file has no stacks and nothing can be started. " +
			"Reads only, changes nothing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeList(cmd.OutOrStdout(), *opts, args[0])
		},
	}
}

func runComposeList(out io.Writer, opts inspectOptions, project string) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.GetJSON("/projects/"+url.PathEscape(strings.TrimSpace(project))+"/docker", actionTimeout)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, formatComposeList(answer))
	return err
}

// formatComposeList prints the stacks and the commands. A stack is named by
// its label, the project root reading as `.`, the way the compose menu reads.
func formatComposeList(answer map[string]any) string {
	var b strings.Builder
	project := text(answer["project"])
	if available, _ := answer["available"].(bool); !available {
		fmt.Fprintf(&b, "%s: no reachable Docker host, nothing can be started.\n", project)
		return b.String()
	}
	stacks, _ := answer["stacks"].([]any)
	if len(stacks) == 0 {
		fmt.Fprintf(&b, "%s: no compose file, nothing to start.\n", project)
	} else {
		fmt.Fprintf(&b, "Stacks of %s:\n", project)
	}
	for _, raw := range stacks {
		stack, _ := raw.(map[string]any)
		label := stackWord(stack["label"])
		fmt.Fprintf(&b, "  %s: %d of %d containers running", label, jsonCount(stack["running"]), jsonCount(stack["total"]))
		if busy, _ := stack["busy"].(bool); busy {
			b.WriteString(", a command runs right now")
		}
		if run, ok := stack["run"].(map[string]any); ok {
			fmt.Fprintf(&b, "; newest run %s: %s, %s", text(run["id"]), text(run["action"]), strings.ToLower(text(run["status"])))
		}
		b.WriteString("\n")
	}
	actions, _ := answer["actions"].([]any)
	if len(actions) == 0 {
		b.WriteString("No compose command is configured under Settings › Docker.\n")
		return b.String()
	}
	b.WriteString("Commands, by the id compose-start takes:\n")
	for _, raw := range actions {
		action, _ := raw.(map[string]any)
		fmt.Fprintf(&b, "  %s: %s (%s, up to %s", text(action["id"]), text(action["label"]), text(action["command"]), text(action["timeout"]))
		if confirm, _ := action["confirm"].(bool); confirm {
			b.WriteString(", asks the user first")
		}
		b.WriteString(")\n")
	}
	return b.String()
}

// stackWord is what the output calls a stack: its label, `.` for the root,
// which the JSON carries as an empty label.
func stackWord(label any) string {
	word, _ := label.(string)
	if word == "" {
		return "."
	}
	return word
}

func newComposeStartCommand(opts *inspectOptions) *cobra.Command {
	var stack string
	cmd := &cobra.Command{
		Use:   "compose-start <project> <command>",
		Short: "Start a compose command on a stack, in the background",
		Long: "Start one configured compose command, by the id `compose-list` prints, on one of the " +
			"project's stacks, the way the compose menu starts it: in the stack's directory, " +
			"detached, surviving a restart of the cockpit. Every command starts this way, whatever " +
			"its id, out of `compose-list`, and runs in the " +
			"background. It prints the run id at once and never waits for the " +
			"run; do not wait, sleep or poll `compose-show` for the result. When the run ends the " +
			"cockpit writes a note into your thread and the user is notified by it, and the events " +
			"`compose-done`, `compose-failed`, `compose-declined` and `compose-ended` fire on it; " +
			"`compose-show` is for when the user asks where a run stands. `--stack` names the " +
			"stack by its label and may be left out where the project has one. A command that asks the user first is parked instead of " +
			"started: the answer says `waits for the user's approval`, the user sees the question " +
			"on every page and on their phone, and the run starts when they approve, ends declined " +
			"when they deny it or let half an hour pass, and ends declined when the cockpit restarts " +
			"while it waits. Do not start it again while it waits. The user can turn the question " +
			"off for every assistant, when approving or with the Compose actions approval under " +
			"Settings › Assistants › Approvals; you cannot. A stack already running a " +
			"command refuses a second one.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeStart(cmd.OutOrStdout(), *opts, args[0], args[1], stack)
		},
	}
	cmd.Flags().StringVar(&stack, "stack", "", "the stack's label out of compose-list (default: the project's only stack)")
	return cmd
}

func runComposeStart(out io.Writer, opts inspectOptions, project, action, stack string) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	project = strings.TrimSpace(project)
	label, err := pickStack(client, project, stack)
	if err != nil {
		return err
	}
	answer, err := client.PostForm("/projects/"+url.PathEscape(project)+"/docker/compose", url.Values{
		"stack":  {label},
		"action": {strings.TrimSpace(action)},
	}, actionTimeout)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, startedRunLine(answer))
	return err
}

// pickStack answers the stack label a start posts: the one named, or the
// project's only stack, refused with the choices where there are several.
func pickStack(client *localapi.Client, project, stack string) (string, error) {
	answer, err := client.GetJSON("/projects/"+url.PathEscape(project)+"/docker", actionTimeout)
	if err != nil {
		return "", err
	}
	return chooseStack(project, stack, answer)
}

// chooseStack is pickStack's decision over the project's reading.
func chooseStack(project, stack string, answer map[string]any) (string, error) {
	stack = strings.TrimSpace(stack)
	named := stack != ""
	if stack == "." {
		stack = ""
	}
	stacks, _ := answer["stacks"].([]any)
	labels := make([]string, 0, len(stacks))
	for _, raw := range stacks {
		entry, _ := raw.(map[string]any)
		// The root stack's label is the empty string, which is a label and
		// no missing value.
		label, _ := entry["label"].(string)
		labels = append(labels, label)
	}
	if len(labels) == 0 {
		return "", fmt.Errorf("%s has no compose stack, nothing to start.", project)
	}
	if named {
		for _, label := range labels {
			if label == stack {
				return label, nil
			}
		}
	} else if len(labels) == 1 {
		return labels[0], nil
	}
	words := make([]string, 0, len(labels))
	for _, label := range labels {
		words = append(words, fmt.Sprintf("%q", stackWord(label)))
	}
	sort.Strings(words)
	if !named {
		return "", fmt.Errorf("%s has several stacks, name one with --stack: %s.", project, strings.Join(words, ", "))
	}
	return "", fmt.Errorf("%s has no stack %q. The stacks are %s.", project, stackWord(stack), strings.Join(words, ", "))
}

// startedRunLine is the one line compose-start prints: the run id, what it
// runs where, and whether it waits for the user instead of running.
func startedRunLine(answer map[string]any) string {
	where := stackWord(answer["stack"]) + " in " + text(answer["project"])
	if pending, _ := answer["pending"].(bool); pending {
		return fmt.Sprintf("run %s waits for the user's approval: %s on %s. It starts when they approve and ends declined when they deny it or half an hour passes; do not start it again.\n",
			text(answer["run"]), text(answer["action"]), where)
	}
	return fmt.Sprintf("run %s started: %s on %s. compose-show reads where it stands, a note lands in your thread when it ends.\n",
		text(answer["run"]), text(answer["action"]), where)
}

func newComposeShowCommand(opts *inspectOptions) *cobra.Command {
	var lines int
	cmd := &cobra.Command{
		Use:   "compose-show <project> <run>",
		Short: "Where a compose run stands, with the tail of its output",
		Long: "Show one compose run by the id `compose-start` printed: what it runs where, whether " +
			"it waits for the user's approval, runs, or ended, the exit code where it wrote one, and " +
			"the last lines of its output. The output is capped at the tail; `--lines` sets how " +
			"many, and 0 prints everything the run wrote so far. Reads only, changes nothing.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeShow(cmd.OutOrStdout(), *opts, args[0], args[1], lines)
		},
	}
	cmd.Flags().IntVar(&lines, "lines", composeShowTail, "lines of output to print from the end, 0 for all")
	return cmd
}

func runComposeShow(out io.Writer, opts inspectOptions, project, run string, lines int) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.GetJSON(composeRunPath(project, run)+"/output", actionTimeout)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, formatComposeShow(answer, lines))
	return err
}

// composeRunPath is the run's own path, the page a notification would link.
func composeRunPath(project, run string) string {
	return "/projects/" + url.PathEscape(strings.TrimSpace(project)) + "/docker/runs/" + url.PathEscape(strings.TrimSpace(run))
}

// formatComposeShow prints one run: the facts on top, the output tail below.
func formatComposeShow(answer map[string]any, lines int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "run %s: %s on %s\n", text(answer["id"]), text(answer["action"]), stackWord(answer["stack"]))
	fmt.Fprintf(&b, "command: %s\n", text(answer["command"]))
	status := text(answer["status"])
	switch {
	case jsonBool(answer["pending"]):
		status = "waits for the user's approval"
	case jsonBool(answer["running"]):
		status = "running"
	case jsonBool(answer["declined"]):
		status = "declined, never ran: " + text(answer["failure"])
	case jsonBool(answer["failed"]):
		status = "failed: " + text(answer["failure"])
	default:
		status = "done, " + strings.ToLower(status)
	}
	fmt.Fprintf(&b, "status: %s\n", status)
	if jsonBool(answer["exited"]) {
		fmt.Fprintf(&b, "exit code: %d\n", jsonCount(answer["exit"]))
	}
	if started := jsonTime(answer["startedAt"]); started != "" {
		fmt.Fprintf(&b, "started: %s\n", started)
	}
	if ended := jsonTime(answer["endedAt"]); ended != "" {
		fmt.Fprintf(&b, "ended: %s\n", ended)
	}
	raw, _ := answer["output"].(string)
	output := strings.TrimRight(raw, "\n")
	if output == "" {
		b.WriteString("output: nothing yet\n")
		return b.String()
	}
	all := strings.Split(output, "\n")
	shown := all
	if lines > 0 && len(all) > lines {
		shown = all[len(all)-lines:]
	}
	if len(shown) < len(all) {
		fmt.Fprintf(&b, "output, the last %d of %d lines:\n", len(shown), len(all))
	} else {
		b.WriteString("output:\n")
	}
	for _, line := range shown {
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}

func jsonBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func newComposeStopCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "compose-stop <project> <run>",
		Short: "Call a running compose command off",
		Long: "End a compose run that is still going, by the id `compose-start` printed: the " +
			"command's whole process group is killed and the run ends as cancelled, which its note " +
			"and `compose-show` then say. It answers at once and does not wait for that end, the " +
			"note comes into your thread like every other end. A run that waits for the user's approval is declined by " +
			"it. A run that is already over is refused, and so is one that is not yours: the user's " +
			"runs and another assistant's are theirs to stop.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeStop(cmd.OutOrStdout(), *opts, args[0], args[1])
		},
	}
}

func runComposeStop(out io.Writer, opts inspectOptions, project, run string) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	if _, err := client.PostForm(composeRunPath(project, run)+"/stop", url.Values{}, actionTimeout); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "run %s called off; its note says how it ended.\n", strings.TrimSpace(run))
	return err
}
