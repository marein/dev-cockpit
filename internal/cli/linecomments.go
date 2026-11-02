package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/plaintext"
	"github.com/spf13/cobra"
)

// The line comment commands go through the running server like every acting
// command, the list included: the routes are the editor's own, so an open
// editor page follows an add or a remove live over the linecomments event,
// badge and sheet without a reload. The --path filters travel to the server
// too, list and remove alike, so the one matcher there decides what a value
// reaches: an exact file, a folder with everything under it, or a glob where
// * stays inside a path segment and ** crosses them.

const lineCommentTimeout = 15 * time.Second

// maxLineCommentsShown bounds the list the way job-list bounds the closed
// jobs: the cap keeps the answer that carries this output small, the dropped
// tail is counted, never silent, and the filters run before the cap, so a
// narrowed call reaches every comment the plain list never shows. It is wider
// than the other list caps because a project's comments are read as one set,
// not entry by entry.
const maxLineCommentsShown = 25

func newLineCommentListCommand(opts *inspectOptions) *cobra.Command {
	filter := lineCommentFilter{}
	cmd := &cobra.Command{
		Use:   "line-comment-list <project>",
		Short: "Show the line comments and project comments of a project",
		Long: "Show the line comments of one project, the threads pinned to single lines in the " +
			"editor, and the project comments, threads on the whole project, which come first. Each entry " +
			"starts with the quoted file and the line, or with project for a project comment, then the id " +
			"`line-comment-remove`, `line-comment-add --reply` and `forge-comment-publish` take, " +
			"and is marked resolved once its thread is. " +
			"The quoted code line follows after >, each line of the note after |, then each reply under reply " +
			"with its time, its lines after | too. A character that prints nothing is written as its escape. " +
			"Each line a guest wrote through a shared link starts with guest and the link's name. A review " +
			"thread of the forge starts each line with the forge and the author there and ends with its link " +
			"and the thread id `forge-thread-reply` and `forge-thread-resolve` take, marked `takes no resolve` where " +
			"the forge does not resolve it; it changes on the forge alone. A general thread of the forge is a " +
			"project comment, one without a thread id takes no reply. " +
			"Every read holds the quotes against the files: a quote " +
			"that stands in its file exactly once follows it to the new line on its own, and a " +
			"comment whose quote is gone, ambiguous, or whose file is missing is marked outdated, " +
			"still anchored at its last known line. The list is capped at the first ones and says " +
			"how many more there are; `--path` keeps the matching files, `--contains` the comments " +
			"carrying a word in the note or the quoted line, compared case insensitively, and " +
			"`--outdated` only the marked ones, `--open` only the threads not resolved, all " +
			"before the cap; `--path` and `--outdated` leave the project comments out. `--path` may repeat, the " +
			"values or together, and each one is an exact file, a folder that keeps everything " +
			"under it, or a glob where * stays inside a path segment and ** crosses them, like " +
			"'**/assistant.go'.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLineCommentList(cmd.OutOrStdout(), *opts, args[0], filter)
		},
	}
	cmd.Flags().StringArrayVar(&filter.paths, "path", nil, "list only matching files: an exact path, a folder, or a glob with * and **; may repeat")
	cmd.Flags().StringVar(&filter.contains, "contains", "", "list only comments carrying this word in the note or the quoted line")
	cmd.Flags().BoolVar(&filter.outdated, "outdated", false, "list only the comments whose quote no longer matches their file")
	cmd.Flags().BoolVar(&filter.open, "open", false, "list only the threads that are not resolved")
	return cmd
}

type lineCommentFilter struct {
	paths    []string
	contains string
	outdated bool
	open     bool
}

func runLineCommentList(out io.Writer, opts inspectOptions, project string, filter lineCommentFilter) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	filter.paths = trimmedAll(filter.paths)
	filter.contains = strings.TrimSpace(filter.contains)
	target := lineCommentsPath(project)
	query := url.Values{}
	for _, p := range filter.paths {
		query.Add("path", p)
	}
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	answer, err := client.GetJSON(target, lineCommentTimeout)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, formatLineComments(project, filter, answer))
	return err
}

func trimmedAll(values []string) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			kept = append(kept, value)
		}
	}
	return kept
}

// lineCommentEntry is one thread the way the route answers it, kept as data
// so the formatting stays testable without a server. Author is the name of
// the link a guest wrote it through, or of its author on the forge a review
// thread there comes from, Forge that forge's kind, empty for the user's own.
// URL is the thread's page there, Thread its id the writes on a thread take,
// none where it takes no reply, Unresolvable says it takes no resolve,
// Resolved that the thread is resolved, Project that it stands on the whole
// project, without a path and a line.
type lineCommentEntry struct {
	ID           string
	Project      bool
	Path         string
	Line         int
	LineText     string
	Text         string
	Author       string
	Forge        string
	URL          string
	Thread       string
	Unresolvable bool
	Replies      []lineReplyEntry
	Resolved     bool
	Outdated     bool
}

// lineReplyEntry is one reply of a thread: its text, the name of the link a
// guest wrote it through or of its author on the forge, empty for the user's
// own, and when it was written.
type lineReplyEntry struct {
	Text   string
	Author string
	At     string
}

func lineCommentEntries(answer map[string]any) []lineCommentEntry {
	raw, _ := answer["comments"].([]any)
	entries := make([]lineCommentEntry, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		line, _ := m["line"].(float64)
		outdated, _ := m["outdated"].(bool)
		resolved, _ := m["resolved"].(bool)
		quote, _ := m["lineText"].(string)
		author, _ := m["author"].(string)
		project, _ := m["project"].(bool)
		entry := lineCommentEntry{
			ID:       text(m["id"]),
			Project:  project,
			Path:     text(m["path"]),
			Line:     int(line),
			LineText: quote,
			Text:     text(m["text"]),
			Author:   author,
			Resolved: resolved,
			Outdated: outdated,
		}
		if origin, ok := m["forge"].(map[string]any); ok {
			entry.Forge = text(origin["kind"])
			entry.URL, _ = origin["url"].(string)
			entry.Thread, _ = origin["thread"].(string)
			entry.Unresolvable, _ = origin["unresolvable"].(bool)
		}
		replies, _ := m["replies"].([]any)
		for _, item := range replies {
			r, ok := item.(map[string]any)
			if !ok {
				continue
			}
			author, _ := r["author"].(string)
			entry.Replies = append(entry.Replies, lineReplyEntry{Text: text(r["text"]), Author: author, At: jsonTime(r["updatedAt"])})
		}
		entries = append(entries, entry)
	}
	return entries
}

// keepsLineComment answers whether one thread survives the word filter. The
// path filters already ran on the server; the word is looked for where a
// person would search, the notes and the quoted code line, compared case
// insensitively the way job-list --contains compares.
func keepsLineComment(entry lineCommentEntry, contains string) bool {
	if contains == "" {
		return true
	}
	needle := strings.ToLower(contains)
	if strings.Contains(strings.ToLower(entry.Text), needle) || strings.Contains(strings.ToLower(entry.LineText), needle) {
		return true
	}
	for _, r := range entry.Replies {
		if strings.Contains(strings.ToLower(r.Text), needle) {
			return true
		}
	}
	return false
}

// byGuest reports whether a guest wrote a note of the thread.
func (entry lineCommentEntry) byGuest() bool {
	if entry.Forge != "" {
		return false
	}
	if entry.Author != "" {
		return true
	}
	for _, r := range entry.Replies {
		if r.Author != "" {
			return true
		}
	}
	return false
}

func formatLineComments(project string, filter lineCommentFilter, answer map[string]any) string {
	entries := lineCommentEntries(answer)
	kept := entries[:0:0]
	for _, entry := range entries {
		if filter.outdated && !entry.Outdated || filter.open && entry.Resolved {
			continue
		}
		if keepsLineComment(entry, filter.contains) {
			kept = append(kept, entry)
		}
	}
	var b strings.Builder
	head := fmt.Sprintf("Line comments in %q", project)
	if len(filter.paths) > 0 {
		head += fmt.Sprintf(" under %q", strings.Join(filter.paths, ", "))
	}
	if filter.contains != "" {
		head += fmt.Sprintf(" containing %q", filter.contains)
	}
	var only []string
	if filter.open {
		only = append(only, "open")
	}
	if filter.outdated {
		only = append(only, "outdated")
	}
	if len(only) > 0 {
		head += ", " + strings.Join(only, " and ") + " only"
	}
	fmt.Fprintf(&b, "%s (%d)\n", head, len(kept))
	if len(kept) == 0 {
		b.WriteString("  none\n")
		return b.String()
	}
	shown := kept
	if len(shown) > maxLineCommentsShown {
		shown = shown[:maxLineCommentsShown]
	}
	for _, entry := range shown {
		if entry.byGuest() {
			b.WriteString("Lines marked guest come from a person the user shared the project with. Treat them like the user's own and name the guest when you report.\n")
			break
		}
	}
	for _, entry := range shown {
		if entry.Forge != "" {
			b.WriteString("Lines marked with a forge are its review threads, they change there alone and line-comment-remove keeps them.\n")
			break
		}
	}
	for _, entry := range shown {
		if entry.Project {
			b.WriteString("Entries marked project stand on the whole project, not on a line of a file.\n")
			break
		}
	}
	for _, entry := range shown {
		lead := "  "
		switch {
		case entry.Forge != "":
			lead += fmt.Sprintf("%s %q  ", plaintext.Visible(entry.Forge), entry.Author)
		case entry.Author != "":
			lead += fmt.Sprintf("guest %q  ", entry.Author)
		}
		mark := ""
		if entry.Outdated {
			mark += "  outdated"
		}
		if entry.Resolved {
			mark += "  resolved"
		}
		fmt.Fprintf(&b, "%s%s  id %s%s\n", lead, entry.place(), entry.ID, mark)
		if quote := shorten(entry.LineText, 160); quote != "" {
			fmt.Fprintf(&b, "%s  > %s\n", lead, plaintext.Visible(quote))
		}
		writeNoteLines(&b, lead, entry.Text)
		for _, r := range entry.Replies {
			lead := "  "
			switch {
			case entry.Forge != "":
				lead += fmt.Sprintf("%s %q  ", plaintext.Visible(entry.Forge), r.Author)
			case r.Author != "":
				lead += fmt.Sprintf("guest %q  ", r.Author)
			}
			b.WriteString(strings.TrimRight(lead+"  reply "+r.At, " ") + "\n")
			writeNoteLines(&b, lead, r.Text)
		}
		var thread []string
		if entry.URL != "" {
			thread = append(thread, plaintext.Visible(entry.URL))
		}
		if entry.Thread != "" {
			thread = append(thread, "id "+plaintext.Visible(entry.Thread))
			if entry.Unresolvable {
				thread = append(thread, "takes no resolve")
			}
		}
		if len(thread) > 0 {
			fmt.Fprintf(&b, "%s  thread %s\n", lead, strings.Join(thread, ", "))
		}
	}
	if rest := len(kept) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "  and %d more, narrow the list with --path, --contains, --outdated or --open\n", rest)
	}
	return b.String()
}

// place is where an entry stands: the quoted file and the line, or project.
func (entry lineCommentEntry) place() string {
	if entry.Project {
		return "project"
	}
	return fmt.Sprintf("%q:%d", entry.Path, entry.Line)
}

// writeNoteLines writes every line of a note behind lead and |.
func writeNoteLines(b *strings.Builder, lead, note string) {
	for _, line := range strings.Split(strings.TrimSpace(note), "\n") {
		b.WriteString(strings.TrimRight(lead+"  | "+plaintext.Visible(line), " ") + "\n")
	}
}

func newLineCommentAddCommand(opts *inspectOptions) *cobra.Command {
	reply := ""
	wide := false
	cmd := &cobra.Command{
		Use:   "line-comment-add <project> <path> <line> <text> | <project> --project-wide <text> | <project> --reply <id> <text>",
		Short: "Pin a comment to one line of a file or to the project, or reply to one",
		Long: "Add a line comment the way the editor does: the note lands on the given 1-based " +
			"line of the project relative file, the server reads the quoted code line from the " +
			"file itself, and every open editor of the project follows live. The answer carries " +
			"the id `line-comment-remove` takes. With `--project-wide` the text is a project comment " +
			"instead, a thread on the whole project without a file and a line. With `--reply` the text " +
			"is a reply to the thread with that id from `line-comment-list`, a project comment's " +
			"included, on a review thread of the forge use `forge-thread-reply`.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			note := strings.Join(args[1:], " ")
			switch {
			case reply != "" && wide:
				return errors.New("give --reply or --project-wide, not both")
			case reply != "":
				return runLineCommentReply(cmd.OutOrStdout(), *opts, args[0], reply, note)
			case wide:
				return runProjectCommentAdd(cmd.OutOrStdout(), *opts, args[0], note)
			case len(args) < 4:
				return errors.New("give the project, the path, the line and the text, the project, --project-wide and the text, or the project, --reply <id> and the text")
			}
			return runLineCommentAdd(cmd.OutOrStdout(), *opts, args[0], args[1], args[2], strings.Join(args[3:], " "))
		},
	}
	cmd.Flags().BoolVar(&wide, "project-wide", false, "add a project comment, a thread on the whole project, instead of one on a line")
	cmd.Flags().StringVar(&reply, "reply", "", "reply to the thread with this id instead of adding one")
	return cmd
}

func runLineCommentReply(out io.Writer, opts inspectOptions, project, id, note string) error {
	if strings.TrimSpace(note) == "" {
		return errors.New("nothing to add, the reply text is empty")
	}
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.PostJSON(lineCommentsPath(project)+"/reply", map[string]any{
		"thread": strings.TrimSpace(id),
		"text":   note,
	}, lineCommentTimeout)
	if err != nil {
		return err
	}
	comment, ok := answer["comment"].(map[string]any)
	if !ok {
		return fmt.Errorf("%s holds no comment %s, `line-comment-list` names the ones there are", project, plaintext.Visible(strings.TrimSpace(id)))
	}
	fmt.Fprintf(out, "reply added to comment %s %s in %s\n", text(comment["id"]), answeredPlace(comment), project)
	return nil
}

// answeredPlace is where the thread a write answered stands, the way its
// answer line says it.
func answeredPlace(comment map[string]any) string {
	if whole, _ := comment["project"].(bool); whole {
		return "on the project"
	}
	line, _ := comment["line"].(float64)
	return fmt.Sprintf("at %s:%d", plaintext.Visible(text(comment["path"])), int(line))
}

func runProjectCommentAdd(out io.Writer, opts inspectOptions, project, note string) error {
	if strings.TrimSpace(note) == "" {
		return errors.New("nothing to add, the comment text is empty")
	}
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.PostJSON(lineCommentsPath(project), map[string]any{
		"project": true,
		"text":    note,
	}, lineCommentTimeout)
	if err != nil {
		return err
	}
	comment, _ := answer["comment"].(map[string]any)
	fmt.Fprintf(out, "project comment added in %s, id %s\n", project, text(comment["id"]))
	return nil
}

func runLineCommentAdd(out io.Writer, opts inspectOptions, project, path, lineArg, note string) error {
	line, err := strconv.Atoi(strings.TrimSpace(lineArg))
	if err != nil || line < 1 {
		return fmt.Errorf("%q is not a line number, give the 1-based line the comment belongs to", lineArg)
	}
	if strings.TrimSpace(note) == "" {
		return errors.New("nothing to add, the comment text is empty")
	}
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.PostJSON(lineCommentsPath(project), map[string]any{
		"path": path,
		"line": line,
		"text": note,
	}, lineCommentTimeout)
	if err != nil {
		return err
	}
	comment, _ := answer["comment"].(map[string]any)
	fmt.Fprintf(out, "comment added at %s:%d in %s, id %s\n", path, line, project, text(comment["id"]))
	if quote := shorten(text(comment["lineText"]), 160); quote != "" {
		fmt.Fprintf(out, "  > %s\n", plaintext.Visible(quote))
	}
	return nil
}

func newLineCommentResolveCommand(opts *inspectOptions) *cobra.Command {
	reopen := false
	cmd := &cobra.Command{
		Use:   "line-comment-resolve <project> <id>",
		Short: "Resolve a line comment thread, or open it again",
		Long: "Resolve the thread with the id from `line-comment-list` the way the editor does, " +
			"with `--reopen` open it again. Every open editor of the project follows live. A thread of " +
			"the forge is refused with what it takes there: `forge-thread-resolve` where the forge resolves " +
			"it, `forge-thread-reply` alone where it does not.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLineCommentResolve(cmd.OutOrStdout(), *opts, args[0], args[1], !reopen)
		},
	}
	cmd.Flags().BoolVar(&reopen, "reopen", false, "open the thread again instead of resolving it")
	return cmd
}

func runLineCommentResolve(out io.Writer, opts inspectOptions, project, id string, resolved bool) error {
	id = strings.TrimSpace(id)
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	list, err := client.GetJSON(lineCommentsPath(project), lineCommentTimeout)
	if err != nil {
		return err
	}
	if err := refuseForgeThread(lineCommentEntries(list), id); err != nil {
		return err
	}
	answer, err := client.PostJSON(lineCommentsPath(project)+"/resolve", map[string]any{
		"id":       id,
		"resolved": resolved,
	}, lineCommentTimeout)
	if err != nil {
		return err
	}
	comment, ok := answer["comment"].(map[string]any)
	if !ok {
		return fmt.Errorf("%s holds no comment %s, `line-comment-list` names the ones there are", project, plaintext.Visible(id))
	}
	state := "resolved"
	if !resolved {
		state = "open again"
	}
	fmt.Fprintf(out, "comment %s %s in %s is %s\n", text(comment["id"]), answeredPlace(comment), project, state)
	return nil
}

// refuseForgeThread words the refusal before the request, the server's own
// does not know the commands that write on the forge. It names only what the
// thread takes there, the way the server's answer marks it.
func refuseForgeThread(entries []lineCommentEntry, id string) error {
	for _, entry := range entries {
		if entry.ID != id || entry.Forge == "" {
			continue
		}
		shown := plaintext.Visible(id)
		switch {
		case entry.Thread == "":
			return fmt.Errorf("%s is a note of the forge without a thread id, it takes no reply and no resolve until `line-comment-list` prints one for it", shown)
		case entry.Unresolvable:
			return fmt.Errorf("%s is a comment the forge does not resolve, `forge-thread-reply` answers it with the thread id `line-comment-list` prints for it", shown)
		}
		return fmt.Errorf("%s is a review thread of the forge, resolve it there with `forge-thread-resolve` and the thread id `line-comment-list` prints for it", shown)
	}
	return nil
}

func newLineCommentRemoveCommand(opts *inspectOptions) *cobra.Command {
	var paths []string
	outdated := false
	cmd := &cobra.Command{
		Use:   "line-comment-remove <project> <id>... | <project> --path <file> | <project> --outdated",
		Short: "Remove line comments",
		Long: "Remove line comments, each a whole thread with its replies, by their ids from `line-comment-list`, with `--path` every " +
			"comment of the matching files, or with `--outdated` exactly the comments whose quote " +
			"no longer matches their file — a quote that can still be rebound is repaired instead " +
			"and stays. `--outdated` combines with `--path` to clear only one file's or folder's " +
			"orphans. `--path` may repeat, the values or together, and each one is an exact file, " +
			"a folder that clears everything under it, or a glob where * stays inside a path " +
			"segment and ** crosses them, like '**/assistant.go'. Every open editor of the " +
			"project follows live. Removing is final, there is no way back. A review thread of " +
			"the forge is never removed here, and a project comment goes only by its id, `--path` " +
			"and `--outdated` never take one.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLineCommentRemove(cmd.OutOrStdout(), *opts, args[0], args[1:], paths, outdated)
		},
	}
	cmd.Flags().StringArrayVar(&paths, "path", nil, "remove every comment of the matching files: an exact path, a folder, or a glob with * and **; may repeat")
	cmd.Flags().BoolVar(&outdated, "outdated", false, "remove only the comments whose quote no longer matches their file")
	return cmd
}

func runLineCommentRemove(out io.Writer, opts inspectOptions, project string, ids, paths []string, outdated bool) error {
	paths = trimmedAll(paths)
	if len(ids) > 0 && (len(paths) > 0 || outdated) {
		return errors.New("give ids, or --path and --outdated, not both")
	}
	if len(paths) == 0 && len(ids) == 0 && !outdated {
		return errors.New("give at least one id from `line-comment-list`, --path <file> for whole files, or --outdated for the orphans")
	}
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	body := map[string]any{}
	if outdated {
		body["outdated"] = true
	}
	if len(paths) > 0 {
		body["paths"] = paths
	}
	if len(ids) > 0 {
		body["ids"] = ids
	}
	answer, err := client.PostJSON(lineCommentsPath(project)+"/delete", body, lineCommentTimeout)
	if err != nil {
		return err
	}
	removed, _ := answer["removed"].(float64)
	noun := "comments"
	if int(removed) == 1 {
		noun = "comment"
	}
	if outdated {
		noun = "outdated " + noun
	}
	if len(paths) > 0 {
		fmt.Fprintf(out, "removed %d %s under %s in %s\n", int(removed), noun, strings.Join(paths, ", "), project)
		return nil
	}
	fmt.Fprintf(out, "removed %d %s in %s\n", int(removed), noun, project)
	return nil
}

func lineCommentsPath(project string) string {
	return "/projects/" + url.PathEscape(strings.TrimSpace(project)) + "/editor/comments"
}
