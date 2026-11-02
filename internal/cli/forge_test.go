package cli

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func forgeAnswer(change, pipeline any) map[string]any {
	return map[string]any{
		"project":      "app",
		"forge":        map[string]any{"label": "GitLab", "address": "gitlab.com/group/sub/app", "url": "https://gitlab.com/group/sub/app", "changeNoun": "merge request"},
		"branch":       "feature/login",
		"remoteBranch": "feature/login",
		"head":         "f471a571e52a7d8211d5cd279aa17f890d2336d9",
		"local":        "f471a571e52a7d8211d5cd279aa17f890d2336d9",
		"change":       change,
		"pipeline":     pipeline,
	}
}

func forgeJob(id, name, status string, allowFailure bool) map[string]any {
	return map[string]any{"id": id, "name": name, "stage": "test", "status": status, "url": "https://gitlab.com/group/sub/app/-/jobs/" + id, "allowFailure": allowFailure}
}

// The binding, the pushed head, the merge request in GitLab's words and the
// pipeline with its jobs, the one that failed it first and the one allowed
// to fail among the rest.
func TestFormatForgeShowNamesTheChangeAndThePipeline(t *testing.T) {
	out := formatForgeShow(forgeAnswer(
		map[string]any{"ref": "!4", "title": "Fix the login", "draft": true, "state": "open", "url": "https://gitlab.com/group/sub/app/-/merge_requests/4", "target": "main", "head": "f471a571e52a7d8211d5cd279aa17f890d2336d9"},
		map[string]any{"id": "12", "status": "failed", "url": "https://gitlab.com/group/sub/app/-/pipelines/12", "partial": false, "jobs": []any{
			forgeJob("31", "build", "success", false), forgeJob("32", "lint", "failed", true), forgeJob("33", "unit", "failed", false),
		}},
	), false)
	want := "app is on GitLab, gitlab.com/group/sub/app\n" +
		"Branch \"feature/login\", pushed at f471a571e52a.\n" +
		"Merge request !4 \"Fix the login\": open, draft, into \"main\"\n" +
		"  https://gitlab.com/group/sub/app/-/merge_requests/4\n" +
		"Pipeline #12: failed\n" +
		"  https://gitlab.com/group/sub/app/-/pipelines/12\n" +
		"  Jobs (3):\n" +
		"    failed \"unit\"  stage \"test\"  id 33\n      https://gitlab.com/group/sub/app/-/jobs/33\n" +
		"    success \"build\"  stage \"test\"  id 31\n      https://gitlab.com/group/sub/app/-/jobs/31\n" +
		"    failed \"lint\"  stage \"test\"  allowed to fail  id 32\n      https://gitlab.com/group/sub/app/-/jobs/32\n"
	if out != want {
		t.Fatalf("the output reads\n%s\nwant\n%s", out, want)
	}
}

// A branch the forge knows under another name, a local branch standing
// elsewhere than what was pushed and a change request whose source moved on
// are all said, and
// GitHub's pipeline has no number.
func TestFormatForgeShowSaysWhereTheBranchStands(t *testing.T) {
	answer := forgeAnswer(
		map[string]any{"ref": "#7", "title": "Fix", "draft": false, "state": "open", "url": "https://github.com/octo/app/pull/7", "target": "main", "head": "553c2077f0edc3d5dc5d17262f6aa498e69d6f8e"},
		map[string]any{"id": "", "status": "running", "url": "https://github.com/octo/app/commit/f471a571", "partial": true, "jobs": []any{forgeJob("", "ci/jenkins", "running", false)}},
	)
	answer["forge"] = map[string]any{"label": "GitHub", "address": "github.com/octo/app", "changeNoun": "pull request"}
	answer["remoteBranch"] = "login"
	answer["local"] = "0208075b065b959528974b98fc3ecdf15fa44f3d"
	out := formatForgeShow(answer, false)
	for _, want := range []string{
		"Branch \"feature/login\", \"login\" on origin, pushed at f471a571e52a; the local branch stands at 0208075b065b, not at the pushed head.\n",
		"Pull request #7 \"Fix\": open, into \"main\"\n",
		"  Its source stands at 553c2077f0ed on the forge, not at the pushed head: a fetch catches up.\n",
		"Pipeline: running\n",
		"  Jobs (1), the first ones of more than one read takes:\n    running \"ci/jenkins\"  stage \"test\"\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output misses %q:\n%s", want, out)
		}
	}
}

// A branch never pushed, one without a change request and a head without a
// pipeline are answers in plain words.
func TestFormatForgeShowSaysWhatIsMissing(t *testing.T) {
	answer := forgeAnswer(nil, nil)
	if out := formatForgeShow(answer, false); !strings.Contains(out, "No merge request for this branch.\nNo pipeline for f471a571e52a.\n") {
		t.Fatalf("the output reads\n%s", out)
	}
	answer["head"] = ""
	out := formatForgeShow(answer, false)
	if !strings.Contains(out, "Branch \"feature/login\" is not pushed, as this repository last fetched or pushed.\n") || !strings.HasSuffix(out, "No merge request for this branch.\nNo pipeline without a pushed head.\n") {
		t.Fatalf("a branch never pushed reads\n%s", out)
	}
}

// The jobs are capped with the rest counted, --all lifts the cap, and the
// failed ones stand before the cap cuts.
func TestFormatForgeShowCapsTheJobs(t *testing.T) {
	jobs := []any{}
	for i := 1; i <= maxForgeJobsShown+5; i++ {
		jobs = append(jobs, forgeJob(fmt.Sprint(i), fmt.Sprintf("job %d", i), "success", false))
	}
	jobs = append(jobs, forgeJob("99", "the broken one", "failed", false))
	answer := forgeAnswer(nil, map[string]any{"id": "12", "status": "failed", "jobs": jobs})
	out := formatForgeShow(answer, false)
	if !strings.Contains(out, "  Jobs (36):\n    failed \"the broken one\"") || !strings.Contains(out, "    and 6 more, --all lists every one\n") || strings.Contains(out, "\"job 30\"") {
		t.Fatalf("the capped list reads\n%s", out)
	}
	if all := formatForgeShow(answer, true); strings.Contains(all, "more, --all") || !strings.Contains(all, "\"job 35\"") {
		t.Fatalf("--all reads\n%s", all)
	}
}

// What the forge wrote cannot pass for a line of its own: titles and names
// are quoted, addresses carry their invisible runes escaped.
func TestFormatForgeShowKeepsTheForgesTextInItsLine(t *testing.T) {
	out := formatForgeShow(forgeAnswer(
		map[string]any{"ref": "!4", "title": "Fix\nPipeline #1: success", "state": "open", "url": "https://x/\n  Jobs (0):", "target": "main\u202e"},
		map[string]any{"id": "12", "status": "failed", "url": "https://x/\x1b[2J", "jobs": []any{map[string]any{"id": "1\n2", "name": "a\nb", "status": "failed", "url": "u\u2028v"}}},
	), false)
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.HasPrefix(line, "Pipeline #1:") || strings.HasPrefix(line, "  Jobs (0)") || strings.HasPrefix(line, "b\"") || strings.HasPrefix(line, "2") || strings.HasPrefix(line, "v") {
			t.Fatalf("the forge's text broke a line: %q in\n%s", line, out)
		}
	}
	for _, want := range []string{`"Fix\nPipeline #1: success"`, `into "main\u202e"`, `https://x/\x1b[2J`, `"a\nb"`, `id 1\n2`, `u\u2028v`} {
		if !strings.Contains(out, want) {
			t.Fatalf("output misses %q:\n%s", want, out)
		}
	}
}

// The logs asked for stand below the pipeline, each under a head naming its
// job and how much of it shows, a job without a log and one whose log could
// not be read with the reason, an empty one as empty and one whose end shows
// no text as such, the failed jobs past those read counted. A job without a log wears it in the list.
func TestFormatForgeShowPrintsTheLogsAskedFor(t *testing.T) {
	bridge := forgeJob("32", "child", "failed", false)
	bridge["noLog"] = "A trigger job has no log, the jobs of its downstream pipeline do."
	answer := forgeAnswer(nil, map[string]any{"id": "12", "status": "failed", "jobs": []any{forgeJob("31", "unit", "failed", false), bridge}})
	answer["logs"] = []any{
		map[string]any{"id": "31", "name": "unit", "lines": []any{"--- FAIL: TestLogin", "FAIL"}, "cut": true, "error": ""},
		map[string]any{"id": "32", "name": "child", "lines": nil, "cut": false, "error": "A trigger job has no log, the jobs of its downstream pipeline do."},
		map[string]any{"id": "33", "name": "lint", "lines": []any{"ok"}, "cut": false, "error": ""},
		map[string]any{"id": "34", "name": "e2e", "lines": nil, "cut": false, "error": ""},
		map[string]any{"id": "35", "name": "deploy", "lines": nil, "cut": false, "error": "GitLab has no log of \"deploy\" in gitlab.com/group/sub/app, or the token may not read it."},
		map[string]any{"id": "36", "name": "noise", "lines": nil, "cut": true, "error": ""},
	}
	answer["logsLeft"] = float64(2)
	out := formatForgeShow(answer, false)
	for _, want := range []string{
		"    failed \"child\"  stage \"test\"  no log  id 32\n",
		"Log of \"unit\", id 31, the last 2 lines:\n  --- FAIL: TestLogin\n  FAIL\n",
		"Log of \"child\", id 32: A trigger job has no log, the jobs of its downstream pipeline do.\n",
		"Log of \"lint\", id 33, the whole log of 1 line:\n  ok\n",
		"Log of \"e2e\", id 34: empty.\n",
		"Log of \"deploy\", id 35: GitLab has no log of \"deploy\" in gitlab.com/group/sub/app, or the token may not read it.\n",
		"Log of \"noise\", id 36: its end holds no text.\n",
		"2 more jobs failed the pipeline, --job reads the log of one.\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(formatForgeShow(forgeAnswer(nil, map[string]any{"id": "12", "status": "failed", "jobs": []any{}}), false), "Log of") {
		t.Fatal("logs nobody asked for")
	}
	answer["logs"], answer["logsLeft"] = []any{}, float64(0)
	if out := formatForgeShow(answer, false); !strings.HasSuffix(out, "No listed job failed the pipeline.\n") {
		t.Fatalf("no failed job reads\n%s", out)
	}
	answer["pipeline"] = nil
	if out := formatForgeShow(answer, false); strings.Contains(out, "No listed job") || !strings.HasSuffix(out, "No pipeline for f471a571e52a.\n") {
		t.Fatalf("no pipeline reads\n%s", out)
	}
}

// A log's lines stand indented under their head with what shows nothing
// escaped, so a line of the log cannot pass for a head or a job.
func TestFormatForgeShowKeepsALogsLinesInTheirPlace(t *testing.T) {
	answer := forgeAnswer(nil, map[string]any{"id": "12", "status": "failed", "jobs": []any{}})
	answer["logs"] = []any{map[string]any{"id": "31", "name": "unit\nLog of \"x\"", "lines": []any{"Log of \"y\": empty.", "a\u2028b\u202ec"}, "cut": false, "error": ""}}
	out := formatForgeShow(answer, false)
	if !strings.Contains(out, `Log of "unit\nLog of \"x\"", id 31, the whole log of 2 lines:`) || !strings.Contains(out, "\n  Log of \"y\": empty.\n") || !strings.Contains(out, `  a\u2028b\u202ec`) {
		t.Fatalf("the log reads\n%s", out)
	}
}

func forgeThread(id, path string, line int, side string, resolvable, resolved, outdated bool, notes ...any) map[string]any {
	return map[string]any{"id": id, "path": path, "line": float64(line), "side": side, "outdated": outdated, "reply": true, "resolvable": resolvable, "resolved": resolved, "notes": notes}
}

func forgeNote(author, body, url string) map[string]any {
	return map[string]any{"id": "1", "author": author, "body": body, "created": "2026-10-28T23:03:31.551Z", "url": url}
}

// The review threads stand after the pipeline, the ones on the diff apart
// from the general ones, the unresolved first: each head says where it
// stands with the thread's id, none for a note the forge keeps alone, the
// first note's link below it, then each note's author, time and text, its
// lines indented.
func TestFormatForgeShowListsTheReviewThreads(t *testing.T) {
	answer := forgeAnswer(map[string]any{"ref": "!4", "title": "Fix", "state": "open", "url": "https://gitlab.com/group/sub/app/-/merge_requests/4", "target": "main"}, nil)
	page := "https://gitlab.com/group/sub/app/-/merge_requests/4#note_"
	answer["review"] = map[string]any{"error": "", "partial": false, "diff": []any{
		forgeThread("d3", "src/login.go", 5, "new", true, true, false, forgeNote("root", "Context c", page+"31")),
		forgeThread("d1", "src/login.go", 4, "new", true, false, true, forgeNote("root", "New name bb?", page+"29"), forgeNote("alice", "Reply:\r\nyes, bb.\n", page+"33")),
		forgeThread("d2", "src/old.go", 4, "old", true, false, false, forgeNote("root", "Why drop b?", page+"30")),
		forgeThread("d4", "README.md", 0, "", true, false, false, forgeNote("", "", page+"37")),
	}, "general": []any{
		forgeThread("d6", "", 0, "", false, false, false, forgeNote("root", "Individual note", page+"36")),
		forgeThread("d5", "", 0, "", true, false, false, forgeNote("root", "General thread", page+"34")),
	}}
	alone := forgeThread("IC_1", "", 0, "", false, false, false, forgeNote("hubot", "Looks good", page+"38"))
	alone["reply"] = false
	answer["review"].(map[string]any)["general"] = append(answer["review"].(map[string]any)["general"].([]any), alone)
	out := formatForgeShow(answer, false)
	at := jsonTime("2026-10-28T23:03:31.551Z")
	want := "No pipeline for f471a571e52a.\n" +
		"Threads on the diff (4, 3 unresolved):\n" +
		"  unresolved \"src/login.go\" new line 4, outdated, id d1\n" +
		"    " + page + "29\n" +
		"    root at " + at + ":\n      New name bb?\n" +
		"    alice at " + at + ":\n      Reply:\n      yes, bb.\n" +
		"  unresolved \"src/old.go\" old line 4, id d2\n" +
		"    " + page + "30\n" +
		"    root at " + at + ":\n      Why drop b?\n" +
		"  unresolved \"README.md\", the whole file, id d4\n" +
		"    " + page + "37\n" +
		"    ? at " + at + ":\n" +
		"  resolved \"src/login.go\" new line 5, id d3\n" +
		"    " + page + "31\n" +
		"    root at " + at + ":\n      Context c\n" +
		"General threads (3, 1 unresolved):\n" +
		"  unresolved, id d5\n" +
		"    " + page + "34\n" +
		"    root at " + at + ":\n      General thread\n" +
		"  comment, id d6\n" +
		"    " + page + "36\n" +
		"    root at " + at + ":\n      Individual note\n" +
		"  comment\n" +
		"    " + page + "38\n" +
		"    hubot at " + at + ":\n      Looks good\n"
	if !strings.HasSuffix(out, want) {
		t.Fatalf("the output reads\n%s\nwant it to end in\n%s", out, want)
	}
}

// Threads not asked for print nothing; without a change request, without
// threads, past a read's bound and where the forge could not be read the
// output says so; each list is capped with the resolved ones dropped first
// and the rest counted, --all lifts the cap.
func TestFormatForgeShowSaysWhatTheThreadsMiss(t *testing.T) {
	answer := forgeAnswer(nil, nil)
	if out := formatForgeShow(answer, false); strings.Contains(out, "thread") {
		t.Fatalf("threads nobody asked for:\n%s", out)
	}
	answer["review"] = nil
	if out := formatForgeShow(answer, false); !strings.HasSuffix(out, "No review threads without a merge request.\n") {
		t.Fatalf("without a change request:\n%s", out)
	}
	answer["review"] = map[string]any{"error": "", "partial": true, "diff": []any{}, "general": []any{}}
	if out := formatForgeShow(answer, false); !strings.HasSuffix(out, "No threads on the diff.\nNo general threads.\nThe forge holds more threads or notes than one read takes.\n") {
		t.Fatalf("no threads:\n%s", out)
	}
	answer["review"] = map[string]any{"error": "The token may not read the review threads of github.com/octo/app."}
	if out := formatForgeShow(answer, false); !strings.HasSuffix(out, "Review threads: The token may not read the review threads of github.com/octo/app.\n") {
		t.Fatalf("a read that failed:\n%s", out)
	}
	threads := []any{}
	for i := 1; i <= maxForgeThreadsShown+3; i++ {
		threads = append(threads, forgeThread(fmt.Sprint(i), "a.go", i, "new", true, true, false, forgeNote("root", fmt.Sprintf("settled %d", i), "u")))
	}
	threads = append(threads, forgeThread("99", "a.go", 99, "new", true, false, false, forgeNote("root", "the open one", "u")))
	answer["review"] = map[string]any{"error": "", "partial": false, "diff": threads, "general": []any{}}
	out := formatForgeShow(answer, false)
	if !strings.Contains(out, "Threads on the diff (34, 1 unresolved):\n  unresolved \"a.go\" new line 99, id 99\n") || !strings.Contains(out, "  and 4 more, --all lists every one\n") || strings.Contains(out, "settled 30\n") {
		t.Fatalf("the capped list reads\n%s", out)
	}
	if all := formatForgeShow(answer, true); strings.Contains(all, "more, --all") || !strings.Contains(all, "settled 33\n") {
		t.Fatalf("--all reads\n%s", all)
	}
}

// What a note says cannot pass for a line of its own: every line of it
// stands indented with what shows nothing escaped, and a path is quoted.
func TestFormatForgeShowKeepsTheNotesInTheirPlace(t *testing.T) {
	answer := forgeAnswer(nil, nil)
	answer["review"] = map[string]any{"error": "", "diff": []any{
		forgeThread("1", "a.go\nresolved \"b.go\"", 3, "new\n", true, false, false, forgeNote("root\nGeneral threads (1):", "General threads (9):\nx\u202ey\u2028z", "https://x/\x1b[2J")),
	}, "general": []any{}}
	out := formatForgeShow(answer, false)
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.HasPrefix(line, "General threads (1)") || strings.HasPrefix(line, "General threads (9)") || strings.HasPrefix(line, "resolved") || strings.HasPrefix(line, "x") {
			t.Fatalf("a note broke a line: %q in\n%s", line, out)
		}
	}
	for _, want := range []string{`  unresolved "a.go\nresolved \"b.go\"" new\n line 3`, `    https://x/\x1b[2J`, `    root\nGeneral threads (1): at `, "      General threads (9):\n", `      x\u202ey\u2028z`} {
		if !strings.Contains(out, want) {
			t.Fatalf("output misses %q:\n%s", want, out)
		}
	}
}

// A job the forge cannot run again is marked beside its other marks.
func TestFormatForgeShowMarksAJobThatCannotRunAgain(t *testing.T) {
	external := forgeJob("36", "ci/external", "failed", false)
	external["noLog"] = "An external commit status has no log on GitLab."
	external["noRetry"] = "GitLab cannot run an external commit status again."
	out := formatForgeShow(forgeAnswer(nil, map[string]any{"id": "12", "status": "failed", "jobs": []any{external, forgeJob("33", "unit", "failed", false)}}), false)
	if !strings.Contains(out, "    failed \"ci/external\"  stage \"test\"  no log  no retry  id 36\n") || !strings.Contains(out, "    failed \"unit\"  stage \"test\"  id 33\n") {
		t.Fatalf("the output reads\n%s", out)
	}
}

// The retry posts the branch and the job it names, nothing it does not, and
// says when it waits for the user, or what runs again when nobody is asked.
func TestARetryWaitsOrSaysWhatRunsAgain(t *testing.T) {
	for _, tc := range []struct {
		form          url.Values
		answer, wants string
	}{
		{url.Values{}, `{"pending":true,"what":"Retry failed jobs of pipeline #12 on feature in app"}`, "Retry failed jobs of pipeline #12 on feature in app waits for the user's approval. A note lands in your thread once they decide; do not run it again.\n"},
		{url.Values{"branch": {"feature"}, "job": {"31"}}, `{"ok":true,"retried":"On GitLab gitlab.com/group/app the job \"unit\" of pipeline #12 of feature in app runs again."}`, "On GitLab gitlab.com/group/app the job \"unit\" of pipeline #12 of feature in app runs again.\n"},
	} {
		var path string
		var posted url.Values
		dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.EscapedPath()
			_ = r.ParseForm()
			posted = r.PostForm
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(tc.answer))
		})
		var out strings.Builder
		if err := runForgePipelineRetry(&out, inspectOptions{stateDir: dir}, " my app ", tc.form); err != nil {
			t.Fatal(err)
		}
		if path != "/projects/my%20app/forge/retry" || posted.Encode() != tc.form.Encode() || out.String() != tc.wants {
			t.Fatalf("posted %s %v, printed %q", path, posted, out.String())
		}
	}
}

// The open posts what its flags name, a draft unless --ready, a title or a
// body only where given, empty ones too, and says when it waits for the
// user, what opened, or the open one the branch has already.
func TestAnOpenPostsItsFlagsAndSaysWhatOpened(t *testing.T) {
	for _, tc := range []struct {
		args          []string
		form          url.Values
		answer, wants string
	}{
		{nil, url.Values{"draft": {"1"}}, `{"pending":true,"what":"Open draft merge request of feature into main in app"}`, "Open draft merge request of feature into main in app waits for the user's approval. A note lands in your thread once they decide; do not run it again.\n"},
		{[]string{"--branch", " feature ", "--target", "release", "--title", "Ship it", "--body", "", "--ready"}, url.Values{"branch": {"feature"}, "target": {"release"}, "title": {"Ship it"}, "body": {""}}, `{"ok":true,"opened":"On GitLab gitlab.com/group/app the merge request !5 \"Ship it\" of feature into release in app is open: https://gitlab.com/group/app/-/merge_requests/5."}`, "On GitLab gitlab.com/group/app the merge request !5 \"Ship it\" of feature into release in app is open: https://gitlab.com/group/app/-/merge_requests/5.\n"},
		{nil, url.Values{"draft": {"1"}}, `{"ok":true,"existing":"On GitLab gitlab.com/group/app feature of app has the open merge request !4 \"Fix\" into main already: https://gitlab.com/group/app/-/merge_requests/4."}`, "On GitLab gitlab.com/group/app feature of app has the open merge request !4 \"Fix\" into main already: https://gitlab.com/group/app/-/merge_requests/4.\n"},
	} {
		var path string
		var posted url.Values
		dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.EscapedPath()
			_ = r.ParseForm()
			posted = r.PostForm
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(tc.answer))
		})
		cmd := newForgeChangeNewCommand(&inspectOptions{stateDir: dir})
		var out strings.Builder
		cmd.SetOut(&out)
		cmd.SetArgs(append([]string{" my app "}, tc.args...))
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if path != "/projects/my%20app/forge/change" || posted.Encode() != tc.form.Encode() || out.String() != tc.wants {
			t.Fatalf("posted %s %v, printed %q", path, posted, out.String())
		}
	}
}

// The publish posts the ids it names, or all, refuses neither or both before
// it asks anything, and says when it waits for the user or what it published.
func TestAPublishPostsItsIdsAndSaysWhatWasPublished(t *testing.T) {
	for _, tc := range []struct {
		args          []string
		form          url.Values
		answer, wants string
	}{
		{[]string{" a1 ", "b2"}, url.Values{"id": {"a1", "b2"}}, `{"pending":true,"what":"Publish 2 line comments on merge request !4 of feature in app"}`, "Publish 2 line comments on merge request !4 of feature in app waits for the user's approval. A note lands in your thread once they decide; do not run it again.\n"},
		{[]string{"--all"}, url.Values{"all": {"1"}}, `{"ok":true,"published":"On GitLab gitlab.com/group/app the line comment of feature in app is published on the merge request !4 \"Fix\":\n- a.go:3 https://gitlab.com/group/app/-/merge_requests/4#note_9"}`, "On GitLab gitlab.com/group/app the line comment of feature in app is published on the merge request !4 \"Fix\":\n- a.go:3 https://gitlab.com/group/app/-/merge_requests/4#note_9\n"},
	} {
		var path string
		var posted url.Values
		dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.EscapedPath()
			_ = r.ParseForm()
			posted = r.PostForm
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(tc.answer))
		})
		cmd := newForgeCommentPublishCommand(&inspectOptions{stateDir: dir})
		var out strings.Builder
		cmd.SetOut(&out)
		cmd.SetArgs(append([]string{" my app "}, tc.args...))
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if path != "/projects/my%20app/forge/publish" || posted.Encode() != tc.form.Encode() || out.String() != tc.wants {
			t.Fatalf("posted %s %v, printed %q", path, posted, out.String())
		}
	}
	for _, args := range [][]string{{"app"}, {"app", "a1", "--all"}} {
		cmd := newForgeCommentPublishCommand(&inspectOptions{stateDir: t.TempDir()})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("%v went through", args)
		}
	}
}

// A reply posts the thread, its text and --resolve, a resolve the thread
// alone, each with --branch; an empty reply is refused before it asks
// anything; and each says when it waits for the user or what it wrote.
func TestTheWritesOnAThreadPostTheirFormAndSayWhatWasWritten(t *testing.T) {
	written := "On GitLab gitlab.com/group/app the review thread on a.go:3 of the merge request !4 \"Fix\" of feature in app holds your reply and is resolved: https://gitlab.com/group/app/-/merge_requests/4#note_9."
	for _, tc := range []struct {
		command       func(*inspectOptions) *cobra.Command
		args          []string
		form          url.Values
		answer, wants string
	}{
		{newForgeThreadReplyCommand, []string{" d1 ", "fixed", "in", "abc123", "--resolve"}, url.Values{"thread": {"d1"}, "body": {"fixed in abc123"}, "resolve": {"1"}}, `{"ok":true,"written":` + strconv.Quote(written) + `}`, written + "\n"},
		{newForgeThreadReplyCommand, []string{"d1", "fixed", "--branch", " feature "}, url.Values{"thread": {"d1"}, "body": {"fixed"}, "branch": {"feature"}}, `{"pending":true,"what":"Reply at a.go:3 on merge request !4 of feature in app"}`, "Reply at a.go:3 on merge request !4 of feature in app waits for the user's approval. A note lands in your thread once they decide; do not run it again.\n"},
		{newForgeThreadResolveCommand, []string{"d1", "--branch", "feature"}, url.Values{"thread": {"d1"}, "resolve": {"1"}, "branch": {"feature"}}, `{"ok":true,"written":"done"}`, "done\n"},
	} {
		var path string
		var posted url.Values
		dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.EscapedPath()
			_ = r.ParseForm()
			posted = r.PostForm
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(tc.answer))
		})
		cmd := tc.command(&inspectOptions{stateDir: dir})
		var out strings.Builder
		cmd.SetOut(&out)
		cmd.SetArgs(append([]string{" my app "}, tc.args...))
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if path != "/projects/my%20app/forge/thread" || posted.Encode() != tc.form.Encode() || out.String() != tc.wants {
			t.Fatalf("posted %s %v, printed %q", path, posted, out.String())
		}
	}
	cmd := newForgeThreadReplyCommand(&inspectOptions{stateDir: t.TempDir()})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"app", "d1", " "})
	if err := cmd.Execute(); err == nil {
		t.Error("an empty reply went through")
	}
}
