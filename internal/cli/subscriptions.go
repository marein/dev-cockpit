package cli

import (
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/spf13/cobra"
)

// The subscriptions: how an assistant hangs itself onto an event and acts when
// it fires. Every command goes over the socket to the same handlers the page
// posts to, so what the page can subscribe to the assistant can, with the same
// filters and the same bounds, and nothing is page only.

// assistantSubscriptionsPath is where the subscriptions live, the same path
// the page's list and form use.
const assistantSubscriptionsPath = "/assistants/subscriptions"

func newSubscribeCommand(opts *inspectOptions) *cobra.Command {
	var a subscribeArgs
	cmd := &cobra.Command{
		Use:   "subscription-new <event>",
		Short: "React to an event: a note in your thread and a turn of yours",
		Long: "Subscribe to an event. When it fires, the reaction runs in a session of its own with " +
			"the task, your instruction file, the memory and your workspace files, nothing of the " +
			"conversation, so the task must be self contained; its answer is pushed into your " +
			"thread, marked as started without the user. Answer NOTHING on the first line when there " +
			"is nothing to do or say, then nothing is pushed and nobody is notified. The events are " +
			strings.Join(eventNames(), ", ") + ". `--terminal` narrows a job or coder event to one " +
			"terminal and may be repeated for several, left out it is any job of yours or any " +
			"coder; a job event only ever reaches the assistant whose job it is. With several " +
			"terminals any of them fires it; `--all` makes them a barrier instead, one turn once " +
			"every one of them produced an event, with every report in it. A barrier belongs on " +
			"job-closed: on job-done it waits forever for a job that closes blocked. A terminal " +
			"deleted while a barrier waits counts as arrived, and the barrier goes with the last " +
			"of its terminals. `cron` takes `--cron` with five crontab fields, local " +
			"time. The bounds: `--once` ends it after its first turn, `--until` is the expiry as a " +
			"span (8h, the default; never for none), `--max-per-hour` caps the turns it buys (6, the " +
			"default; a refused turn shows on the subscription's line), `--batch` is the window that folds " +
			"events into one turn (30s, the default; 0 for a schedule). The answer names the id " +
			"`subscription-edit` and `subscription-delete` take.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a.event = args[0]
			return runSubscribe(cmd.OutOrStdout(), *opts, a)
		},
	}
	subscriptionFlags(cmd, &a)
	return cmd
}

type subscribeArgs struct {
	event, spec, task, until, batch string
	terminals                       []string
	once, all                       bool
	perHour                         int
}

// subscriptionFlags are the fields of a subscription, the same set on the
// command that makes one and on the command that changes one: one wiring, so a
// flag cannot mean two things or go missing on one of them. What they do to a
// change is decided by whether they were named, see runSubscriptionEdit.
func subscriptionFlags(cmd *cobra.Command, a *subscribeArgs) {
	cmd.Flags().StringArrayVar(&a.terminals, "terminal", nil, "a terminal the event is about (job and coder events), repeatable, default any")
	cmd.Flags().BoolVar(&a.all, "all", false, "fire once every named terminal produced an event, not on the first one")
	cmd.Flags().StringVar(&a.spec, "cron", "", "the schedule of a cron event, five crontab fields like \"*/30 9-17 * * 1-5\"")
	cmd.Flags().StringVar(&a.task, "task", "", "what to do when the event fires (required)")
	cmd.Flags().BoolVar(&a.once, "once", false, "end the subscription after its first turn")
	cmd.Flags().StringVar(&a.until, "until", "", "expiry as a span from now, like 8h or 2d; never for none (default 8h)")
	cmd.Flags().IntVar(&a.perHour, "max-per-hour", 0, "at most this many turns per hour (default 6)")
	cmd.Flags().StringVar(&a.batch, "batch", "", "how long to collect events into one turn, like 30s (default 30s, 0 for cron)")
}

// eventNames are the events a subscription may name, in the order the
// surfaces list them.
func eventNames() []string {
	out := make([]string, 0, len(assistant.EventOptions))
	for _, k := range assistant.EventOptions {
		out = append(out, k.Name())
	}
	return out
}

func runSubscribe(out io.Writer, opts inspectOptions, a subscribeArgs) error {
	if _, err := assistant.ParseEventOption(a.event); err != nil {
		return err
	}
	if strings.TrimSpace(a.task) == "" {
		return fmt.Errorf("A subscription needs --task: what to do when the event fires.")
	}
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	form := url.Values{
		"form":         {"new"},
		"event":        {strings.TrimSpace(a.event)},
		"spec":         {strings.TrimSpace(a.spec)},
		"task":         {strings.TrimSpace(a.task)},
		"until":        {strings.TrimSpace(a.until)},
		"max_per_hour": {strconv.Itoa(a.perHour)},
		"batch":        {strings.TrimSpace(a.batch)},
	}
	// The terminals travel as the repeated field the page's multiple select
	// posts, and the mode as the value its switch posts.
	for _, terminal := range a.terminals {
		if terminal = strings.TrimSpace(terminal); terminal != "" {
			form.Add("terminal", terminal)
		}
	}
	if a.all {
		form.Set("mode", "all")
	}
	if a.once {
		form.Set("once", "on")
	}
	answer, err := client.PostForm(assistantSubscriptionsPath, form, actionTimeout)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "subscribed %s: %s\n", word(answer["id"]), word(answer["summary"]))
	return nil
}

func newSubscriptionsCommand(opts *inspectOptions) *cobra.Command {
	var whose string
	cmd := &cobra.Command{
		Use:   "subscription-list",
		Short: "Show the events an assistant reacts to",
		Long: "Show the subscriptions: what fires each one, its task, whether it is standing, a one " +
			"shot or over, how often it fired, the next tick of a schedule and when it expires, " +
			"each with the id `subscription-edit` and `subscription-delete` take. It lists your " +
			"own; `--assistant all` " +
			"lists every assistant's, each line naming whose it is, and `--assistant <id>` one " +
			"other's. Run outside a turn it lists everybody's. Reads only, changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSubscriptions(cmd.OutOrStdout(), *opts, whose)
		},
	}
	cmd.Flags().StringVar(&whose, "assistant", "", "whose subscriptions to list: an assistant id, or all (default: your own)")
	return cmd
}

func runSubscriptions(out io.Writer, opts inspectOptions, whose string) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	query := url.Values{}
	if whose = strings.TrimSpace(whose); whose != "" {
		query.Set("assistant", whose)
	}
	path := assistantSubscriptionsPath
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	answer, err := client.GetJSON(path, inputTimeout)
	if err != nil {
		return err
	}
	rows, _ := answer["subscriptions"].([]any)
	if len(rows) == 0 {
		fmt.Fprintln(out, "no subscriptions")
		return nil
	}
	owners, _ := answer["owners"].(bool)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		printSubscription(out, row, owners, time.Now())
	}
	return nil
}

// printSubscription prints one subscription the way job-list prints a job:
// the id and the event on the first line, the rest indented under it.
func printSubscription(out io.Writer, row map[string]any, owners bool, now time.Time) {
	head := fmt.Sprintf("%s  %s", word(row["id"]), word(row["label"]))
	if where := word(row["where"]); where != "" {
		head += ", " + where
	}
	if owners {
		head += "  (" + word(row["ownerName"]) + ")"
	}
	fmt.Fprintln(out, head)
	fmt.Fprintf(out, "  task      %s\n", word(row["task"]))
	state := word(row["state"])
	if once, _ := row["once"].(bool); once && state == "standing" {
		state += ", once"
	}
	if reacting, _ := row["reacting"].(bool); reacting {
		state += ", reacting now"
	}
	fired, _ := row["fired"].(float64)
	fmt.Fprintf(out, "  state     %s, fired %d time%s\n", state, int(fired), plural(int(fired)))
	var facts []string
	if next := stamp(row["nextAt"]); !next.IsZero() {
		facts = append(facts, "next "+next.Local().Format("2006-01-02 15:04"))
	}
	if until := stamp(row["expiresAt"]); !until.IsZero() {
		facts = append(facts, "until "+until.Local().Format("2006-01-02 15:04"))
	} else {
		facts = append(facts, "no expiry")
	}
	if perHour, _ := row["maxPerHour"].(float64); perHour > 0 {
		facts = append(facts, fmt.Sprintf("%d turns per hour at most", int(perHour)))
	}
	if batch, _ := row["batchSeconds"].(float64); batch > 0 {
		facts = append(facts, fmt.Sprintf("batch %ds", int(batch)))
	}
	fmt.Fprintf(out, "  bounds    %s\n", strings.Join(facts, ", "))
	if missing := waitingFor(row); len(missing) > 0 {
		fmt.Fprintf(out, "  waiting   %s\n", strings.Join(missing, ", "))
	}
	if note := word(row["note"]); note != "" {
		fmt.Fprintf(out, "  last      %s\n", note)
	}
}

// waitingFor names the terminals a barrier still misses, so a subscription that
// has not fired says what it is waiting for. Empty for every other one: they
// fire on the first event, there is nothing to wait for.
func waitingFor(row map[string]any) []string {
	if all, _ := row["all"].(bool); !all {
		return nil
	}
	var missing []string
	items, _ := row["targets"].([]any)
	for _, raw := range items {
		target, _ := raw.(map[string]any)
		met, _ := target["met"].(bool)
		gone, _ := target["gone"].(bool)
		if met || gone {
			continue
		}
		name := word(target["name"])
		if name == "" {
			name = word(target["terminal"])
		}
		missing = append(missing, name)
	}
	return missing
}

// word reads a string out of a JSON answer, empty when there is none: an
// absent field is nothing to print, not a question mark.
func word(value any) string {
	s, _ := value.(string)
	return strings.TrimSpace(s)
}

// stamp reads an RFC3339 time out of a JSON answer, zero when there is none.
func stamp(value any) time.Time {
	raw, _ := value.(string)
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func newSubscriptionEditCommand(opts *inspectOptions) *cobra.Command {
	var a subscribeArgs
	cmd := &cobra.Command{
		Use:   "subscription-edit <id>",
		Short: "Change a standing subscription instead of making it again",
		Long: "Change one subscription by the id `subscription-list` shows, with the flags " +
			"`subscription-new` takes: only the flags you name change anything, everything else " +
			"stands, so a typo in the task is one `--task` away and nothing else moves. What can be " +
			"changed is the task, the terminals with their mode, a schedule's `--cron` and the " +
			"bounds `--once`, `--until`, `--max-per-hour` and `--batch`. What cannot is the event: " +
			"another event is another subscription, so make one and remove this. A subscription " +
			"that is done or expired is spent and is refused. Changing a terminal is a replacement, " +
			"`--terminal` names the whole list; one that stays keeps what it reached, so a barrier " +
			"waits on for the targets that have not arrived and counts a new one as not arrived " +
			"unless its job is already closed, and a removed one takes its state with it. What the " +
			"subscription already did is untouched: how often it fired, when it was made, the " +
			"events waiting in its window, and a reaction that runs right now, which keeps the task " +
			"it was given. Only your own, like the removal. The answer names what changed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSubscriptionEdit(cmd.OutOrStdout(), *opts, args[0], a, cmd.Flags().Changed)
		},
	}
	subscriptionFlags(cmd, &a)
	return cmd
}

// runSubscriptionEdit posts what was named and nothing else: a field the form
// does not carry is one nobody named, and the server leaves that one standing.
// That is the same reading a create gets, so there is one rule and one handler
// for both.
func runSubscriptionEdit(out io.Writer, opts inspectOptions, id string, a subscribeArgs, named func(string) bool) error {
	form := url.Values{"form": {"edit"}, "id": {strings.TrimSpace(id)}}
	if named("task") {
		form.Set("task", strings.TrimSpace(a.task))
	}
	if named("cron") {
		form.Set("spec", strings.TrimSpace(a.spec))
	}
	if named("until") {
		form.Set("until", strings.TrimSpace(a.until))
	}
	if named("max-per-hour") {
		form.Set("max_per_hour", strconv.Itoa(a.perHour))
	}
	if named("batch") {
		form.Set("batch", strings.TrimSpace(a.batch))
	}
	if named("once") {
		// The empty value is what the page's box posts when it is unchecked: the
		// field is there and says no, rather than not being there at all.
		form.Set("once", "")
		if a.once {
			form.Set("once", "on")
		}
	}
	if named("all") {
		form.Set("mode", "any")
		if a.all {
			form.Set("mode", "all")
		}
	}
	if named("terminal") {
		form["terminal"] = []string{""}
		for _, terminal := range a.terminals {
			if terminal = strings.TrimSpace(terminal); terminal != "" {
				form.Add("terminal", terminal)
			}
		}
	}
	// form holds the action and the id and nothing else: nothing was named, so
	// there is nothing to change and no call to make.
	if len(form) == 2 {
		return fmt.Errorf("Name what to change: --task, --terminal, --all, --cron, --once, --until, --max-per-hour or --batch.")
	}
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.PostForm(assistantSubscriptionsPath, form, actionTimeout)
	if err != nil {
		return err
	}
	changed := word(answer["changed"])
	if changed == "" {
		changed = "nothing"
	}
	fmt.Fprintf(out, "%s changed: %s\n", word(answer["id"]), changed)
	return nil
}

func newUnsubscribeCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "subscription-delete <id>",
		Short: "Remove a subscription",
		Long: "Remove one subscription by the id `subscription-list` shows. Only your own: a " +
			"subscription of another assistant is theirs, and the refusal says so. To change one " +
			"instead of taking it away, `subscription-edit`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUnsubscribe(cmd.OutOrStdout(), *opts, args[0])
		},
	}
}

func runUnsubscribe(out io.Writer, opts inspectOptions, id string) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	if _, err := client.PostForm(assistantSubscriptionsPath, url.Values{
		"form": {"remove"},
		"id":   {strings.TrimSpace(id)},
	}, actionTimeout); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s is removed\n", strings.TrimSpace(id))
	return nil
}
