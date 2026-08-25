package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
	coderclaude "github.com/marein/dev-cockpit/internal/coder/claude"
	codercopilot "github.com/marein/dev-cockpit/internal/coder/copilot"
	coderopencode "github.com/marein/dev-cockpit/internal/coder/opencode"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/recent"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/terminal"
	"github.com/marein/dev-cockpit/internal/terminalstate"
	"github.com/marein/dev-cockpit/internal/tmux"
	"github.com/spf13/cobra"
)

// The inspection commands are the cockpit's read only view of itself, meant to
// be run by the assistant while it answers. They never write: the state
// belongs to the serve process, and a second writer would bypass its caches
// and its event stream. They read the same files and the same tmux the server
// reads, so a fresh process always sees the current picture.

// inspectOptions is which cockpit a command talks to. One instance is shared by
// the whole assistant command group, filled from its persistent flags.
type inspectOptions struct {
	stateDir    string
	projectsDir string
}

func newStatusCommand(opts *inspectOptions) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the coders, shells and projects of a running cockpit",
		Long: "Show what the cockpit holds right now: running coders and shells, " +
			"conversations that can be resumed, which of them have unread news, and the projects. " +
			"The inactive coders are capped at the recent ones; `--all` lists every one. " +
			"Reads only, changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			limit := maxInactiveShown
			if all {
				limit = 0
			}
			return runStatus(cmd.OutOrStdout(), *opts, limit)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "list every inactive coder instead of the recent ones")
	return cmd
}

// defaultOutputLines is how much of a terminal the assistant reads by default:
// enough to see what a coder said last, small enough to stay a fraction of a
// turn's context.
const defaultOutputLines = 120

func newOutputCommand(opts *inspectOptions) *cobra.Command {
	lines := defaultOutputLines
	cmd := &cobra.Command{
		Use:   "terminal-screen <terminal>",
		Short: "Show what a coder or shell has on screen",
		Long: "Show the last lines of a terminal the cockpit runs: what a coder said, " +
			"what it is waiting for, what a command printed. The terminal is an id from " +
			"`status`. Reads only, and it never resizes the terminal.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOutput(cmd.OutOrStdout(), *opts, args[0], lines)
		},
	}
	cmd.Flags().IntVar(&lines, "lines", lines, "how many lines to show")
	return cmd
}

func runOutput(out io.Writer, opts inspectOptions, rawTarget string, lines int) error {
	target, err := terminal.ValidateIdentifier(rawTarget)
	if err != nil {
		return err
	}
	text, err := tmux.New().CapturePane(target, lines)
	if err != nil {
		return screenError(opts, target, err)
	}
	if strings.TrimSpace(text) == "" {
		fmt.Fprintf(out, "Terminal %s has nothing on screen.\n", target)
		return nil
	}
	fmt.Fprintln(out, text)
	return nil
}

// screenError says why a terminal's screen could not be read, and what can be
// done about it. What tmux says is that it found no session, which leaves the
// caller guessing whether the terminal is gone for good, so the id is
// classified against the same coders and shells the server holds and the answer
// names `coder-resume` where it is the way back. A cockpit this process cannot
// even read falls back to what tmux said, which is all there is then.
func screenError(opts inspectOptions, target string, capture error) error {
	picture, err := openTerminals(opts)
	if err != nil {
		return fmt.Errorf("no terminal %q is running: %s", target, firstLine(capture.Error()))
	}
	return errors.New(screenErrorMessage(target,
		terminalstate.Classify(target, picture.coderLookups(), picture.shells), capture))
}

// screenErrorMessage words one classified id for this command. There is no
// branch for a shell that is gone: a shell leaves nothing behind, so an id no
// shell runs under is indistinguishable from an id nobody ever used, and this
// command takes any id. The shell's own sentence lives on the shell route,
// which knows what it was asked about.
func screenErrorMessage(target string, state terminalstate.State, capture error) string {
	switch state {
	case terminalstate.Running:
		return fmt.Sprintf("terminal %q is running, but reading its screen failed: %s",
			target, firstLine(capture.Error()))
	case terminalstate.Resumable:
		return fmt.Sprintf("no terminal %q is running, its coder session is stopped: `coder-resume %s` brings it back",
			target, target)
	default:
		return fmt.Sprintf("no terminal %q is running, and no session with that id can be resumed", target)
	}
}

// newActivityCommand reads through the running server, unlike the other
// inspection commands: which coder owns a session and how its record is read
// live there, and a second reader would have to repeat both. It still only
// reads, the route changes nothing.
func newActivityCommand(opts *inspectOptions) *cobra.Command {
	entries := 0
	full := false
	cmd := &cobra.Command{
		Use:   "coder-activity <terminal>",
		Short: "Show what a session last did, from its own record",
		Long: "Ask the coder what one of its sessions last did and whether its turn is over, " +
			"read from the session's own record instead of the screen. The screen carries the " +
			"coder's input line with the coder's own draft in it; the record does not, and it " +
			"is still there after the terminal stopped. The reading is capped by default, and " +
			"a cut entry says how much of the message is shown; `--full` lifts the cap and " +
			"composes with `--entries`: `--entries 1 --full` is the whole last message. " +
			"Reads only, changes nothing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivity(cmd.OutOrStdout(), *opts, args[0], entries, full)
		},
	}
	cmd.Flags().IntVar(&entries, "entries", 0, "how many recorded messages to show (default the coder's own cap)")
	cmd.Flags().BoolVar(&full, "full", false, "show the messages whole, without the cap on the reading")
	return cmd
}

func runActivity(out io.Writer, opts inspectOptions, target string, entries int, full bool) error {
	client, err := localapi.Dial(opts.stateDir)
	if err != nil {
		return err
	}
	path := "/coders/" + strings.TrimSpace(target) + "/activity"
	query := ""
	if entries > 0 {
		query = "entries=" + strconv.Itoa(entries)
	}
	if full {
		if query != "" {
			query += "&"
		}
		query += "full=1"
	}
	if query != "" {
		path += "?" + query
	}
	answer, err := client.GetJSON(path, activityTimeout)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, formatActivity(strings.TrimSpace(target), answer))
	return err
}

// activityTimeout is generous because the screen fallback reads a terminal
// three times with a settle gap between the reads.
const activityTimeout = 30 * time.Second

// formatActivity renders the answer of the activity route. A reading that came
// from the screen says so before the text: it carries the coder's input line,
// and whoever reads it has to know that the draft there is nobody's message.
func formatActivity(target string, answer map[string]any) string {
	text, _ := answer["text"].(string)
	finished, _ := answer["finished"].(bool)
	screen, _ := answer["screen"].(bool)

	var b strings.Builder
	if screen {
		state := "the picture was still changing while it was read"
		if finished {
			state = "the picture stood still while it was read"
		}
		fmt.Fprintf(&b, "Session %s keeps no record of its own, so this is its screen; %s.\n", target, state)
		b.WriteString("The line at the bottom is the coder's input box, and whatever stands in it is the coder's own draft, not a message.\n\n")
	} else {
		state := "its turn is still running"
		if finished {
			state = "its turn is over"
		}
		fmt.Fprintf(&b, "What session %s last recorded, newest last; %s.\n\n", target, state)
	}
	if strings.TrimSpace(text) == "" {
		b.WriteString("This session has not recorded anything yet.\n")
		return b.String()
	}
	b.WriteString(strings.TrimSpace(text))
	b.WriteString("\n")
	return b.String()
}

// The conversation commands read through the running server, like coder-activity:
// which conversations exist and what stands in them lives in the assistant
// service, and a second reader would have to repeat its rules. They still only
// read, the routes change nothing.

// conversationTimeout bounds the two conversation reads. Generous, because a
// contains search reads every transcript once.
const conversationTimeout = 30 * time.Second

// maxConversationsShown bounds the conversation list the same way the status
// list bounds the inactive coders: the recent ones carry the information, the
// tail is only cost, and what is dropped is printed as a count, never silently.
const maxConversationsShown = maxInactiveShown

func newConversationsCommand(opts *inspectOptions) *cobra.Command {
	contains := ""
	cmd := &cobra.Command{
		Use:   "conversation-list",
		Short: "Show the assistant's own recent conversations",
		Long: "Show the conversations the assistant had with the user, newest first: the id, " +
			"the title, the coder, when the last message was, and a preview. The list is capped " +
			"at the recent ones and says how many older ones it dropped. `--contains` keeps only " +
			"the conversations where a word appears in the title or in a message, compared case " +
			"insensitively, and the cap applies after that. Reads only, changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConversations(cmd.OutOrStdout(), *opts, contains)
		},
	}
	cmd.Flags().StringVar(&contains, "contains", "", "list only conversations carrying this word in the title or a message")
	return cmd
}

func runConversations(out io.Writer, opts inspectOptions, contains string) error {
	client, err := localapi.Dial(opts.stateDir)
	if err != nil {
		return err
	}
	path := "/assistant/conversations"
	contains = strings.TrimSpace(contains)
	if contains != "" {
		path += "?contains=" + url.QueryEscape(contains)
	}
	answer, err := client.GetJSON(path, conversationTimeout)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, formatConversations(contains, answer))
	return err
}

// formatConversations renders the conversation list, at most
// maxConversationsShown entries, the dropped tail counted. The cap sits here
// and not in the route, like the status cap: the route reports what there is,
// the command decides what a page of it may cost.
func formatConversations(contains string, answer map[string]any) string {
	raw, _ := answer["conversations"].([]any)
	var b strings.Builder
	if contains != "" {
		fmt.Fprintf(&b, "Assistant conversations containing %q (%d)\n", contains, len(raw))
	} else {
		fmt.Fprintf(&b, "Assistant conversations (%d)\n", len(raw))
	}
	if len(raw) == 0 {
		b.WriteString("  none\n")
		return b.String()
	}
	shown := raw
	if len(shown) > maxConversationsShown {
		shown = shown[:maxConversationsShown]
	}
	for _, entry := range shown {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		title, _ := m["title"].(string)
		coder, _ := m["coderId"].(string)
		id, _ := m["id"].(string)
		fmt.Fprintf(&b, "  %-9s %s", orDash(coder), quoted(title))
		if when := jsonTime(m["lastMessageAt"]); when != "" {
			fmt.Fprintf(&b, "  last message %s", when)
		}
		fmt.Fprintf(&b, "  id %s\n", id)
		if preview, _ := m["preview"].(string); strings.TrimSpace(preview) != "" {
			fmt.Fprintf(&b, "    %s\n", preview)
		}
	}
	if rest := len(raw) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "  and %d older, narrow the list with --contains\n", rest)
	}
	return b.String()
}

func newConversationCommand(opts *inspectOptions) *cobra.Command {
	entries := 0
	full := false
	cmd := &cobra.Command{
		Use:   "conversation-show <id>",
		Short: "Show the messages of one assistant conversation",
		Long: "Show what was said in one of the assistant's own conversations, newest last, " +
			"each message with its role. The id is from `conversation-list`. The reading is capped " +
			"by default, and a cut message says how much of it is shown; `--full` lifts the cap " +
			"and composes with `--entries`: `--entries 1 --full` is the whole last message. " +
			"Reads only, changes nothing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConversation(cmd.OutOrStdout(), *opts, args[0], entries, full)
		},
	}
	cmd.Flags().IntVar(&entries, "entries", 0, "how many messages to show (default the recent ones)")
	cmd.Flags().BoolVar(&full, "full", false, "show the messages whole, without the cap on each one")
	return cmd
}

func runConversation(out io.Writer, opts inspectOptions, id string, entries int, full bool) error {
	client, err := localapi.Dial(opts.stateDir)
	if err != nil {
		return err
	}
	path := "/assistant/conversations/" + strings.TrimSpace(id)
	query := ""
	if entries > 0 {
		query = "entries=" + strconv.Itoa(entries)
	}
	if full {
		if query != "" {
			query += "&"
		}
		query += "full=1"
	}
	if query != "" {
		path += "?" + query
	}
	answer, err := client.GetJSON(path, conversationTimeout)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, formatConversation(answer))
	return err
}

// formatConversation renders one transcript: a line saying what is shown, then
// the messages, newest last, each under a line naming who said it and when.
func formatConversation(answer map[string]any) string {
	title, _ := answer["title"].(string)
	coder, _ := answer["coderId"].(string)
	raw, _ := answer["messages"].([]any)
	dropped := jsonCount(answer["dropped"])
	total := jsonCount(answer["messageCount"])

	var b strings.Builder
	fmt.Fprintf(&b, "Conversation %s", quoted(title))
	if coder != "" {
		fmt.Fprintf(&b, " with coder %s", coder)
	}
	fmt.Fprintf(&b, ", %d messages.\n", total)
	if dropped > 0 {
		fmt.Fprintf(&b, "Showing the last %d; %d older are not shown, --entries brings them back.\n", len(raw), dropped)
	}
	if len(raw) == 0 {
		b.WriteString("This conversation has no messages yet.\n")
		return b.String()
	}
	for _, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		header := role
		if when := jsonTime(m["createdAt"]); when != "" {
			header += " " + when
		}
		fmt.Fprintf(&b, "\n[%s]\n", header)
		content, _ := m["content"].(string)
		if strings.TrimSpace(content) == "" {
			b.WriteString("(no text)\n")
			continue
		}
		b.WriteString(strings.TrimSpace(content))
		b.WriteString("\n")
	}
	return b.String()
}

// jsonTime renders a timestamp the way the other lists do. The JSON answer
// carries RFC 3339; anything else prints as nothing rather than as a guess.
func jsonTime(v any) string {
	s, _ := v.(string)
	t, err := time.Parse(time.RFC3339, s)
	if err != nil || t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

// jsonCount reads a number out of a JSON answer, where every number decodes as
// a float64.
func jsonCount(v any) int {
	f, _ := v.(float64)
	return int(f)
}

func newNotificationsCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "notification-list",
		Short: "Show the unread notifications of a running cockpit",
		Long: "Show what asked for attention while nobody was looking: the unread notifications, " +
			"newest first. Reads only, and it does not mark anything read.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNotifications(cmd.OutOrStdout(), *opts)
		},
	}
}

func newJobsCommand(opts *inspectOptions) *cobra.Command {
	var filter jobsFilter
	since := ""
	cmd := &cobra.Command{
		Use:   "job-list",
		Short: "Show the coders the assistant is steering",
		Long: "Show the steered jobs: what each one has to reach, what is left of its checks " +
			"and of its time, and what the last check said. A steered terminal is the " +
			"assistant's to write into; every other terminal belongs to the user. " +
			"Every open job is listed; the closed ones are capped at the recent ones, " +
			"counted per state under their header, and a long criterion is cut and says so. " +
			"`--contains`, `--state` and `--since` narrow the list before the cap, so they " +
			"reach jobs the capped list never shows; `--full` prints the criteria whole and " +
			"composes with them; `--all` lifts the cap. Reads only, changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ready, err := filter.parse(since, time.Now())
			if err != nil {
				return err
			}
			return runJobs(cmd.OutOrStdout(), *opts, ready)
		},
	}
	cmd.Flags().StringVar(&filter.Contains, "contains", "", "list only jobs carrying this word in the name, task, criterion or last check")
	cmd.Flags().StringVar(&filter.State, "state", "", "list only jobs in this state (steering, done, blocked, expired, stopped)")
	cmd.Flags().StringVar(&since, "since", "", "list only jobs that changed since then, a span like 24h or a date like 2026-03-04")
	cmd.Flags().BoolVar(&filter.All, "all", false, "list every closed job instead of the recent ones")
	cmd.Flags().BoolVar(&filter.Full, "full", false, "show the criteria whole, without the cut")
	return cmd
}

// jobLine is one job in the job-list output.
type jobLine struct {
	Terminal  string
	Name      string
	Project   string
	Coder     string
	State     string
	Open      bool
	Wakes     int
	MaxWakes  int
	ExpiresAt time.Time
	// ChangedAt is when something last happened on this job, which is what
	// `--since` asks about: the last write, or when it was made if it was never
	// written to again.
	ChangedAt time.Time
	Task      string
	DoneWhen  string
	Note      string
	NoteAt    time.Time
}

// jobsFilter is what a job-list call asks for. The store keeps far more jobs than
// a list may print, and the cap alone made everything behind it unreachable,
// including the terminal ids `job-show` needs. So the filters run before the cap:
// a narrowed call reaches jobs the plain one never shows.
type jobsFilter struct {
	Contains string
	State    string
	// Since is the moment a job must have changed after, already resolved from
	// the flag, and SinceRaw is what the caller wrote, for the line that says
	// what was filtered.
	Since    time.Time
	SinceRaw string
	All      bool
	Full     bool
}

// jobStates are the states a job can be in, in the order the counts are
// printed. It is also what `--state` accepts, so a typo is refused with the
// list instead of printing an empty report.
var jobStates = []string{"steering", "done", "blocked", "expired", "stopped"}

// parse resolves the flags into what the filter matches on, and refuses what
// it cannot: a state that does not exist, and a `--since` that is neither a
// span nor a date.
func (f jobsFilter) parse(since string, now time.Time) (jobsFilter, error) {
	f.Contains = strings.TrimSpace(f.Contains)
	f.State = strings.ToLower(strings.TrimSpace(f.State))
	if f.State != "" && !slices.Contains(jobStates, f.State) {
		return f, fmt.Errorf("No job is in state %q. The states are %s.", f.State, strings.Join(jobStates, ", "))
	}
	f.SinceRaw = strings.TrimSpace(since)
	if f.SinceRaw == "" {
		return f, nil
	}
	moment, err := parseSince(f.SinceRaw, now)
	if err != nil {
		return f, err
	}
	f.Since = moment
	return f, nil
}

// keeps answers whether one job survives the filter. The word is looked for in
// what a person would search by: the coder's name, the task, the criterion and
// what the last check said. Compared case insensitively, the way
// `conversation-list --contains` compares.
func (f jobsFilter) keeps(job jobLine) bool {
	if f.State != "" && job.State != f.State {
		return false
	}
	if !f.Since.IsZero() && job.ChangedAt.Before(f.Since) {
		return false
	}
	if f.Contains == "" {
		return true
	}
	needle := strings.ToLower(f.Contains)
	for _, field := range []string{job.Name, job.Task, job.DoneWhen, job.Note} {
		if strings.Contains(strings.ToLower(field), needle) {
			return true
		}
	}
	return false
}

// on reports whether anything was narrowed at all.
func (f jobsFilter) on() bool {
	return f.Contains != "" || f.State != "" || !f.Since.IsZero()
}

// parseSince reads what a caller writes for a point in time: a span back from
// now, "24h", or a date, with or without the time of day. Both forms exist
// because both questions do, "what happened while I was away" and "what
// happened on Tuesday". A date is read in the local zone, which is the one the
// output prints in.
func parseSince(raw string, now time.Time) (time.Time, error) {
	if span, err := time.ParseDuration(raw); err == nil {
		if span <= 0 {
			return time.Time{}, fmt.Errorf("A --since of %q looks backwards. Give a span back from now, like 24h.", raw)
		}
		return now.Add(-span), nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
		if moment, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return moment, nil
		}
	}
	return time.Time{}, fmt.Errorf("A --since of %q is neither a span nor a date. Write a span like 24h or 90m, or a date like 2026-03-04, optionally with a time: \"2026-03-04 09:30\".", raw)
}

// jobsReport is everything the job-list command prints, kept as data so the
// formatting stays testable without a state directory.
type jobsReport struct {
	Now    time.Time
	Jobs   []jobLine
	Filter jobsFilter
	// Dropped is how many jobs the filter took out, so the output can say that
	// the list is a part of what is stored and not all of it.
	Dropped int
}

func runJobs(out io.Writer, opts inspectOptions, filter jobsFilter) error {
	stateDir, err := filesystem.ExpandHome(opts.stateDir)
	if err != nil {
		return fmt.Errorf("failed to read the cockpit configuration: %w", err)
	}
	jobs := assistant.NewJobStore(stateDir).List()
	// The same order the conversation and the page use, from the same function:
	// open jobs first, then the newest.
	assistant.SortJobs(jobs)

	report := jobsReport{Now: time.Now(), Filter: filter}
	for _, job := range jobs {
		line := jobLineFrom(job)
		if !filter.keeps(line) {
			report.Dropped++
			continue
		}
		report.Jobs = append(report.Jobs, line)
	}
	_, err = io.WriteString(out, formatJobs(report))
	return err
}

func jobLineFrom(job assistant.Job) jobLine {
	changed := job.UpdatedAt
	if changed.IsZero() {
		changed = job.CreatedAt
	}
	return jobLine{
		Terminal:  job.Terminal,
		Name:      job.Name,
		Project:   job.Project,
		Coder:     job.CoderID,
		State:     string(job.State),
		Open:      job.State.Open(),
		Wakes:     job.Wakes,
		MaxWakes:  job.MaxWakes,
		ExpiresAt: job.ExpiresAt,
		ChangedAt: changed,
		Task:      job.Task,
		DoneWhen:  job.DoneWhen,
		Note:      job.Note,
		NoteAt:    job.NoteAt,
	}
}

func newJobCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "job-show <terminal>",
		Short: "Show one job in full",
		Long: "Show everything one job holds, nothing cut: the criterion, the task, " +
			"the state, the spent checks, the time left, and the whole report the " +
			"last check left. `job-list` keeps its list short on purpose; this is where a single " +
			"job is looked up. Reads only, changes nothing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJob(cmd.OutOrStdout(), *opts, args[0])
		},
	}
}

func runJob(out io.Writer, opts inspectOptions, terminal string) error {
	stateDir, err := filesystem.ExpandHome(opts.stateDir)
	if err != nil {
		return fmt.Errorf("failed to read the cockpit configuration: %w", err)
	}
	terminal = strings.TrimSpace(terminal)
	for _, job := range assistant.NewJobStore(stateDir).List() {
		if job.Terminal == terminal {
			_, err = io.WriteString(out, formatJob(time.Now(), jobLineFrom(job)))
			return err
		}
	}
	return fmt.Errorf("No job steers %q. `job-list` lists the terminals that have one.", terminal)
}

// formatJob prints one job whole. The list output cuts the note and folds the
// criterion so a page of jobs stays readable; this is the counterpart that
// never cuts and never folds, because a single job was asked for, its last
// report is the answer, and a multi line criterion is a list of checks that
// reads as such.
func formatJob(now time.Time, job jobLine) string {
	var b strings.Builder
	name := job.Name
	if name == "" {
		name = job.Terminal
	}
	fmt.Fprintf(&b, "Job %s\n", quoted(name))
	fmt.Fprintf(&b, "  terminal  %s\n", job.Terminal)
	if job.Coder != "" {
		fmt.Fprintf(&b, "  coder     %s\n", job.Coder)
	}
	if job.Project != "" {
		fmt.Fprintf(&b, "  project   %s\n", job.Project)
	}
	fmt.Fprintf(&b, "  state     %s\n", job.State)
	fmt.Fprintf(&b, "  checks    %d/%d used\n", job.Wakes, job.MaxWakes)
	fmt.Fprintf(&b, "  time      %s\n", jobDeadline(now, job))
	if job.Task != "" {
		fmt.Fprintf(&b, "\ntask: %s\n", strings.TrimSpace(job.Task))
	}
	fmt.Fprintf(&b, "\ndone when: %s\n", strings.TrimSpace(assistant.DoneWhenLine(job.DoneWhen)))
	if job.Note != "" {
		when := ""
		if !job.NoteAt.IsZero() {
			when = " (" + job.NoteAt.Local().Format("2006-01-02 15:04") + ")"
		}
		fmt.Fprintf(&b, "\nlast check%s: %s\n", when, strings.TrimSpace(job.Note))
	}
	return b.String()
}

// maxClosedJobsShown bounds the closed tail, and maxJobNoteRunes both the note
// and the criterion of one job, for the same reason the resumable list is
// bounded: a host collects jobs for weeks, the report a check leaves is a
// paragraph, a criterion may be a whole page, and the assistant reads this
// output inside an answer. What is dropped is printed as a count and every cut
// says how much is missing, never silently.
//
// The criterion is cut here, which the list refused to do for a long time: a
// check that reads half a sentence judges against half a sentence. That is
// still true and it is not about this output. A check gets its criterion from
// the store, in its own prompt, whole or refused at the door, and never from
// what this command prints. What is printed here is read by somebody deciding
// where to look, and for that the first lines carry the job. `--full` prints
// them whole, and `job-show <terminal>` is the uncut single view.
const (
	maxClosedJobsShown = 5
	maxJobNoteRunes    = 240
)

func formatJobs(r jobsReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Steered jobs at %s%s\n", r.Now.Format("2006-01-02 15:04"), filterLine(r))

	var open, closed []jobLine
	for _, job := range r.Jobs {
		if job.Open {
			open = append(open, job)
			continue
		}
		closed = append(closed, job)
	}

	fmt.Fprintf(&b, "\nSteering (%d)\n", len(open))
	if len(open) == 0 {
		b.WriteString("  nothing is being steered\n")
	}
	for _, job := range open {
		writeJobLines(&b, r.Now, job, r.Filter.Full)
	}

	fmt.Fprintf(&b, "\nClosed (%d)\n", len(closed))
	if len(closed) == 0 {
		b.WriteString("  none\n")
	}
	// The counts answer what a hundred closed jobs are without printing one of
	// them: how a job ended is the whole question most of the time, and
	// `--state` turns any of these numbers back into a list.
	if counts := stateCounts(closed); counts != "" {
		fmt.Fprintf(&b, "  %s\n", counts)
	}
	shown := closed
	if !r.Filter.All && len(shown) > maxClosedJobsShown {
		shown = shown[:maxClosedJobsShown]
	}
	for _, job := range shown {
		writeJobLines(&b, r.Now, job, r.Filter.Full)
	}
	if rest := len(closed) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "  and %d older, --contains, --state and --since search past this, --all lists every one\n", rest)
	}

	b.WriteString("\nA steering job means the assistant holds that terminal and may write into it. " +
		"Every other terminal belongs to the user.\n")
	return b.String()
}

// filterLine says what was narrowed and how much it left out, so a short list
// is never read as everything that is stored.
func filterLine(r jobsReport) string {
	if !r.Filter.on() {
		return ""
	}
	var parts []string
	if r.Filter.Contains != "" {
		parts = append(parts, fmt.Sprintf("containing %q", r.Filter.Contains))
	}
	if r.Filter.State != "" {
		parts = append(parts, "in state "+r.Filter.State)
	}
	if !r.Filter.Since.IsZero() {
		parts = append(parts, "changed since "+r.Filter.Since.Local().Format("2006-01-02 15:04"))
	}
	return fmt.Sprintf(", %s (%d other jobs are not shown)", strings.Join(parts, ", "), r.Dropped)
}

// stateCounts is how the closed jobs ended, one number per state that occurs.
func stateCounts(closed []jobLine) string {
	if len(closed) == 0 {
		return ""
	}
	seen := map[string]int{}
	for _, job := range closed {
		seen[job.State]++
	}
	var parts []string
	for _, state := range jobStates {
		if n := seen[state]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", state, n))
			delete(seen, state)
		}
	}
	// A state an older build stored still gets counted, under its own name.
	rest := make([]string, 0, len(seen))
	for state := range seen {
		rest = append(rest, state)
	}
	sort.Strings(rest)
	for _, state := range rest {
		parts = append(parts, fmt.Sprintf("%s %d", state, seen[state]))
	}
	return strings.Join(parts, ", ")
}

func writeJobLines(b *strings.Builder, now time.Time, job jobLine, full bool) {
	name := job.Name
	if name == "" {
		name = job.Terminal
	}
	fmt.Fprintf(b, "  %-9s %-16s %-16s checks %d/%d  %s  id %s\n",
		job.State, quoted(name), orDash(job.Project),
		job.Wakes, job.MaxWakes, jobDeadline(now, job), job.Terminal)
	// A criterion may be stored as several lines, one check per line; the list
	// folds them to one line and `job-show` shows them as they stand. A job without
	// one is judged against the session's own task, and the line says so.
	doneWhen := foldLine(assistant.DoneWhenLine(job.DoneWhen))
	if !full {
		doneWhen = cutRunes(doneWhen, maxJobNoteRunes, "--full shows the criteria whole")
	}
	fmt.Fprintf(b, "    done when: %s\n", doneWhen)
	if job.Note != "" {
		fmt.Fprintf(b, "    last check: %s\n", shorten(job.Note, maxJobNoteRunes))
	}
}

// foldLine folds text onto one line for a list: whitespace and line breaks
// collapse to single spaces, nothing is cut.
func foldLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// shorten cuts a note to what a list can carry, on a rune boundary and visibly,
// so nobody reads a cut line as the whole text. The full wording stands in the
// conversation and in what the terminal itself shows.
func shorten(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

// cutRunes is shorten for a text whose rest is worth fetching: it says how much
// is shown and which flag brings the rest, the way a cut message in a
// conversation does. An ellipsis alone tells a reader that something is missing
// and not what to do about it.
func cutRunes(text string, limit int, how string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return fmt.Sprintf("%s… [cut: %d of %d runes shown, %s]",
		strings.TrimSpace(string(runes[:limit])), limit, len(runes), how)
}

// jobDeadline is what is left of a job's time, in the form a reader can act on.
// A job that is closed has none left, and its state already says what became
// of it.
func jobDeadline(now time.Time, job jobLine) string {
	if !job.Open {
		return "closed"
	}
	if job.ExpiresAt.IsZero() {
		return "no time limit"
	}
	left := job.ExpiresAt.Sub(now).Round(time.Minute)
	if left <= 0 {
		return "time is up"
	}
	if hours := int(left / time.Hour); hours > 0 {
		return fmt.Sprintf("%dh %02dm left", hours, int(left/time.Minute)%60)
	}
	return fmt.Sprintf("%dm left", int(left/time.Minute))
}

// terminalLine is one coder or shell in the status output.
type terminalLine struct {
	Kind    string // coder or shell
	Coder   string
	ID      string
	Name    string
	Project string
	News    bool
	// Steered says an open job hangs on this terminal, so the assistant holds
	// it and may write into it.
	Steered bool
	When    time.Time
}

// statusReport is everything the status command prints, kept as data so the
// formatting stays testable without a host, a tmux or a state directory.
type statusReport struct {
	Now      time.Time
	Running  []terminalLine
	Inactive []terminalLine
	Projects []projectLine
	Unread   int
}

type projectLine struct {
	Name   string
	Branch string
}

// maxInactiveShown bounds the resumable list by default. A host collects
// conversations for months, and the assistant reads this output inside an
// answer: the recent ones carry the information, the tail is only cost. The
// full count is still printed, so nothing is hidden silently, and `--all`
// lifts the cap for the one answer that needs the tail.
const maxInactiveShown = 10

// terminals is the cockpit of one state directory as a fresh process can build
// it for itself: the same coder managers and the same shells the serve process
// holds, reading the same tmux and the same state files. `status` lists from it
// and `terminal-screen` classifies an id against it, so neither needs a running
// cockpit to answer, and the two can never come to different conclusions about
// what a session is.
type terminals struct {
	cfg      config.Config
	projects *project.Repository
	coders   []*coder.Manager
	shells   *shell.Shells
}

func openTerminals(opts inspectOptions) (terminals, error) {
	options := defaultOptions()
	options.StateDir = opts.stateDir
	options.ProjectsDir = opts.projectsDir
	cfg, err := config.Load(options)
	if err != nil {
		return terminals{}, fmt.Errorf("failed to read the cockpit configuration: %w", err)
	}
	projectRepo := project.NewRepository(cfg.ProjectsRoot, recent.New(filepath.Join(cfg.StateDir, "recent-projects.json")))
	tmuxClient := tmux.New()
	hidden := reservedSessions(cfg.StateDir)

	out := terminals{cfg: cfg, projects: projectRepo}
	registry := coder.NewRegistry(codercopilot.New(), coderclaude.New(notify.InboxDir(cfg.StateDir, "claude")), coderopencode.New(notify.InboxDir(cfg.StateDir, "opencode")))
	for _, c := range registry.All() {
		if len(clirun.MissingTools(c.RequiredTools())) > 0 {
			continue
		}
		manager := coder.NewManager(cfg, tmuxClient, c, projectRepo)
		manager.SetHidden(func(sessionID string) bool { return hidden[sessionID] })
		out.coders = append(out.coders, manager)
	}
	out.shells = shell.NewShells(cfg, tmuxClient, projectRepo, func() bool { return false })
	return out, nil
}

// available reports whether this host has tmux at all. Without it nothing can be
// running, so there is nothing to list and nothing to look up.
func (t terminals) available() bool {
	return len(clirun.MissingTools(tmux.RequiredTools)) == 0
}

// coderLookups hands the managers to the classifier, which reads two of their
// methods and nothing else.
func (t terminals) coderLookups() []terminalstate.CoderLookup {
	out := make([]terminalstate.CoderLookup, 0, len(t.coders))
	for _, manager := range t.coders {
		out = append(out, manager)
	}
	return out
}

func runStatus(out io.Writer, opts inspectOptions, inactiveLimit int) error {
	picture, err := openTerminals(opts)
	if err != nil {
		return err
	}
	cfg, projectRepo := picture.cfg, picture.projects
	news := notify.NewService(notify.StorePath(cfg.StateDir), nil)
	unread := news.UnreadTargets()
	steered := steeredTerminals(cfg.StateDir)

	report := statusReport{Now: time.Now(), Unread: news.UnreadCount()}
	for _, p := range projectRepo.List() {
		branch := p.GitBranch
		if !p.GitRepo {
			branch = ""
		}
		report.Projects = append(report.Projects, projectLine{Name: p.Name, Branch: branch})
	}

	if picture.available() {
		for _, manager := range picture.coders {
			snap := manager.Snapshot()
			for _, r := range snap.Running {
				report.Running = append(report.Running, terminalLine{
					Kind: "coder", Coder: manager.ID(), ID: r.Identifier, Name: r.Name,
					Project: projectRepo.ProjectNameFor(r.CWD), News: unread[r.Identifier],
					Steered: steered[r.Identifier], When: r.StartedAt,
				})
			}
			for _, r := range snap.Inactive {
				report.Inactive = append(report.Inactive, terminalLine{
					Kind: "coder", Coder: manager.ID(), ID: r.SessionID, Name: r.Name,
					Project: projectRepo.ProjectNameFor(r.CWD), News: unread[r.SessionID],
					Steered: steered[r.SessionID], When: r.UpdatedAt,
				})
			}
		}
		for _, sh := range picture.shells.List() {
			report.Running = append(report.Running, terminalLine{
				Kind: "shell", ID: sh.Identifier, Name: sh.Name,
				Project: projectRepo.ProjectNameFor(sh.CWD), News: unread[sh.Identifier],
				Steered: steered[sh.Identifier], When: sh.StartedAt,
			})
		}
	}

	sortTerminals(report.Running)
	// Newest first, because only the head of this list is printed.
	sort.SliceStable(report.Inactive, func(i, j int) bool {
		return report.Inactive[i].When.After(report.Inactive[j].When)
	})
	_, err = io.WriteString(out, formatStatus(report, inactiveLimit))
	return err
}

// reservedSessions collects the provider sessions that belong to an assistant
// conversation, so they are not reported as coders anybody could resume. It
// mirrors the filter the server installs.
func reservedSessions(stateDir string) map[string]bool {
	out := map[string]bool{}
	store := assistant.NewStore(stateDir)
	for _, entry := range store.List() {
		if entry.Status == assistant.StatusActive {
			out[entry.ID] = true
		}
	}
	return out
}

// steeredTerminals reports which terminals an open job steers, read from
// the file the server writes, so the status output and the job-list command can
// never come to different conclusions about what is steered.
func steeredTerminals(stateDir string) map[string]bool {
	out := map[string]bool{}
	for _, job := range assistant.NewJobStore(stateDir).List() {
		if job.State.Open() {
			out[job.Terminal] = true
		}
	}
	return out
}

func sortTerminals(lines []terminalLine) {
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].Project != lines[j].Project {
			return lines[i].Project < lines[j].Project
		}
		return lines[i].When.After(lines[j].When)
	})
}

// formatStatus renders the report, printing at most inactiveLimit inactive
// coders; zero or less prints them all.
func formatStatus(r statusReport, inactiveLimit int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Cockpit status at %s\n", r.Now.Format("2006-01-02 15:04"))

	fmt.Fprintf(&b, "\nRunning (%d)\n", len(r.Running))
	if len(r.Running) == 0 {
		b.WriteString("  nothing is running\n")
	}
	for _, line := range r.Running {
		writeTerminalLine(&b, line, "")
	}

	fmt.Fprintf(&b, "\nInactive coders (%d)\n", len(r.Inactive))
	if len(r.Inactive) == 0 {
		b.WriteString("  none\n")
	}
	shown := r.Inactive
	if inactiveLimit > 0 && len(shown) > inactiveLimit {
		shown = shown[:inactiveLimit]
	}
	for _, line := range shown {
		writeTerminalLine(&b, line, "last used")
	}
	if rest := len(r.Inactive) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "  and %d older, ask the user before digging them out\n", rest)
	}

	fmt.Fprintf(&b, "\nProjects (%d)\n", len(r.Projects))
	if len(r.Projects) == 0 {
		b.WriteString("  none\n")
	}
	for _, p := range r.Projects {
		if p.Branch != "" {
			fmt.Fprintf(&b, "  %s (%s)\n", p.Name, p.Branch)
			continue
		}
		fmt.Fprintf(&b, "  %s\n", p.Name)
	}

	fmt.Fprintf(&b, "\nUnread notifications: %d\n", r.Unread)
	return b.String()
}

func writeTerminalLine(b *strings.Builder, line terminalLine, whenLabel string) {
	kind := line.Kind
	if line.Coder != "" {
		kind = line.Kind + " " + line.Coder
	}
	fmt.Fprintf(b, "  %-14s %-16s %s", kind, orDash(line.Project), quoted(line.Name))
	if line.News {
		b.WriteString("  [news]")
	}
	if line.Steered {
		// A job hangs on this terminal, so the assistant holds it. The full
		// picture is one command away, see `dev-cockpit assistant job-list`.
		b.WriteString("  [steered]")
	}
	if whenLabel != "" && !line.When.IsZero() {
		fmt.Fprintf(b, "  %s %s", whenLabel, line.When.Local().Format("2006-01-02 15:04"))
	}
	fmt.Fprintf(b, "  id %s\n", line.ID)
}

func runNotifications(out io.Writer, opts inspectOptions) error {
	stateDir, err := filesystem.ExpandHome(opts.stateDir)
	if err != nil {
		return fmt.Errorf("failed to read the cockpit configuration: %w", err)
	}
	service := notify.NewService(notify.StorePath(stateDir), nil)
	var unread []notify.Notification
	for _, n := range service.List(0) {
		if !n.Read {
			unread = append(unread, n)
		}
	}
	_, err = io.WriteString(out, formatNotifications(unread))
	return err
}

func formatNotifications(unread []notify.Notification) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Unread notifications (%d)\n", len(unread))
	if len(unread) == 0 {
		b.WriteString("  nothing new\n")
		return b.String()
	}
	for _, n := range unread {
		title := n.Title
		if title == "" {
			title = fmt.Sprintf("Something new in %s", quoted(n.TargetName))
		}
		// The title says what happened and nothing else, the name of the coder,
		// shell, job or backup stands in the entry's second line, so a line
		// without it would name nothing at all.
		if n.Detail != "" {
			title += "  " + n.Detail
		}
		fmt.Fprintf(&b, "  %s  %-16s %s  %s\n",
			n.CreatedAt.Local().Format("2006-01-02 15:04"), orDash(n.Project), title, n.URL)
	}
	return b.String()
}

// firstLine keeps a tmux error to the sentence a reader needs, the rest is the
// command line that produced it.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func quoted(s string) string {
	if strings.TrimSpace(s) == "" {
		return `""`
	}
	return `"` + s + `"`
}
