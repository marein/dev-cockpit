package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/linecomment"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/plaintext"
	"github.com/spf13/cobra"
)

// maxForgeJobsShown caps the jobs forge-show lists unless --all lifts it.
// The failed and the unfinished ones come first, so the cap drops the ones
// that went through.
const maxForgeJobsShown = 30

// maxForgeThreadsShown caps each list of review threads forge-show prints
// unless --all lifts it. The unresolved ones come first, the resolved last,
// so the cap drops what is settled.
const maxForgeThreadsShown = 30

func newForgeShowCommand(opts *inspectOptions) *cobra.Command {
	branch, job := "", ""
	all, log, threads, fresh := false, false, false, false
	lines := 0
	cmd := &cobra.Command{
		Use:   "forge-show <project>",
		Short: "Show the change request and pipeline of a project's branch",
		Long: "Show the change request and the pipeline of a project's branch on its forge, the branch HEAD is on " +
			"unless `--branch` names another: the open change request from that branch, else one merged or " +
			"closed at the pushed head, and the pipeline of the pushed head with its jobs, on GitHub the check " +
			"runs and commit statuses combined. The pushed head is what origin's tracking branch names as last " +
			"fetched or pushed, never the local HEAD; the output says when the local branch or the change " +
			"request's source stands elsewhere. The jobs list the failed and unfinished first, each with its " +
			"id, capped at the first ones; `--all` lists every one. A job without a log is marked `no log` " +
			"with the reason, one the forge cannot run again `no retry`. `--log` adds the end of the logs of " +
			"the first " + strconv.Itoa(forge.MaxFailedLogs) + " jobs that failed the pipeline, plain text " +
			"with colours and control sequences taken out, `--job` the log of one job instead, the last " +
			strconv.Itoa(forge.DefaultLogLines) + " lines unless `--lines` says otherwise. `--threads` adds " +
			"the review threads, read from the forge on every call: those on the diff with file, line, side, " +
			"whether outdated and the id `forge-thread-reply` and `forge-thread-resolve` take, then the general " +
			"ones, each with its resolved state, link and notes, the forge's own system notes left out. Each " +
			"list shows its first " + strconv.Itoa(maxForgeThreadsShown) + " threads, the unresolved first; " +
			"`--all` lists every one. What the cockpit polled no longer than " +
			strconv.Itoa(int(forge.CacheFresh/time.Second)) + " seconds ago answers, else the forge is asked; " +
			"`--fresh` always asks it. Needs the host's token. Reads only, changes nothing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := url.Values{}
			if branch = strings.TrimSpace(branch); branch != "" {
				query.Set("branch", branch)
			}
			switch {
			case strings.TrimSpace(job) != "":
				query.Set("log", strings.TrimSpace(job))
			case log:
				query.Set("log", "failed")
			}
			if threads {
				query.Set("threads", "1")
			}
			if cmd.Flags().Changed("lines") {
				query.Set("lines", strconv.Itoa(lines))
			}
			if fresh {
				query.Set("fresh", "1")
			}
			return runForgeShow(cmd.OutOrStdout(), *opts, args[0], query, all)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "the branch to read instead of the one HEAD is on")
	cmd.Flags().BoolVar(&all, "all", false, "list every job and review thread instead of the first ones")
	cmd.Flags().BoolVar(&log, "log", false, "add the end of the logs of the jobs that failed the pipeline")
	cmd.Flags().StringVar(&job, "job", "", "add the end of the log of the job with this id instead")
	cmd.Flags().BoolVar(&threads, "threads", false, "add the review threads of the change request")
	cmd.Flags().IntVar(&lines, "lines", forge.DefaultLogLines, fmt.Sprintf("lines of each log to print from its end, 1 to %d", forge.MaxLogLines))
	cmd.Flags().BoolVar(&fresh, "fresh", false, "ask the forge now instead of reading what the cockpit polled")
	return cmd
}

func newForgePipelineRetryCommand(opts *inspectOptions) *cobra.Command {
	branch, job := "", ""
	cmd := &cobra.Command{
		Use:   "forge-pipeline-retry <project>",
		Short: "Run the failed jobs of a branch's pipeline again, once the user approves",
		Long: "Run the jobs that failed the pipeline of a branch's pushed head again, the branch HEAD is on unless " +
			"`--branch` names another; `--job` runs one failed job again, an id `forge-show` lists. Only a " +
			"failed pipeline or job runs again. On GitLab the pipeline's retry runs its failed and canceled " +
			"jobs, each failed trigger job is retried on its own. On GitHub every failed GitHub Actions " +
			"workflow run of the pushed head runs its failed jobs again. A commit status and a check run of " +
			"another app cannot run again, `forge-show` marks them `no retry`. A branch pushed again since, " +
			"another pipeline or a job that ran again is refused. Needs a token that writes: the scope api " +
			"on GitLab, on GitHub the scope repo or, fine grained, Actions read and write. It asks the user first and " +
			"answers `waits for the user's approval`; on approval the forge is read again and the jobs run. " +
			"Either way a note lands in your thread with the outcome; do not run it again while it waits. " +
			"The user can turn the question off with the Pipeline retry approval under Settings › Assistants " +
			"› Approvals; you cannot. Without the question the jobs run at once. A pipeline that then passes " +
			"fires forge-pipeline-succeeded.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			form := url.Values{}
			if branch = strings.TrimSpace(branch); branch != "" {
				form.Set("branch", branch)
			}
			if job = strings.TrimSpace(job); job != "" {
				form.Set("job", job)
			}
			return runForgePipelineRetry(cmd.OutOrStdout(), *opts, args[0], form)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "the branch whose pipeline runs again instead of the one HEAD is on")
	cmd.Flags().StringVar(&job, "job", "", "run only the failed job with this id again, an id forge-show lists")
	return cmd
}

func newForgeChangeNewCommand(opts *inspectOptions) *cobra.Command {
	branch, target, title, body := "", "", "", ""
	ready := false
	cmd := &cobra.Command{
		Use:   "forge-change-new <project>",
		Short: "Open a change request of a pushed branch, once the user approves",
		Long: "Open a change request, a merge request on GitLab and a pull request on GitHub, of a pushed branch, " +
			"the branch HEAD is on unless `--branch` names another. It merges into the default branch unless " +
			"`--target` names another and opens as a draft unless `--ready`. Without `--title` and `--body` " +
			"they are made of the commits the pushed head carries since the target as last fetched: the one " +
			"commit's subject and body, else the oldest one's subject over a list of every subject. A branch " +
			"that carries no commit gets no title, name one. A title has at most " +
			strconv.Itoa(forge.MaxChangeTitle) + " characters, a body at most " + strconv.Itoa(forge.MaxChangeBody) +
			". Only what is pushed is in it, a branch not pushed is refused. A branch with an open change " +
			"request gets no second one, the answer names the open one. Needs a token that writes: the scope " +
			"api on GitLab, on GitHub the scope repo or, fine grained, Pull requests read and write. It asks the user first " +
			"and answers `waits for the user's approval`; on approval the forge is read again and it opens. " +
			"Either way a note lands in your thread with the outcome; do not run it again while it waits. " +
			"The user can turn the question off with the Change request approval under Settings › Assistants " +
			"› Approvals; you cannot. Without the question it opens at once.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			form := url.Values{}
			if branch = strings.TrimSpace(branch); branch != "" {
				form.Set("branch", branch)
			}
			if target = strings.TrimSpace(target); target != "" {
				form.Set("target", target)
			}
			if cmd.Flags().Changed("title") {
				form.Set("title", title)
			}
			if cmd.Flags().Changed("body") {
				form.Set("body", body)
			}
			if !ready {
				form.Set("draft", "1")
			}
			return runForgeChangeNew(cmd.OutOrStdout(), *opts, args[0], form)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "the branch to open it of instead of the one HEAD is on")
	cmd.Flags().StringVar(&target, "target", "", "the branch it merges into instead of the default branch")
	cmd.Flags().StringVar(&title, "title", "", "its title instead of the one made of the commits")
	cmd.Flags().StringVar(&body, "body", "", "its description instead of the one made of the commits")
	cmd.Flags().BoolVar(&ready, "ready", false, "open it ready to merge instead of as a draft")
	return cmd
}

func newForgeCommentPublishCommand(opts *inspectOptions) *cobra.Command {
	all := false
	cmd := &cobra.Command{
		Use:   "forge-comment-publish <project> <id>... | <project> --all",
		Short: "Publish line comments as review comments, once the user approves",
		Long: "Publish line comments as review comments on the open change request of the branch HEAD is on, each " +
			"a review thread of its own: the ids `line-comment-list` prints, or with `--all` every comment the " +
			"user or you wrote. A review thread of the forge, a guest's comment and one with replies do not " +
			"publish. A comment's line has to stand in the pushed head, a line not pushed is refused, push it " +
			"first; an outdated comment no push carries, remove it or write it again on its line. The line " +
			"has to be one the change request's diff shows, a changed line or one around it, else the whole " +
			"call is refused. A published comment becomes that review thread among the line comments. Needs " +
			"a token that writes: the scope api on GitLab, on GitHub the scope repo or, fine grained, Pull " +
			"requests read and write. It asks the user first and answers `waits for the user's approval`; on approval the forge " +
			"is read again and they are published. Either way a note lands in your thread with the outcome " +
			"and each thread's link; do not run it again while it waits. The user can turn the question off " +
			"with the Review comment approval under Settings › Assistants › Approvals; you cannot. Without " +
			"the question they are published at once.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids := trimmedAll(args[1:])
			switch {
			case all && len(ids) > 0:
				return errors.New("give ids, or --all, not both")
			case !all && len(ids) == 0:
				return errors.New("give at least one id from `line-comment-list`, or --all for every comment")
			}
			form := url.Values{}
			if all {
				form.Set("all", "1")
			}
			for _, id := range ids {
				form.Add("id", id)
			}
			return runForgeCommentPublish(cmd.OutOrStdout(), *opts, args[0], form)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "publish every comment the user or you wrote in the project")
	return cmd
}

func newForgeThreadReplyCommand(opts *inspectOptions) *cobra.Command {
	branch := ""
	resolve := false
	cmd := &cobra.Command{
		Use:   "forge-thread-reply <project> <thread> <text>",
		Short: "Answer a review thread, once the user approves",
		Long: "Answer a review thread on the diff of the change request of a branch, the branch HEAD is on unless " +
			"`--branch` names another, by the thread id `forge-show --threads` and `line-comment-list` print " +
			"and a forge-review-comment event names. The text is Markdown, like `fixed in abc123` once a coder " +
			"handled the thread, at most " + strconv.Itoa(linecomment.MaxText>>10) + " KiB. `--resolve` " +
			"resolves the thread after the reply. A general thread of the conversation is refused. GitHub " +
			"files a reply under a review the token's account has pending and shows it once that review is " +
			"submitted, the answer says so. Needs a token that writes: the scope api on GitLab, on GitHub " +
			"the scope repo or, fine grained, Pull requests read and write. It asks the user first and answers `waits for " +
			"the user's approval`; on approval the forge is read again and the reply is posted. Either way a " +
			"note lands in your thread with the outcome; do not run it again while it waits. The user can " +
			"turn the question off with the Review thread approval under Settings › Assistants › Approvals; " +
			"you cannot. Without the question it is posted at once.",
		Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := strings.TrimSpace(strings.Join(args[2:], " "))
			if body == "" {
				return errors.New("nothing to post, the reply's text is empty")
			}
			form := url.Values{"thread": {strings.TrimSpace(args[1])}, "body": {body}}
			if branch = strings.TrimSpace(branch); branch != "" {
				form.Set("branch", branch)
			}
			if resolve {
				form.Set("resolve", "1")
			}
			return runForgeThread(cmd.OutOrStdout(), *opts, args[0], form)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "the branch whose change request holds the thread instead of the one HEAD is on")
	cmd.Flags().BoolVar(&resolve, "resolve", false, "resolve the thread after the reply")
	return cmd
}

func newForgeThreadResolveCommand(opts *inspectOptions) *cobra.Command {
	branch := ""
	cmd := &cobra.Command{
		Use:   "forge-thread-resolve <project> <thread>",
		Short: "Resolve a review thread, once the user approves",
		Long: "Resolve a review thread on the diff of the change request of a branch, the branch HEAD is on unless " +
			"`--branch` names another, by the thread id `forge-show --threads` and `line-comment-list` print " +
			"and a forge-review-comment event names. A thread resolved already, one the forge does not resolve and " +
			"a general one are refused. To say why, use `forge-thread-reply --resolve`, one question for both. " +
			"On GitLab it needs the Developer role or the token's own merge request. Needs a token that " +
			"writes: the scope api on GitLab, on GitHub the scope repo or, fine grained, Pull requests read and write. It " +
			"asks the user first and answers `waits for the user's approval`; on approval the forge is read " +
			"again and the thread is resolved. Either way a note lands in your thread with the outcome; do " +
			"not run it again while it waits. The user can turn the question off with the Review thread " +
			"approval under Settings › Assistants › Approvals; you cannot. Without the question it is " +
			"resolved at once.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			form := url.Values{"thread": {strings.TrimSpace(args[1])}, "resolve": {"1"}}
			if branch = strings.TrimSpace(branch); branch != "" {
				form.Set("branch", branch)
			}
			return runForgeThread(cmd.OutOrStdout(), *opts, args[0], form)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "the branch whose change request holds the thread instead of the one HEAD is on")
	return cmd
}

func runForgeThread(out io.Writer, opts inspectOptions, project string, form url.Values) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.PostForm("/projects/"+url.PathEscape(strings.TrimSpace(project))+"/forge/thread", form, actionTimeout)
	if err != nil {
		return err
	}
	if jsonBool(answer["pending"]) {
		_, err = io.WriteString(out, approvalLine(answer))
		return err
	}
	_, err = fmt.Fprintln(out, text(answer["written"]))
	return err
}

func runForgeCommentPublish(out io.Writer, opts inspectOptions, project string, form url.Values) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.PostForm("/projects/"+url.PathEscape(strings.TrimSpace(project))+"/forge/publish", form, actionTimeout)
	if err != nil {
		return err
	}
	if jsonBool(answer["pending"]) {
		_, err = io.WriteString(out, approvalLine(answer))
		return err
	}
	_, err = fmt.Fprintln(out, text(answer["published"]))
	return err
}

func runForgeChangeNew(out io.Writer, opts inspectOptions, project string, form url.Values) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.PostForm("/projects/"+url.PathEscape(strings.TrimSpace(project))+"/forge/change", form, actionTimeout)
	if err != nil {
		return err
	}
	if jsonBool(answer["pending"]) {
		_, err = io.WriteString(out, approvalLine(answer))
		return err
	}
	said := answer["opened"]
	if existing, ok := answer["existing"]; ok {
		said = existing
	}
	_, err = fmt.Fprintln(out, text(said))
	return err
}

func runForgePipelineRetry(out io.Writer, opts inspectOptions, project string, form url.Values) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.PostForm("/projects/"+url.PathEscape(strings.TrimSpace(project))+"/forge/retry", form, actionTimeout)
	if err != nil {
		return err
	}
	if jsonBool(answer["pending"]) {
		_, err = io.WriteString(out, approvalLine(answer))
		return err
	}
	_, err = fmt.Fprintln(out, text(answer["retried"]))
	return err
}

func runForgeShow(out io.Writer, opts inspectOptions, project string, query url.Values, all bool) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	target := "/projects/" + url.PathEscape(strings.TrimSpace(project)) + "/forge/status"
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	answer, err := client.GetJSON(target, actionTimeout)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, formatForgeShow(answer, all))
	return err
}

// formatForgeShow prints the binding, where the branch stands, the change
// request, the pipeline, the job logs and the review threads asked for.
// Everything the forge wrote is quoted or written with its invisible runes
// escaped, so it cannot pass for a line of its own; a log's lines and a
// note's stand indented below their head.
func formatForgeShow(answer map[string]any, all bool) string {
	var b strings.Builder
	host, _ := answer["forge"].(map[string]any)
	noun, _ := host["changeNoun"].(string)
	branch, _ := answer["branch"].(string)
	remote, _ := answer["remoteBranch"].(string)
	head, _ := answer["head"].(string)
	local, _ := answer["local"].(string)
	fmt.Fprintf(&b, "%s is on %s, %s\n", text(answer["project"]), text(host["label"]), plaintext.Visible(text(host["address"])))
	on := ""
	if remote != branch {
		on = fmt.Sprintf(", %q on origin", remote)
	}
	if head == "" {
		fmt.Fprintf(&b, "Branch %q%s is not pushed, as this repository last fetched or pushed.\n", branch, on)
	} else {
		fmt.Fprintf(&b, "Branch %q%s, pushed at %s", branch, on, shortSHA(head))
		if local != "" && local != head {
			fmt.Fprintf(&b, "; the local branch stands at %s, not at the pushed head", shortSHA(local))
		}
		b.WriteString(".\n")
	}
	if change, ok := answer["change"].(map[string]any); ok {
		formatChange(&b, change, noun, head)
	} else {
		fmt.Fprintf(&b, "No %s for this branch.\n", noun)
	}
	pipeline, ok := answer["pipeline"].(map[string]any)
	switch {
	case head == "":
		b.WriteString("No pipeline without a pushed head.\n")
	case !ok:
		fmt.Fprintf(&b, "No pipeline for %s.\n", shortSHA(head))
	default:
		formatPipeline(&b, pipeline, all)
		if logs, ok := answer["logs"].([]any); ok {
			formatLogs(&b, logs, jsonCount(answer["logsLeft"]))
		}
	}
	if review, asked := answer["review"]; asked {
		formatReview(&b, review, noun, all)
	}
	return b.String()
}

func formatReview(b *strings.Builder, review any, noun string, all bool) {
	r, ok := review.(map[string]any)
	if !ok {
		fmt.Fprintf(b, "No review threads without a %s.\n", noun)
		return
	}
	if problem, _ := r["error"].(string); problem != "" {
		fmt.Fprintf(b, "Review threads: %s\n", plaintext.Visible(problem))
		return
	}
	formatThreads(b, "Threads on the diff", "No threads on the diff.", r["diff"], all)
	formatThreads(b, "General threads", "No general threads.", r["general"], all)
	if partial, _ := r["partial"].(bool); partial {
		b.WriteString("The forge holds more threads or notes than one read takes.\n")
	}
}

// formatThreads prints a list of threads, the unresolved first, each under
// a head saying where it stands with its link below, then its notes, each
// one's text indented below who wrote it when.
func formatThreads(b *strings.Builder, title, none string, list any, all bool) {
	raw, _ := list.([]any)
	threads := make([]map[string]any, 0, len(raw))
	unresolved, resolvable := 0, 0
	for _, item := range raw {
		t, ok := item.(map[string]any)
		if !ok {
			continue
		}
		threads = append(threads, t)
		if r, _ := t["resolvable"].(bool); r {
			resolvable++
			if done, _ := t["resolved"].(bool); !done {
				unresolved++
			}
		}
	}
	if len(threads) == 0 {
		b.WriteString(none + "\n")
		return
	}
	slices.SortStableFunc(threads, func(x, y map[string]any) int { return threadRank(x) - threadRank(y) })
	fmt.Fprintf(b, "%s (%d", title, len(threads))
	if resolvable > 0 {
		fmt.Fprintf(b, ", %d unresolved", unresolved)
	}
	b.WriteString("):\n")
	shown := threads
	if !all && len(shown) > maxForgeThreadsShown {
		shown = shown[:maxForgeThreadsShown]
	}
	for _, t := range shown {
		b.WriteString("  " + threadHead(t) + "\n")
		notes, _ := t["notes"].([]any)
		if len(notes) > 0 {
			first, _ := notes[0].(map[string]any)
			if address, _ := first["url"].(string); address != "" {
				fmt.Fprintf(b, "    %s\n", plaintext.Visible(address))
			}
		}
		for _, item := range notes {
			n, _ := item.(map[string]any)
			b.WriteString("    " + plaintext.Visible(text(n["author"])))
			if when := jsonTime(n["created"]); when != "" {
				b.WriteString(" at " + when)
			}
			b.WriteString(":\n")
			body, _ := n["body"].(string)
			body = strings.TrimRight(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
			if body == "" {
				continue
			}
			for _, line := range strings.Split(body, "\n") {
				fmt.Fprintf(b, "      %s\n", plaintext.Visible(line))
			}
		}
	}
	if rest := len(threads) - len(shown); rest > 0 {
		fmt.Fprintf(b, "  and %d more, --all lists every one\n", rest)
	}
}

// threadHead says whether a thread is resolved, a comment for one the forge
// does not resolve, and on the diff where it stands with the id the writes on
// a thread take.
func threadHead(t map[string]any) string {
	head := "comment"
	if r, _ := t["resolvable"].(bool); r {
		head = "unresolved"
		if done, _ := t["resolved"].(bool); done {
			head = "resolved"
		}
	}
	path, _ := t["path"].(string)
	if path == "" {
		return head
	}
	head += fmt.Sprintf(" %q", path)
	if line := jsonCount(t["line"]); line > 0 {
		head += fmt.Sprintf(" %s line %d", plaintext.Visible(text(t["side"])), line)
	} else {
		head += ", the whole file"
	}
	if outdated, _ := t["outdated"].(bool); outdated {
		head += ", outdated"
	}
	if id, _ := t["id"].(string); id != "" {
		head += ", id " + plaintext.Visible(id)
	}
	return head
}

// threadRank puts an unresolved thread first, then a comment the forge does
// not resolve, then a resolved one.
func threadRank(t map[string]any) int {
	if r, _ := t["resolvable"].(bool); !r {
		return 1
	}
	if done, _ := t["resolved"].(bool); done {
		return 2
	}
	return 0
}

func formatLogs(b *strings.Builder, logs []any, left int) {
	if len(logs) == 0 {
		b.WriteString("No listed job failed the pipeline.\n")
	}
	for _, item := range logs {
		l, _ := item.(map[string]any)
		head := fmt.Sprintf("Log of %q", text(l["name"]))
		if id, _ := l["id"].(string); id != "" {
			head += ", id " + plaintext.Visible(id)
		}
		raw, _ := l["lines"].([]any)
		cut, _ := l["cut"].(bool)
		switch problem, _ := l["error"].(string); {
		case problem != "":
			fmt.Fprintf(b, "%s: %s\n", head, plaintext.Visible(problem))
			continue
		case len(raw) == 0 && cut:
			fmt.Fprintf(b, "%s: its end holds no text.\n", head)
			continue
		case len(raw) == 0:
			fmt.Fprintf(b, "%s: empty.\n", head)
			continue
		case cut:
			fmt.Fprintf(b, "%s, the last %s:\n", head, lineCount(len(raw)))
		default:
			fmt.Fprintf(b, "%s, the whole log of %s:\n", head, lineCount(len(raw)))
		}
		for _, line := range raw {
			s, _ := line.(string)
			fmt.Fprintf(b, "  %s\n", plaintext.Visible(s))
		}
	}
	if left > 0 {
		fmt.Fprintf(b, "%d more jobs failed the pipeline, --job reads the log of one.\n", left)
	}
}

func formatChange(b *strings.Builder, change map[string]any, noun, head string) {
	state := text(change["state"])
	if draft, _ := change["draft"].(bool); draft {
		state += ", draft"
	}
	fmt.Fprintf(b, "%s %s %q: %s, into %q\n", upperFirst(noun), plaintext.Visible(text(change["ref"])), text(change["title"]), state, text(change["target"]))
	fmt.Fprintf(b, "  %s\n", plaintext.Visible(text(change["url"])))
	if source, _ := change["head"].(string); source != "" && head != "" && source != head && change["state"] == "open" {
		fmt.Fprintf(b, "  Its source stands at %s on the forge, not at the pushed head: a fetch catches up.\n", shortSHA(plaintext.Visible(source)))
	}
}

func formatPipeline(b *strings.Builder, pipeline map[string]any, all bool) {
	name := "Pipeline"
	if id, _ := pipeline["id"].(string); id != "" {
		name += " #" + plaintext.Visible(id)
	}
	fmt.Fprintf(b, "%s: %s\n", name, text(pipeline["status"]))
	if address, _ := pipeline["url"].(string); address != "" {
		fmt.Fprintf(b, "  %s\n", plaintext.Visible(address))
	}
	raw, _ := pipeline["jobs"].([]any)
	jobs := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if job, ok := item.(map[string]any); ok {
			jobs = append(jobs, job)
		}
	}
	slices.SortStableFunc(jobs, func(x, y map[string]any) int { return jobRank(x) - jobRank(y) })
	fmt.Fprintf(b, "  Jobs (%d)", len(jobs))
	if partial, _ := pipeline["partial"].(bool); partial {
		b.WriteString(", the first ones of more than one read takes")
	}
	b.WriteString(":\n")
	shown := jobs
	if !all && len(shown) > maxForgeJobsShown {
		shown = shown[:maxForgeJobsShown]
	}
	for _, job := range shown {
		fmt.Fprintf(b, "    %s %q", text(job["status"]), text(job["name"]))
		if stage, _ := job["stage"].(string); stage != "" {
			fmt.Fprintf(b, "  stage %q", stage)
		}
		if allowed, _ := job["allowFailure"].(bool); allowed {
			b.WriteString("  allowed to fail")
		}
		if noLog, _ := job["noLog"].(string); noLog != "" {
			b.WriteString("  no log")
		}
		if noRetry, _ := job["noRetry"].(string); noRetry != "" {
			b.WriteString("  no retry")
		}
		if id, _ := job["id"].(string); id != "" {
			fmt.Fprintf(b, "  id %s", plaintext.Visible(id))
		}
		b.WriteString("\n")
		if address, _ := job["url"].(string); address != "" {
			fmt.Fprintf(b, "      %s\n", plaintext.Visible(address))
		}
	}
	if rest := len(jobs) - len(shown); rest > 0 {
		fmt.Fprintf(b, "    and %d more, --all lists every one\n", rest)
	}
}

// jobRank puts a job that failed the pipeline first, then the unfinished
// ones, then the rest.
func jobRank(job map[string]any) int {
	allowed, _ := job["allowFailure"].(bool)
	switch job["status"] {
	case "failed":
		if !allowed {
			return 0
		}
	case "running", "pending":
		return 1
	}
	return 2
}

func lineCount(n int) string {
	if n == 1 {
		return "1 line"
	}
	return strconv.Itoa(n) + " lines"
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
