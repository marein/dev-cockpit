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
	var paths []string
	contains := ""
	outdated := false
	cmd := &cobra.Command{
		Use:   "line-comment-list <project>",
		Short: "Show the line comments of a project",
		Long: "Show the line comments of one project, the notes pinned to single lines in the " +
			"editor: file and line, the quoted code line, the note, and the id " +
			"`line-comment-remove` takes. Every read holds the quotes against the files: a quote " +
			"that stands in its file exactly once follows it to the new line on its own, and a " +
			"comment whose quote is gone, ambiguous, or whose file is missing is marked outdated, " +
			"still anchored at its last known line. The list is capped at the first ones and says " +
			"how many more there are; `--path` keeps the matching files, `--contains` the comments " +
			"carrying a word in the note or the quoted line, compared case insensitively, and " +
			"`--outdated` only the marked ones, all before the cap. `--path` may repeat, the " +
			"values or together, and each one is an exact file, a folder that keeps everything " +
			"under it, or a glob where * stays inside a path segment and ** crosses them, like " +
			"'**/assistant.go'.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLineCommentList(cmd.OutOrStdout(), *opts, args[0], paths, contains, outdated)
		},
	}
	cmd.Flags().StringArrayVar(&paths, "path", nil, "list only matching files: an exact path, a folder, or a glob with * and **; may repeat")
	cmd.Flags().StringVar(&contains, "contains", "", "list only comments carrying this word in the note or the quoted line")
	cmd.Flags().BoolVar(&outdated, "outdated", false, "list only the comments whose quote no longer matches their file")
	return cmd
}

func runLineCommentList(out io.Writer, opts inspectOptions, project string, paths []string, contains string, outdated bool) error {
	client, err := localapi.Dial(opts.stateDir)
	if err != nil {
		return err
	}
	target := lineCommentsPath(project)
	query := url.Values{}
	for _, p := range trimmedAll(paths) {
		query.Add("path", p)
	}
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	answer, err := client.GetJSON(target, lineCommentTimeout)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, formatLineComments(project, trimmedAll(paths), strings.TrimSpace(contains), outdated, answer))
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

// lineCommentEntry is one comment the way the route answers it, kept as data
// so the formatting stays testable without a server.
type lineCommentEntry struct {
	ID       string
	Path     string
	Line     int
	LineText string
	Text     string
	Outdated bool
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
		entries = append(entries, lineCommentEntry{
			ID:       text(m["id"]),
			Path:     text(m["path"]),
			Line:     int(line),
			LineText: text(m["lineText"]),
			Text:     text(m["text"]),
			Outdated: outdated,
		})
	}
	return entries
}

// keepsLineComment answers whether one comment survives the word filter. The
// path filters already ran on the server; the word is looked for where a
// person would search, the note and the quoted code line, compared case
// insensitively the way job-list --contains compares.
func keepsLineComment(entry lineCommentEntry, contains string) bool {
	if contains == "" {
		return true
	}
	needle := strings.ToLower(contains)
	return strings.Contains(strings.ToLower(entry.Text), needle) ||
		strings.Contains(strings.ToLower(entry.LineText), needle)
}

func formatLineComments(project string, paths []string, contains string, onlyOutdated bool, answer map[string]any) string {
	entries := lineCommentEntries(answer)
	kept := entries[:0:0]
	for _, entry := range entries {
		if onlyOutdated && !entry.Outdated {
			continue
		}
		if keepsLineComment(entry, contains) {
			kept = append(kept, entry)
		}
	}
	var b strings.Builder
	head := fmt.Sprintf("Line comments in %q", project)
	if len(paths) > 0 {
		head += fmt.Sprintf(" under %q", strings.Join(paths, ", "))
	}
	if contains != "" {
		head += fmt.Sprintf(" containing %q", contains)
	}
	if onlyOutdated {
		head += ", outdated only"
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
		mark := ""
		if entry.Outdated {
			mark = "  outdated"
		}
		fmt.Fprintf(&b, "  %s:%d  id %s%s\n", entry.Path, entry.Line, entry.ID, mark)
		if quote := strings.TrimSpace(entry.LineText); quote != "" {
			fmt.Fprintf(&b, "    > %s\n", shorten(quote, 160))
		}
		for _, line := range strings.Split(strings.TrimSpace(entry.Text), "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}
	if rest := len(kept) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "  and %d more, narrow the list with --path, --contains or --outdated\n", rest)
	}
	return b.String()
}

func newLineCommentAddCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "line-comment-add <project> <path> <line> <text>",
		Short: "Pin a line comment to one line of a file",
		Long: "Add a line comment the way the editor does: the note lands on the given 1-based " +
			"line of the project relative file, the server reads the quoted code line from the " +
			"file itself, and every open editor of the project follows live. The answer carries " +
			"the id `line-comment-remove` takes.",
		Args: cobra.MinimumNArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLineCommentAdd(cmd.OutOrStdout(), *opts, args[0], args[1], args[2], strings.Join(args[3:], " "))
		},
	}
}

func runLineCommentAdd(out io.Writer, opts inspectOptions, project, path, lineArg, note string) error {
	line, err := strconv.Atoi(strings.TrimSpace(lineArg))
	if err != nil || line < 1 {
		return fmt.Errorf("%q is not a line number, give the 1-based line the comment belongs to", lineArg)
	}
	if strings.TrimSpace(note) == "" {
		return errors.New("nothing to add, the comment text is empty")
	}
	client, err := localapi.Dial(opts.stateDir)
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
	if quote := strings.TrimSpace(text(comment["lineText"])); quote != "" {
		fmt.Fprintf(out, "  > %s\n", shorten(quote, 160))
	}
	return nil
}

func newLineCommentRemoveCommand(opts *inspectOptions) *cobra.Command {
	var paths []string
	outdated := false
	cmd := &cobra.Command{
		Use:   "line-comment-remove <project> <id>... | <project> --path <file> | <project> --outdated",
		Short: "Remove line comments",
		Long: "Remove line comments by their ids from `line-comment-list`, with `--path` every " +
			"comment of the matching files, or with `--outdated` exactly the comments whose quote " +
			"no longer matches their file — a quote that can still be rebound is repaired instead " +
			"and stays. `--outdated` combines with `--path` to clear only one file's or folder's " +
			"orphans. `--path` may repeat, the values or together, and each one is an exact file, " +
			"a folder that clears everything under it, or a glob where * stays inside a path " +
			"segment and ** crosses them, like '**/assistant.go'. Every open editor of the " +
			"project follows live. Removing is final, there is no way back.",
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
	client, err := localapi.Dial(opts.stateDir)
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
