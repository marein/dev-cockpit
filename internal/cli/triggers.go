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

// The triggers: how an assistant hangs itself onto an event and acts when it
// fires. Every command goes over the socket to the same handlers the page
// posts to, so what the page can react to the assistant can, with the same
// filters and the same bounds, and nothing is page only.

// assistantTriggersPath is where the triggers live, the same path the page's
// list and form use.
const assistantTriggersPath = "/assistants/triggers"

func newTriggerNewCommand(opts *inspectOptions) *cobra.Command {
	var a triggerArgs
	cmd := &cobra.Command{
		Use:   "trigger-new <event>",
		Short: "React to an event: a note in your thread and a turn of yours",
		Long: "Add a trigger on an event. When it fires, the reaction runs in a session of its own, so " +
			"the task must be self contained; its answer is pushed into your " +
			"thread, marked as started without the user. Answer NOTHING on the first line when there " +
			"is nothing to do or say, then nothing is pushed and nobody is notified. The events are " +
			strings.Join(eventNames(), ", ") + ". Three of them stand over several kinds: job-closed takes every " +
			"way a job ends, coder-news every signal a coder sends, a turn that ended and a question " +
			"alike; there is no narrower coder event, and which of the two arrived stands in the event's " +
			"headline; and compose-ended every way a compose command you started ends, done, failed or " +
			"declined, the three narrow compose events taking one each. A compose event is about a run " +
			"and takes no `--terminal`. `--name` is optional, at most " + strconv.Itoa(assistant.MaxTriggerNameRunes) +
			" runes; with one the row and `trigger-list` read by it. `--terminal` narrows a job or coder event to one " +
			"terminal and may be repeated for several, left out it is any job of yours or any " +
			"coder; a job event only ever reaches the assistant whose job it is. With several " +
			"terminals any of them fires it; `--all` makes them a barrier instead, one turn once " +
			"every one of them produced an event, with every report in it. A barrier belongs on " +
			"job-closed: on job-done it waits forever for a job that closes blocked. A terminal " +
			"deleted while a barrier waits counts as arrived, and the barrier goes with the last " +
			"of its terminals. `cron` takes `--cron` with five crontab fields and `--tz` " +
			"with the zone those wall clock times are read in, an IANA name like Europe/Berlin, " +
			"never an offset. Without `--tz` the schedule takes the stored zone, and without a stored " +
			"one this server's; the answer names the zone that was applied and the next tick in " +
			"it, and `timezone-set` is what moves the stored one. The wall clock is taken as it " +
			"stands: a local time the spring changeover skips matches no minute and the schedule " +
			"falls out once, an hour the autumn changeover repeats matches twice and the schedule " +
			"fires twice. `--batch` on a schedule is ignored and the answer says " +
			"so. `--model` runs the reaction on a model of its own, a name the coder takes, the " +
			"ones the ring button's list offers among them; without it the trigger runs on the " +
			"assistant default, evaluated when it fires: your Triggers pick at the ring where one " +
			"stands, else Same as chat, your chat model as it stands then, which is what most " +
			"triggers want and what `assistant-models-get` shows. A cheaper model for a task that mostly answers " +
			"NOTHING is what the flag is for. The answer names the id `trigger-edit` and " +
			"`trigger-delete` take.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a.event = args[0]
			return runTriggerNew(cmd.OutOrStdout(), *opts, a)
		},
	}
	triggerFlags(cmd, &a)
	return cmd
}

type triggerArgs struct {
	event, name, spec, zone, task, until, batch, model string
	terminals                                          []string
	once, all                                          bool
}

// triggerFlags are the fields of a trigger, the same set on the command that
// makes one and on the command that changes one: one wiring, so a flag cannot
// mean two things or go missing on one of them. What they do to a change is
// decided by whether they were named, see runTriggerEdit.
func triggerFlags(cmd *cobra.Command, a *triggerArgs) {
	cmd.Flags().StringVar(&a.name, "name", "", "a short name for the trigger, two or three words for what it is for; the row reads by it")
	cmd.Flags().StringArrayVar(&a.terminals, "terminal", nil, "a terminal the event is about (job and coder events), repeatable, default any")
	cmd.Flags().BoolVar(&a.all, "all", false, "fire once every named terminal produced an event, not on the first one")
	cmd.Flags().StringVar(&a.spec, "cron", "", "the schedule of a cron event, five crontab fields like \"*/30 9-17 * * 1-5\"")
	cmd.Flags().StringVar(&a.zone, "tz", "", "the zone a schedule's times are read in, an IANA name like Europe/Berlin (default: the stored zone, see timezone-set)")
	cmd.Flags().StringVar(&a.task, "task", "", "what to do when the event fires (required)")
	cmd.Flags().BoolVar(&a.once, "once", false, "end the trigger after its first turn")
	cmd.Flags().StringVar(&a.until, "until", "", "expiry as a span from now, like 8h or 2d; never for none (default: none, it stands until removed)")
	cmd.Flags().StringVar(&a.batch, "batch", "", "how long to collect events into one turn, like 30s (default 30s; ignored for cron)")
	cmd.Flags().StringVar(&a.model, "model", "", "the model the reaction runs on, a name the coder takes (default: the assistant default, evaluated when it fires, your Triggers pick at the ring, else Same as chat; on an edit, default clears it back to that)")
}

// modelField is what a model flag posts: the name, or the empty field for
// `default`, the word that clears a trigger's own model back to the
// assistant default, the way `never` clears an expiry.
func modelField(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "default") {
		return ""
	}
	return raw
}

// eventNames are the events a trigger may name, in the order the surfaces list
// them.
func eventNames() []string {
	out := make([]string, 0, len(assistant.EventOptions))
	for _, k := range assistant.EventOptions {
		out = append(out, k.Name())
	}
	return out
}

func runTriggerNew(out io.Writer, opts inspectOptions, a triggerArgs) error {
	if _, err := assistant.ParseEventOption(a.event); err != nil {
		return err
	}
	if strings.TrimSpace(a.task) == "" {
		return fmt.Errorf("A trigger needs --task: what to do when the event fires.")
	}
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	form := url.Values{
		"form":     {"new"},
		"name":     {strings.TrimSpace(a.name)},
		"event":    {strings.TrimSpace(a.event)},
		"spec":     {strings.TrimSpace(a.spec)},
		"timezone": {strings.TrimSpace(a.zone)},
		"task":     {strings.TrimSpace(a.task)},
		"until":    {strings.TrimSpace(a.until)},
		"batch":    {strings.TrimSpace(a.batch)},
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
	if model := modelField(a.model); model != "" {
		form.Set("model", model)
	}
	answer, err := client.PostForm(assistantTriggersPath, form, actionTimeout)
	if err != nil {
		return err
	}
	io.WriteString(out, addedLine(modelField(a.model), answer))
	io.WriteString(out, scheduleLine(answer))
	io.WriteString(out, ignoredLine(answer))
	return nil
}

// addedLine is the first line trigger-new prints. The model stands on it only
// when the call passed one that made a difference, one the reaction would not
// have run on without the flag (the cockpit answers what it would have been
// as modelDefault), and then as the cockpit answered it, the way coder-new's
// started line names it, so a turn reports the model that runs and never the
// one it asked for; without the flag, or with the flag naming the default
// anyway, the line stays as it was.
func addedLine(model string, answer map[string]any) string {
	line := fmt.Sprintf("added %s: %s", word(answer["id"]), word(answer["summary"]))
	if model != "" {
		on, _ := answer["model"].(string)
		if on == "" {
			on = model
		}
		if without, _ := answer["modelDefault"].(string); on != without {
			line += " on " + on
		}
	}
	return line + "\n"
}

// ignoredLine says what the call named that the trigger has no use for, so a
// bound somebody passed is never read back off the row to find out it was
// dropped. The server decides it, the two surfaces only word it: a schedule
// has no batch window, and the form's field is gone the moment one is picked.
func ignoredLine(answer map[string]any) string {
	if word(answer["ignored"]) != "batch" {
		return ""
	}
	return "--batch is ignored: a schedule has no batch window\n"
}

// scheduleLine is what a schedule answers beyond its id: the zone that was
// really applied, and the next tick written out in it. The zone alone would
// leave a person and a model with the crontab fields to work out by hand, and
// that is exactly the arithmetic a changeover gets wrong; the timestamp does
// the work once, here, and it carries its zone so the line stands on its own
// wherever it is quoted. Empty for every trigger that is not a schedule.
func scheduleLine(answer map[string]any) string {
	zone := word(answer["timezone"])
	next := stamp(answer["nextAt"])
	if zone == "" || next.IsZero() {
		return ""
	}
	return fmt.Sprintf("zone %s, next %s\n", zone, zonedStamp(next, zone))
}

// zonedStamp is a moment as the wall clock of a zone reads it, with the zone
// named. A schedule is a statement about wall clock time, so every line that
// shows one of its ticks says which clock it read.
func zonedStamp(t time.Time, zone string) string {
	loc, err := assistant.LoadZone(zone)
	if err != nil {
		return t.Local().Format("2006-01-02 15:04")
	}
	return t.In(loc).Format("2006-01-02 15:04") + " " + zone
}

func newTriggerListCommand(opts *inspectOptions) *cobra.Command {
	var filter triggerFilter
	since := ""
	cmd := &cobra.Command{
		Use:   "trigger-list",
		Short: "Show the events an assistant reacts to",
		Long: "Show the triggers: its name where it has one with the event under it and the event " +
			"itself where it has none, its task, whether it is standing, a one " +
			"shot or over, how often it fired, the next tick of a schedule in the zone that schedule " +
			"is read in and when it expires, " +
			"each with the id `trigger-edit` and `trigger-delete` take. It lists your " +
			"own; `--assistant all` " +
			"lists every assistant's, each line naming whose it is, and `--assistant <id>` one " +
			"other's. Run outside a turn it lists everybody's. " +
			"Every standing trigger is listed; the spent ones are capped at the recent ones and " +
			"stand as their state and their last fire alone, the way a closed job stands in `job-list`, " +
			"and a long task is cut and says so. `--contains` and `--since` narrow the list before the " +
			"cap, so they reach triggers the capped list never shows; `--since` is what \"what " +
			"happened yesterday\" asks for, and a trigger counts " +
			"as changed by every fire, every end and every edit; `--full` prints the tasks whole, " +
			"gives a spent trigger its task and its bounds back and composes with them; `--all` " +
			"lifts the cap. Reads only, changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ready, err := filter.parse(since, time.Now())
			if err != nil {
				return err
			}
			return runTriggerList(cmd.OutOrStdout(), *opts, ready)
		},
	}
	cmd.Flags().StringVar(&filter.Assistant, "assistant", "", "whose triggers to list: an assistant id, or all (default: your own)")
	cmd.Flags().StringVar(&filter.Contains, "contains", "", "list only triggers carrying this word in the name, the event, the task or the last fire")
	cmd.Flags().StringVar(&since, "since", "", "list only triggers that changed since then, a span like 24h or a date like 2026-03-04")
	cmd.Flags().BoolVar(&filter.All, "all", false, "list every spent trigger instead of the recent ones")
	cmd.Flags().BoolVar(&filter.Full, "full", false, "show the tasks whole, without the cut")
	return cmd
}

// triggerFilter is what a reading of the triggers asks for. The word and the
// cap are the server's, the way `assistant-show` has them narrowed before the
// window is taken: the route holds every trigger, and a list capped in here
// could not be searched past. Full is the printer's alone, it only lifts the
// cut on the task.
type triggerFilter struct {
	Assistant string
	Contains  string
	// Since is the moment a trigger must have moved after, already resolved
	// from the flag. A trigger moves on every fire, every end and every edit,
	// so this is what "what happened yesterday" asks about.
	Since time.Time
	All   bool
	Full  bool
}

// parse resolves the flag into what the filter matches on and refuses what it
// cannot read, the way `job-list --since` refuses it and through the very same
// parser: a span back from now, or a date. The moment is what travels to the
// server, so a caller writes what a person writes and the refusal lands where
// the flag was typed.
func (f triggerFilter) parse(since string, now time.Time) (triggerFilter, error) {
	f.Contains = strings.TrimSpace(f.Contains)
	since = strings.TrimSpace(since)
	if since == "" {
		return f, nil
	}
	moment, err := parseSince(since, now)
	if err != nil {
		return f, err
	}
	f.Since = moment
	return f, nil
}

func runTriggerList(out io.Writer, opts inspectOptions, filter triggerFilter) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	query := url.Values{}
	if whose := strings.TrimSpace(filter.Assistant); whose != "" {
		query.Set("assistant", whose)
	}
	if filter.Contains != "" {
		query.Set("contains", filter.Contains)
	}
	if !filter.Since.IsZero() {
		query.Set("since", filter.Since.UTC().Format(time.RFC3339))
	}
	if filter.All {
		query.Set("all", "1")
	}
	path := assistantTriggersPath
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	answer, err := client.GetJSON(path, inputTimeout)
	if err != nil {
		return err
	}
	report := triggerReport{Now: time.Now(), Filter: filter}
	report.Owners, _ = answer["owners"].(bool)
	report.Zone = word(answer["timezone"])
	report.Stored, _ = answer["timezoneStored"].(bool)
	report.Dropped = count(answer["dropped"])
	report.Older = count(answer["older"])
	rows, _ := answer["triggers"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		report.Rows = append(report.Rows, row)
	}
	_, err = io.WriteString(out, formatTriggers(report))
	return err
}

// triggerReport is everything a listing prints: the rows the server answered,
// and what its word and its cap left out, so a short list is never read as
// every trigger there is.
type triggerReport struct {
	Now     time.Time
	Rows    []map[string]any
	Owners  bool
	Filter  triggerFilter
	Dropped int
	Older   int
	// Zone is what a schedule made now would be read in, and Stored whether
	// anybody said so or it is only this server's own setting. The two are
	// apart because they say different things: a stored zone is where the
	// user said they are, an unstored one is only the host's setting.
	Zone   string
	Stored bool
}

// formatTriggers prints the two groups the way `job-list` prints the open jobs
// and the closed ones, and `status` its running and inactive sessions: every
// standing trigger, then the spent tail capped, with a line saying how many
// the cap held back and what searches past it.
func formatTriggers(r triggerReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Triggers at %s%s\n", r.Now.Format("2006-01-02 15:04"), triggerFilterLine(r))
	if line := triggerZoneLine(r); line != "" {
		b.WriteString(line)
	}

	var standing, spent []map[string]any
	for _, row := range r.Rows {
		if open, _ := row["open"].(bool); open {
			standing = append(standing, row)
			continue
		}
		spent = append(spent, row)
	}

	fmt.Fprintf(&b, "\nStanding (%d)\n", len(standing))
	if len(standing) == 0 {
		b.WriteString("nothing is waiting for an event\n")
	}
	for _, row := range standing {
		printTrigger(&b, row, r)
	}

	fmt.Fprintf(&b, "\nSpent (%d)\n", len(spent)+r.Older)
	if len(spent)+r.Older == 0 {
		b.WriteString("none\n")
	}
	for _, row := range spent {
		printTrigger(&b, row, r)
	}
	if r.Older > 0 {
		fmt.Fprintf(&b, "and %d older, --contains and --since search past this, --all lists every one\n", r.Older)
	}
	return b.String()
}

// triggerZoneLine says what a schedule made now would be read in, and whether
// that is an answer somebody gave or only the zone this server happens to run
// in. A schedule is a statement about a wall clock, so the one thing a caller
// must not do is guess which clock: this line is where it reads that a zone is
// stored, and where it reads that nobody has said yet.
func triggerZoneLine(r triggerReport) string {
	if r.Zone == "" {
		return ""
	}
	if r.Stored {
		return fmt.Sprintf("New schedules are read in %s, the stored zone; timezone-set moves it.\n", r.Zone)
	}
	return fmt.Sprintf("New schedules are read in %s, this server's own zone; nobody stored one, timezone-set does.\n", r.Zone)
}

// triggerFilterLine says what was narrowed and how much it left out, the line
// `job-list` writes for the same reason.
func triggerFilterLine(r triggerReport) string {
	var parts []string
	if r.Filter.Contains != "" {
		parts = append(parts, fmt.Sprintf("containing %q", r.Filter.Contains))
	}
	if !r.Filter.Since.IsZero() {
		parts = append(parts, "changed since "+r.Filter.Since.Local().Format("2006-01-02 15:04"))
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf(", %s (%d other triggers are not shown)", strings.Join(parts, ", "), r.Dropped)
}

// printTrigger prints one trigger the way job-list prints a job: the id and
// what it reads by on the first line, the rest indented under it, and a spent
// one short, the way a closed job stands there. The task is cut like a job's
// criterion, `--full` prints it whole, and the id is what `trigger-edit`,
// `trigger-delete` and a `--contains` search lead back to.
func printTrigger(b *strings.Builder, row map[string]any, r triggerReport) {
	event := word(row["label"])
	if where := word(row["where"]); where != "" {
		event += ", " + where
	}
	// The name is the head where there is one and the event moves a line down,
	// the way it moves under the name in the row on the page; without a name
	// nothing changes at all, the event is the head as it always was.
	name := word(row["name"])
	head := name
	if head == "" {
		head = event
	}
	head = fmt.Sprintf("%s  %s", word(row["id"]), head)
	if r.Owners {
		head += "  (" + word(row["ownerName"]) + ")"
	}
	fmt.Fprintln(b, head)
	if name != "" {
		fmt.Fprintf(b, "  event     %s\n", event)
	}
	// A spent trigger fires nothing again, so its task and its bounds describe
	// work nobody can move any more: it stands the way a closed job stands in
	// `job-list`, its state and its last line alone, and `--full` prints both
	// back here so a `--contains` hit that fell in a task can be read where it
	// was found.
	open, _ := row["open"].(bool)
	whole := open || r.Filter.Full
	if whole {
		task := foldLine(word(row["task"]))
		if !r.Filter.Full {
			task = cutRunes(task, maxJobNoteRunes, "--full shows the tasks whole")
		}
		fmt.Fprintf(b, "  task      %s\n", task)
	}
	state := word(row["state"])
	if once, _ := row["once"].(bool); once && state == "standing" {
		state += ", once"
	}
	if reacting, _ := row["reacting"].(bool); reacting {
		state += ", reacting now"
	}
	fired := count(row["fired"])
	fmt.Fprintf(b, "  state     %s, fired %d time%s\n", state, fired, plural(fired))
	// A model of its own is the exception and stands where it is set; a
	// trigger on the assistant's chat model says nothing, that is the rule.
	if model := word(row["model"]); model != "" {
		fmt.Fprintf(b, "  model     %s\n", model)
	}
	if whole {
		var facts []string
		if next := stamp(row["nextAt"]); !next.IsZero() {
			facts = append(facts, "next "+zonedStamp(next, word(row["timezone"])))
		}
		if until := stamp(row["expiresAt"]); !until.IsZero() {
			facts = append(facts, "until "+until.Local().Format("2006-01-02 15:04"))
		} else {
			facts = append(facts, "no expiry")
		}
		if batch := count(row["batchSeconds"]); batch > 0 {
			facts = append(facts, fmt.Sprintf("batch %ds", batch))
		}
		fmt.Fprintf(b, "  bounds    %s\n", strings.Join(facts, ", "))
	}
	if missing := waitingFor(row); len(missing) > 0 {
		fmt.Fprintf(b, "  waiting   %s\n", strings.Join(missing, ", "))
	}
	if note := word(row["note"]); note != "" {
		fmt.Fprintf(b, "  last      %s\n", shorten(foldLine(note), maxJobNoteRunes))
	}
}

// waitingFor names the terminals a barrier still misses, so a trigger that has
// not fired says what it is waiting for. Empty for every other one: they fire
// on the first event, there is nothing to wait for.
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

// count reads a number out of a JSON answer, which arrives as a float, zero
// when there is none.
func count(value any) int {
	n, _ := value.(float64)
	return int(n)
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

func newTriggerEditCommand(opts *inspectOptions) *cobra.Command {
	var a triggerArgs
	cmd := &cobra.Command{
		Use:   "trigger-edit <id>",
		Short: "Change a standing trigger instead of making it again",
		Long: "Change one trigger by the id `trigger-list` shows, with the flags " +
			"`trigger-new` takes: only the flags you name change anything, everything else " +
			"stands. What can be " +
			"changed is the name with `--name` (an empty one takes the name away and the row reads by " +
			"the event again), the model with `--model` (`--model default` clears it back to " +
			"the assistant default, evaluated when it fires: your Triggers pick at the ring, else " +
			"Same as chat) and everything else the flags below name. Moving " +
			"the schedule or its zone works the next tick out again and the answer names it; " +
			"neither moves the stored zone, `timezone-set` is what does. What cannot is the event: " +
			"another event is another trigger, so make one and remove this. A trigger " +
			"that is done or expired is spent and is refused. Changing a terminal is a replacement, " +
			"`--terminal` names the whole list; one that stays keeps what it reached, so a barrier " +
			"waits on for the targets that have not arrived and counts a new one as not arrived " +
			"unless its job is already closed, and a removed one takes its state with it. What the " +
			"trigger already did is untouched: how often it fired, when it was made, the " +
			"events waiting in its window, and a reaction that runs right now, which keeps the task " +
			"it was given. Only your own, like the removal. The answer names what changed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTriggerEdit(cmd.OutOrStdout(), *opts, args[0], a, cmd.Flags().Changed)
		},
	}
	triggerFlags(cmd, &a)
	return cmd
}

// runTriggerEdit posts what was named and nothing else: a field the form does
// not carry is one nobody named, and the server leaves that one standing. That
// is the same reading a create gets, so there is one rule and one handler for
// both.
func runTriggerEdit(out io.Writer, opts inspectOptions, id string, a triggerArgs, named func(string) bool) error {
	form := url.Values{"form": {"edit"}, "id": {strings.TrimSpace(id)}}
	if named("name") {
		// An empty --name clears the name, the way the page's emptied field
		// does: the field is there and says none, rather than not being there.
		form.Set("name", strings.TrimSpace(a.name))
	}
	if named("task") {
		form.Set("task", strings.TrimSpace(a.task))
	}
	if named("model") {
		// `default` posts the empty field, which clears the trigger's own model
		// the way the page's select does with its first entry.
		form.Set("model", modelField(a.model))
	}
	if named("cron") {
		form.Set("spec", strings.TrimSpace(a.spec))
	}
	if named("tz") {
		form.Set("timezone", strings.TrimSpace(a.zone))
	}
	if named("until") {
		form.Set("until", strings.TrimSpace(a.until))
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
		return fmt.Errorf("Name what to change: --name, --task, --model, --terminal, --all, --cron, --tz, --once, --until or --batch.")
	}
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.PostForm(assistantTriggersPath, form, actionTimeout)
	if err != nil {
		return err
	}
	changed := word(answer["changed"])
	if changed == "" {
		changed = "nothing"
	}
	fmt.Fprintf(out, "%s changed: %s\n", word(answer["id"]), changed)
	io.WriteString(out, scheduleLine(answer))
	io.WriteString(out, ignoredLine(answer))
	return nil
}

func newTriggerDeleteCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "trigger-delete <id>",
		Short: "Remove a trigger",
		Long: "Remove one trigger by the id `trigger-list` shows. Only your own: a " +
			"trigger of another assistant is theirs, and the refusal says so. To change one " +
			"instead of taking it away, `trigger-edit`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTriggerDelete(cmd.OutOrStdout(), *opts, args[0])
		},
	}
}

func runTriggerDelete(out io.Writer, opts inspectOptions, id string) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	if _, err := client.PostForm(assistantTriggersPath, url.Values{
		"form": {"remove"},
		"id":   {strings.TrimSpace(id)},
	}, actionTimeout); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s is removed\n", strings.TrimSpace(id))
	return nil
}

// newTimezoneGetCommand answers where the user sits, in one line. The same
// two facts ride in every `trigger-list`, but a caller that only wants to know
// which clock a schedule would be read on would have to buy the whole list to
// read them, and "which time zone am I in" is a question the user asks on its
// own.
func newTimezoneGetCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "timezone-get",
		Short: "Say which zone new schedules are read in",
		Long: "Say the zone a schedule takes when it names none of its own, whether anybody stored " +
			"it and which zone this server runs in. Nobody has stored one until `timezone-set` is " +
			"run, and until then a schedule is read on this server's clock, which is nobody's " +
			"answer about where they are. Reads only, changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTimezoneGet(cmd.OutOrStdout(), *opts)
		},
	}
}

func runTimezoneGet(out io.Writer, opts inspectOptions) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.GetJSON(assistantTriggersPath+"?timezone=1", inputTimeout)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, timezoneLine(answer))
	return err
}

// timezoneLine is that one line. A stored zone and this server's own are two
// different facts and the sentence says which it is looking at: a stored one
// is where the user said they are, an unstored one is only the host's setting
// and says nothing about anybody.
func timezoneLine(answer map[string]any) string {
	server := word(answer["server"])
	if stored, _ := answer["stored"].(bool); stored {
		return fmt.Sprintf("New schedules are read in %s, the stored zone; this server runs in %s.\n",
			word(answer["timezone"]), server)
	}
	return fmt.Sprintf("Nobody stored a zone, so new schedules are read in %s, this server's own; timezone-set stores one.\n", server)
}

// newTimezoneSetCommand stores the zone new schedules are read in. It is a
// verb of its own and not a flag on a trigger, and that line is the whole
// point: `--tz` says where one schedule's clock hangs, this says where the
// user sits. In the browser a person picks the zone and sees the field they
// picked it in; here a turn picks one out of a sentence on their behalf, and a
// choice somebody derived must never become the default every later schedule
// starts on. Making that its own command draws the line in the tool instead of
// leaving it to a rule somebody has to remember.
func newTimezoneSetCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "timezone-set <zone>",
		Short: "Store the zone new schedules are read in",
		Long: "Store the zone a schedule takes when it names none of its own, an IANA name like " +
			"Europe/Berlin, never an offset. Use it only when the user says where they are, \"I am in " +
			"Berlin\", and never to record a zone you worked out from something they wrote. For " +
			"one schedule in another place, \"nine o'clock in New York\", pass `--tz` to " +
			"`trigger-new` instead: that is a zone for that schedule and it moves nothing here. " +
			"What already stands is untouched either way, every schedule carries the zone it was " +
			"made with. `timezone-get` says which zone is in force and whether it is stored.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTimezoneSet(cmd.OutOrStdout(), *opts, args[0])
		},
	}
}

func runTimezoneSet(out io.Writer, opts inspectOptions, zone string) error {
	zone = strings.TrimSpace(zone)
	if _, err := assistant.LoadZone(zone); err != nil {
		return err
	}
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.PostForm(assistantTriggersPath, url.Values{
		"form":     {"timezone"},
		"timezone": {zone},
	}, actionTimeout)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "new schedules are read in %s\n", word(answer["timezone"]))
	return nil
}
