package render

import (
	"errors"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

// The badge takes the pipeline's colour and glyph, the change request's
// glyph of where it stands and its number in the forge's words; without a
// change request, a pipeline and a failure there is none, and a failure
// stands beside what the read before it found.
func TestTheForgeBadgeWearsWhatThePollerRead(t *testing.T) {
	onGitLab := forge.Binding{Host: gitlab.Builtin(), Path: "group/app"}
	onGitHub := forge.Binding{Host: github.Builtin(), Path: "octo/app"}
	if NewForgeBadge(onGitLab, forge.Polled{}) != nil {
		t.Fatal("a read of nothing wears a badge")
	}
	for _, c := range []struct {
		status      forge.Status
		tone, icon  string
		id, pipeTip string
	}{
		{forge.Success, "bg-green-lt", "ti-circle-check", "12", "Pipeline #12 success"},
		{forge.Failed, "bg-red-lt", "ti-circle-x", "12", "Pipeline #12 failed"},
		{forge.Running, "bg-blue-lt", "ti-progress dc-icon-spinner", "", "Pipeline running"},
		{forge.Pending, "bg-blue-lt", "ti-clock", "", "Pipeline pending"},
		{forge.Canceled, "bg-secondary-lt", "ti-ban", "", "Pipeline canceled"},
		{forge.Skipped, "bg-secondary-lt", "ti-player-skip-forward", "", "Pipeline skipped"},
		{forge.Manual, "bg-secondary-lt", "ti-hand-stop", "", "Pipeline manual"},
	} {
		b := NewForgeBadge(onGitLab, forge.Polled{Status: forge.BranchStatus{Pipeline: &forge.Pipeline{ID: c.id, Status: c.status}}})
		if b == nil || b.Tone() != c.tone || b.PipelineIcon() != c.icon || b.PipelineTitle() != c.pipeTip || b.Change != nil {
			t.Errorf("%s: %+v", c.status, b)
		}
	}
	for _, c := range []struct {
		binding forge.Binding
		change  forge.Change
		ref     string
		icon    string
		title   string
	}{
		{onGitLab, forge.Change{Number: 4, Title: "Fix", State: forge.Open}, "!4", "ti-git-pull-request", "Merge request !4, open: Fix"},
		{onGitLab, forge.Change{Number: 4, Title: "Fix", State: forge.Open, Draft: true}, "!4", "ti-git-pull-request-draft", "Merge request !4, draft: Fix"},
		{onGitHub, forge.Change{Number: 7, Title: "Fix", State: forge.Merged, Draft: true}, "#7", "ti-git-merge", "Pull request #7, merged: Fix"},
		{onGitHub, forge.Change{Number: 7, Title: "Fix", State: forge.Closed}, "#7", "ti-git-pull-request-closed", "Pull request #7, closed: Fix"},
	} {
		b := NewForgeBadge(c.binding, forge.Polled{Status: forge.BranchStatus{Change: &c.change}})
		if b == nil || b.ChangeRef() != c.ref || b.ChangeIcon() != c.icon || b.ChangeTitle() != c.title || b.Tone() != "bg-secondary-lt" {
			t.Errorf("%+v: %s %s %q", c.change, b.ChangeRef(), b.ChangeIcon(), b.ChangeTitle())
		}
	}
	failed := NewForgeBadge(onGitLab, forge.Polled{Err: errors.New("The server did not answer in time.")})
	if failed == nil || failed.Problem != "The last read of the forge failed: The server did not answer in time." || failed.Change != nil || failed.Pipeline != nil {
		t.Fatalf("a failed read: %+v", failed)
	}
	off := forge.Watch{Branch: "x\u202e", Remote: "x\u202e"}.Refused()
	if b := NewForgeBadge(onGitLab, forge.Polled{Err: off}); b == nil || b.Problem != off.Error() || !strings.HasPrefix(b.Problem, "The forge features are off for this branch, since its name breaks") {
		t.Fatalf("a branch the forge features are off for: %+v", b)
	}
}

// The forge sheet's one line names the change request by its number and
// where it stands, the pipeline and why the last read failed, else the change
// request it offers to open.
func TestTheForgeSheetSumsItselfUpInOneLine(t *testing.T) {
	onGitHub := forge.Binding{Host: github.Builtin(), Path: "octo/app"}
	both := NewForgeBadge(onGitHub, forge.Polled{
		Status: forge.BranchStatus{Change: &forge.Change{Number: 7, State: forge.Open, Draft: true}, Pipeline: &forge.Pipeline{ID: "9", Status: forge.Failed}},
		Err:    errors.New("The server did not answer in time."),
	})
	failed := NewForgeBadge(onGitHub, forge.Polled{Err: errors.New("The server did not answer in time.")})
	offer := &ForgeChangeOffer{Label: "New pull request"}
	for _, c := range []struct {
		data ForgeBranchData
		lead string
	}{
		{ForgeBranchData{Badge: both, Offer: offer}, "#7 draft · Pipeline #9 failed · The last read of the forge failed: The server did not answer in time."},
		{ForgeBranchData{Badge: failed, Offer: offer}, "The last read of the forge failed: The server did not answer in time."},
		{ForgeBranchData{Offer: offer}, "New pull request"},
		{ForgeBranchData{}, ""},
	} {
		if got := c.data.Lead(); got != c.lead {
			t.Errorf("%+v sums up as %q", c.data, got)
		}
	}
}

// The forge sheet counts the pipeline's jobs by status, offers the log of the
// first jobs that failed it and have one, and says under each job where a
// tap leads; the log says how much of it shows.
func TestTheForgeSheetListsThePipelinesJobs(t *testing.T) {
	onGitLab := forge.Binding{Host: gitlab.Builtin(), Path: "group/app"}
	jobs := []forge.Job{
		{ID: "1", Name: "build", Stage: "build", Status: forge.Success},
		{ID: "2", Name: "unit", Stage: "test", Status: forge.Failed},
		{ID: "3", Name: "child", Stage: "deploy", Status: forge.Failed, URL: "https://gitlab.com/group/app/-/jobs/3", NoLog: "A trigger job has no log."},
		{ID: "4", Name: "lint", Stage: "test", Status: forge.Failed, AllowFailure: true},
		{ID: "5", Name: "e2e", Stage: "test", Status: forge.Failed},
		{ID: "6", Name: "ci/ext", Status: forge.Failed, NoLog: "An external commit status has no log."},
		{ID: "7", Name: "docs", Stage: "test", Status: forge.Failed},
		{ID: "8", Name: "race", Stage: "test", Status: forge.Failed},
		{ID: "9", Name: "deploy", Stage: "deploy", Status: forge.Manual},
	}
	b := NewForgeBadge(onGitLab, forge.Polled{Status: forge.BranchStatus{Pipeline: &forge.Pipeline{ID: "12", Status: forge.Failed, Jobs: jobs}}})
	if got, want := b.JobsSummary(), "9 jobs, 7 failed, 1 manual, 1 success"; got != want {
		t.Errorf("the summary reads %q, want %q", got, want)
	}
	b.Pipeline.Partial = true
	if got, want := b.JobsSummary(), "the first 9 jobs, 7 failed, 1 manual, 1 success"; got != want {
		t.Errorf("a partial summary reads %q, want %q", got, want)
	}
	var logs []string
	for _, j := range b.FailedLogs() {
		logs = append(logs, j.Name)
	}
	if got := strings.Join(logs, ","); got != "unit,e2e,docs" {
		t.Errorf("the logs offered are %q", got)
	}
	subs := map[string]string{
		"build":  "build · success",
		"child":  "deploy · failed · no log, opens on GitLab",
		"lint":   "test · failed · allowed to fail",
		"ci/ext": "failed · An external commit status has no log.",
	}
	for _, j := range b.Jobs() {
		if want, ok := subs[j.Name]; ok && j.Sub() != want {
			t.Errorf("%s reads %q, want %q", j.Name, j.Sub(), want)
		}
		if j.Name == "unit" && (j.Icon() != "ti-circle-x" || j.Color() != "red") {
			t.Errorf("a failed job wears %s %s", j.Icon(), j.Color())
		}
	}
	if running := (ForgeJob{Job: forge.Job{Status: forge.Running}}); running.Icon() != "ti-progress dc-icon-spinner" || running.Color() != "blue" {
		t.Errorf("a running job wears %s %s", running.Icon(), running.Color())
	}
	if b := NewForgeBadge(onGitLab, forge.Polled{Status: forge.BranchStatus{Change: &forge.Change{Number: 1, State: forge.Open}}}); b.Jobs() != nil || b.FailedLogs() != nil {
		t.Error("a branch without a pipeline lists jobs")
	}
	for _, c := range []struct {
		log  ForgeLogData
		head string
	}{
		{ForgeLogData{}, "The log is empty."},
		{ForgeLogData{Lines: []string{"ok"}}, "The whole log, 1 line."},
		{ForgeLogData{Lines: []string{"a", "b"}, Cut: true}, "The last 2 lines of the log."},
		{ForgeLogData{Live: forge.LiveLogPace}, "The log is empty. Live while the job runs."},
		{ForgeLogData{Lines: []string{"a", "b"}, Cut: true, Live: forge.LiveLogPace}, "The last 2 lines of the log. Live while the job runs."},
	} {
		if got := c.log.Head(); got != c.head {
			t.Errorf("%+v heads %q, want %q", c.log, got, c.head)
		}
	}
}

// A log names the status of its job and, while the job runs, how soon it is
// read again; one the forge hands out only once the job ended says so in
// place of the log.
func TestALogSaysWhetherItFollowsItsJob(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	running := ForgeJob{Job: forge.Job{ID: "31", Name: "unit", Status: forge.Running}, Forge: "GitLab"}
	for _, c := range []struct {
		log  ForgeLogData
		want []string
		not  []string
	}{
		{ForgeLogData{Job: running, Lines: []string{"ok"}, Live: forge.LiveLogPace},
			[]string{`<div data-forge-log-view data-forge-job="31" data-forge-job-status="running" data-forge-log-live="5000">`, `data-forge-log-head>The whole log, 1 line. Live while the job runs.</div>`},
			[]string{"data-forge-log-later"}},
		{ForgeLogData{Job: ForgeJob{Job: forge.Job{ID: "31", Status: forge.Failed}, Forge: "GitLab"}, Lines: []string{"ok"}},
			[]string{`<div data-forge-log-view data-forge-job="31" data-forge-job-status="failed">`, `data-forge-log-head>The whole log, 1 line.</div>`},
			[]string{"data-forge-log-live", "data-forge-log-later"}},
		{ForgeLogData{Job: ForgeJob{Job: forge.Job{ID: "6", Status: forge.Running}, Forge: "GitHub"}, Later: (&forge.LogLater{Forge: "GitHub"}).Error()},
			[]string{`<div data-forge-log-view data-forge-job="6" data-forge-job-status="running">`, `<div class="small text-secondary px-3 py-2" data-forge-log-later>GitHub hands out a job&#39;s log only once the job ended.</div>`},
			[]string{"data-forge-log-live", "data-forge-log-head", "data-forge-log-text", "data-forge-log-error"}},
	} {
		var out strings.Builder
		if err := tmpl.ExecuteTemplate(&out, "forge_log.gohtml", c.log); err != nil {
			t.Fatal(err)
		}
		html := out.String()
		for _, want := range c.want {
			if !strings.Contains(html, want) {
				t.Errorf("the log lacks %s:\n%s", want, html)
			}
		}
		for _, not := range c.not {
			if strings.Contains(html, not) {
				t.Errorf("the log carries %s:\n%s", not, html)
			}
		}
	}
}

// What a forge hands out, a title, a job's name and its log, comes out of
// the forge sheet's templates escaped; a branch the poller holds nothing of
// renders nothing.
func TestTheForgeSheetEscapesWhatTheForgeSays(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	onGitLab := forge.Binding{Host: gitlab.Builtin(), Path: "group/app"}
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "forge_branch.gohtml", ForgeBranchData{}); err != nil || strings.TrimSpace(out.String()) != "" {
		t.Fatalf("nothing read renders %q %v", out.String(), err)
	}
	job := forge.Job{ID: `7"x`, Name: "<b>unit</b>", Stage: "test", Status: forge.Failed, URL: "https://gitlab.com/group/app/-/jobs/7"}
	b := NewForgeBadge(onGitLab, forge.Polled{Status: forge.BranchStatus{
		Change:   &forge.Change{Number: 4, Title: `<script>alert("x")</script> & co`, State: forge.Open, URL: "https://gitlab.com/group/app/-/merge_requests/4"},
		Pipeline: &forge.Pipeline{ID: "12", Status: forge.Failed, Jobs: []forge.Job{job}},
	}})
	if err := tmpl.ExecuteTemplate(&out, "forge_branch.gohtml", ForgeBranchData{Badge: b}); err != nil {
		t.Fatal(err)
	}
	if err := tmpl.ExecuteTemplate(&out, "forge_log.gohtml", ForgeLogData{Job: ForgeJob{Job: job, Forge: "GitLab"}, Lines: []string{"<img src=x onerror=alert(1)>"}}); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, bad := range []string{"<script>", "<b>unit", "<img", `data-forge-job="7"x"`} {
		if strings.Contains(html, bad) {
			t.Errorf("the sheet carries %s unescaped:\n%s", bad, html)
		}
	}
	for _, want := range []string{
		`data-forge-title>&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt; &amp; co</span>`,
		`data-forge-job="7&#34;x" data-forge-job-name="&lt;b&gt;unit&lt;/b&gt;" data-forge-job-status="failed"`,
		`<span class="text-truncate">Log of &lt;b&gt;unit&lt;/b&gt;</span>`,
		`data-forge-log-text>&lt;img src=x onerror=alert(1)&gt;</pre>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the sheet lacks %s:\n%s", want, html)
		}
	}
}

// A failed pipeline offers its failed jobs to run again in the forge's verb,
// naming the jobs that failed it which cannot run again, and stays off with
// the reason where none of them can; a failed job offers its own run again.
// A pipeline that did not fail offers nothing, and neither does a forge
// that runs nothing again.
func TestAFailedPipelineOffersItsRunAgain(t *testing.T) {
	onGitHub := forge.Binding{Host: github.Builtin(), Path: "octo/app"}
	jobs := []forge.Job{
		{ID: "5", Name: "test", Status: forge.Failed},
		{ID: "7", Name: "scan", Status: forge.Failed, NoRetry: "Only CodeQL runs it again."},
		{ID: "9", Name: "ci/jenkins", Status: forge.Failed, NoRetry: "A commit status cannot run again."},
	}
	polled := func(status forge.Status, jobs ...forge.Job) forge.Polled {
		return forge.Polled{Watch: forge.Watch{Branch: "feature", Head: "6dcb09b5"}, Status: forge.BranchStatus{Pipeline: &forge.Pipeline{Status: status, Jobs: jobs}}}
	}
	b := NewForgeBadge(onGitHub, polled(forge.Failed, jobs...))
	want := ForgeRetry{Label: "Re-run failed jobs", Sub: "Pipeline failed · scan, ci/jenkins cannot run again", Branch: "feature", Head: "6dcb09b5"}
	if r := b.Retry(); r == nil || *r != want {
		t.Fatalf("the pipeline offers %+v", r)
	}
	if r := b.JobRetry(jobs[0]); r == nil || *r != (ForgeRetry{Label: "Re-run job", Sub: "test", Branch: "feature", Head: "6dcb09b5", Job: "5"}) {
		t.Fatalf("the job offers %+v", r)
	}
	if r := b.JobRetry(jobs[1]); r == nil || r.Blocked != "Only CodeQL runs it again." {
		t.Fatalf("a job of another app offers %+v", r)
	}
	blocked := NewForgeBadge(onGitHub, polled(forge.Failed, jobs[1:]...)).Retry()
	if blocked == nil || blocked.Blocked != `No job that failed the pipeline can run again. "scan": Only CodeQL runs it again. "ci/jenkins": A commit status cannot run again.` {
		t.Fatalf("a pipeline nothing of which runs again offers %+v", blocked)
	}
	if r := NewForgeBadge(onGitHub, polled(forge.Running, jobs...)); r.Retry() != nil || r.JobRetry(forge.Job{Status: forge.Success}) != nil {
		t.Fatal("a running pipeline offers a run again")
	}
	plain := forge.Binding{Host: forge.Host{Kind: forge.Kind{ID: "plain", Label: "Plain"}}}
	if r := NewForgeBadge(plain, polled(forge.Failed, jobs...)); r.Retry() != nil || r.JobRetry(jobs[0]) != nil {
		t.Fatal("a forge that runs nothing again offers a run again")
	}
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "forge_retry.gohtml", map[string]any{"Retry": blocked, "Pin": false}); err != nil {
		t.Fatal(err)
	}
	if html := out.String(); !strings.Contains(html, " disabled>") || !strings.Contains(html, `title="No job that failed the pipeline can run again. &#34;scan&#34;`) {
		t.Fatalf("the blocked row renders %s", html)
	}
}

// A pushed branch without an open change request offers one into the
// default branch the forge named, in the forge's words, and the forge sheet
// shows the offer alone where nothing else was read, its one line naming it;
// the default branch itself, a branch not pushed or with an open one offers
// none.
func TestAPushedBranchOffersAChangeRequest(t *testing.T) {
	onGitLab := forge.Binding{Host: gitlab.Builtin(), Path: "group/app"}
	read := func(branch, head string, status forge.BranchStatus) forge.Polled {
		return forge.Polled{Watch: forge.Watch{Branch: branch, Remote: branch, Head: head}, Status: status}
	}
	offer := NewForgeChangeOffer("shop", onGitLab, read("feature/login", "a1", forge.BranchStatus{Default: "main"}))
	if offer == nil || *offer != (ForgeChangeOffer{Label: "New merge request", Sub: "feature/login into main", URL: "/projects/shop/forge/change?branch=feature%2Flogin"}) {
		t.Fatalf("the branch offers %+v", offer)
	}
	for name, p := range map[string]forge.Polled{
		"default":   read("main", "a1", forge.BranchStatus{Default: "main"}),
		"unpushed":  read("feature", "", forge.BranchStatus{Default: "main"}),
		"open":      read("feature", "a1", forge.BranchStatus{Change: &forge.Change{Number: 4, State: forge.Open}}),
		"untold":    read("feature", "a1", forge.BranchStatus{}),
		"merged at": read("feature", "a1", forge.BranchStatus{Change: &forge.Change{Number: 3, State: forge.Merged}}),
	} {
		if got := NewForgeChangeOffer("shop", onGitLab, p); got != nil {
			t.Errorf("%s offers %+v", name, got)
		}
	}
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	var out strings.Builder
	data := ForgeBranchData{Kind: onGitLab.Host.Kind, Offer: offer}
	if err := tmpl.ExecuteTemplate(&out, "forge_branch.gohtml", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, `href="/projects/shop/forge/change?branch=feature%2Flogin" data-forge-change-new>`) || !strings.Contains(html, "<span>New merge request</span>") || strings.Contains(html, "data-forge-head") {
		t.Fatalf("the sheet renders %s", html)
	}
	if !strings.Contains(html, `data-forge-bar-title="GitLab: New merge request"`) || strings.Contains(html, "data-forge-bar-pipeline") {
		t.Fatalf("the button renders %s", html)
	}
}

// An open change request of a forge that publishes review comments is the
// one the line comments publish on, in the forge's words, marked apart from
// the sheet's rows; none without one, one merged, a forge that cannot publish
// or a branch the forge features are off for.
func TestAnOpenChangeRequestIsOfferedToPublishOn(t *testing.T) {
	onGitLab := forge.Binding{Host: gitlab.Builtin(), Path: "group/app"}
	open := forge.Polled{Watch: forge.Watch{Branch: "feature", Remote: "feature", Head: "a1"}, Status: forge.BranchStatus{Change: &forge.Change{Number: 4, State: forge.Open}}}
	offer := NewForgePublishOffer(onGitLab, open, true)
	if offer == nil || *offer != (ForgePublishOffer{Forge: "GitLab", Noun: "merge request", Ref: "!4"}) {
		t.Fatalf("the branch offers %+v", offer)
	}
	failed := open
	failed.Err = errors.New("The server answered 502 Bad Gateway.")
	if NewForgePublishOffer(onGitLab, failed, true) == nil {
		t.Fatal("a read that failed once took the offer away")
	}
	off := open
	off.Err = &forge.BranchOff{What: "its name", Reason: "A branch name holds no spaces."}
	merged := open
	merged.Status = forge.BranchStatus{Change: &forge.Change{Number: 3, State: forge.Merged}}
	for name, got := range map[string]*ForgePublishOffer{
		"no change request": NewForgePublishOffer(onGitLab, forge.Polled{}, true),
		"merged":            NewForgePublishOffer(onGitLab, merged, true),
		"cannot publish":    NewForgePublishOffer(onGitLab, open, false),
		"off":               NewForgePublishOffer(onGitLab, off, true),
	} {
		if got != nil {
			t.Errorf("%s offers %+v", name, got)
		}
	}
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "forge_branch.gohtml", ForgeBranchData{Publish: offer}); err != nil {
		t.Fatal(err)
	}
	if html := out.String(); !strings.Contains(html, `<span hidden data-forge-publish data-forge-publish-forge="GitLab" data-forge-publish-noun="merge request" data-forge-publish-ref="!4"></span>`) || strings.Contains(html, "data-forge-summary") {
		t.Fatalf("the sheet renders %s", html)
	}
}

// The change request whose review threads the line comments show is offered
// to answer and resolve them, merged too, with what the forge does of the
// two, in a hidden mark of the forge fragment; none without one, a forge
// that does neither or a branch the forge features are off for.
func TestAChangeRequestIsOfferedToAnswerItsThreads(t *testing.T) {
	onGitLab := forge.Binding{Host: gitlab.Builtin(), Path: "group/app"}
	merged := forge.Polled{Watch: forge.Watch{Branch: "feature", Remote: "feature", Head: "a1"}, Status: forge.BranchStatus{Change: &forge.Change{Number: 3, State: forge.Merged}}}
	offer := NewForgeThreadsOffer(onGitLab, merged, true, true)
	if offer == nil || *offer != (ForgeThreadsOffer{Forge: "GitLab", Noun: "merge request", Ref: "!3", Reply: true, Resolve: true}) {
		t.Fatalf("the branch offers %+v", offer)
	}
	off := merged
	off.Err = &forge.BranchOff{What: "its name", Reason: "A branch name holds no spaces."}
	for name, got := range map[string]*ForgeThreadsOffer{
		"no change request": NewForgeThreadsOffer(onGitLab, forge.Polled{}, true, true),
		"neither":           NewForgeThreadsOffer(onGitLab, merged, false, false),
		"off":               NewForgeThreadsOffer(onGitLab, off, true, true),
	} {
		if got != nil {
			t.Errorf("%s offers %+v", name, got)
		}
	}
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	for _, c := range []struct {
		offer *ForgeThreadsOffer
		html  string
	}{
		{offer, `<span hidden data-forge-threads data-forge-threads-forge="GitLab" data-forge-threads-noun="merge request" data-forge-threads-ref="!3" data-forge-threads-reply data-forge-threads-resolve></span>`},
		{NewForgeThreadsOffer(onGitLab, merged, true, false), `<span hidden data-forge-threads data-forge-threads-forge="GitLab" data-forge-threads-noun="merge request" data-forge-threads-ref="!3" data-forge-threads-reply></span>`},
	} {
		var out strings.Builder
		if err := tmpl.ExecuteTemplate(&out, "forge_branch.gohtml", ForgeBranchData{Threads: c.offer}); err != nil {
			t.Fatal(err)
		}
		if html := out.String(); !strings.Contains(html, c.html) || strings.Contains(html, "data-forge-summary") {
			t.Fatalf("the sheet renders %s", html)
		}
	}
}

// The form says which commits its title and body were made of.
func TestTheChangeFormSaysWhatItWasMadeOf(t *testing.T) {
	for _, c := range []struct {
		data ForgeChangeData
		says string
	}{
		{ForgeChangeData{Branch: "feature", Target: "main", Carried: 3, Measured: true}, "Made of the 3 commits feature carries since main."},
		{ForgeChangeData{Branch: "feature", Target: "main", Carried: 1, Measured: true}, "Made of the one commit feature carries since main."},
		{ForgeChangeData{Branch: "feature", Target: "main", Measured: true}, "feature carries no commit main lacks, as this repository last fetched it."},
		{ForgeChangeData{Branch: "feature", Target: "main", Carried: 1}, "Made of the pushed head alone: main is not fetched here."},
	} {
		if got := c.data.CarriedLine(); got != c.says {
			t.Errorf("%+v says %q, want %q", c.data, got, c.says)
		}
	}
	if got := (ForgeChangeData{Kind: github.Kind}).Heading(); got != "New pull request" {
		t.Fatalf("the heading reads %q", got)
	}
}

// The form asks for a push only where the local branch is ahead of the
// pushed head alone, and warns where it holds what the change request lacks.
func TestTheChangeFormSaysHowTheLocalBranchStands(t *testing.T) {
	for _, c := range []struct {
		ahead, behind int
		level, says   string
	}{
		{1, 0, "warning", "1 commit ahead of the pushed head: push first to carry it."},
		{3, 0, "warning", "3 commits ahead of the pushed head: push first to carry them."},
		{0, 1, "info", "1 commit behind the pushed head, which this pull request carries."},
		{2, 1, "warning", "2 commits ahead of the pushed head and 1 behind it: this pull request lacks them."},
		{0, 0, "warning", "not at the pushed head."},
	} {
		data := ForgeChangeData{Kind: github.Kind, Ahead: c.ahead, Behind: c.behind}
		if level, says := data.LocalLevel(), data.LocalLine(); level != c.level || says != c.says {
			t.Errorf("%d ahead, %d behind: %s %q, want %s %q", c.ahead, c.behind, level, says, c.level, c.says)
		}
	}
}
