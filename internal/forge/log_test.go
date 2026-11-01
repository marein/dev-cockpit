package forge_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

// logStub answers the raw end of a log by the job's id, and keeps whom it
// was asked for.
type logStub struct {
	gitlab.Provider
	logs map[string]string
	cut  map[string]bool

	mu    sync.Mutex
	asked []string
	tok   string
	keep  int
}

func (p *logStub) JobLog(_ context.Context, _ *forge.Client, _ forge.Binding, token string, job forge.Job, keep int) ([]byte, bool, error) {
	p.mu.Lock()
	p.asked, p.tok, p.keep = append(p.asked, job.ID), token, keep
	p.mu.Unlock()
	raw, ok := p.logs[job.ID]
	if !ok {
		return nil, false, errors.New("GitLab has no log of it.")
	}
	return []byte(raw), p.cut[job.ID], nil
}

func logService(t *testing.T, providers ...forge.Provider) (*forge.Service, forge.Binding) {
	t.Helper()
	s := forge.New(t.TempDir(), append([]forge.Provider{github.Provider{}}, providers...), github.Builtin(), gitlab.Builtin())
	return s, forge.Binding{Host: gitlab.Builtin(), Path: "group/app"}
}

// Each log reaches its job in the order asked, as the plain text lines a
// terminal shows: a GitLab trace's section marks and colours go, a log kept
// from its middle loses the rest of its first line, a byte order mark at the
// start shows nothing, the empty lines at the end go, and more lines than
// asked for keep the last ones and count as cut. A job without a log is not
// asked for and answers why, a failure answers its sentence.
func TestJobLogsReadTheEndOfEachLogAsPlainText(t *testing.T) {
	stub := &logStub{
		logs: map[string]string{
			"1": "section_start:1730000002:step_script\r\x1b[0K\x1b[0K\x1b[36;1mExecuting \"step_script\"\x1b[0;m\n\x1b[32;1m$ go test\x1b[0;m\n10%\r100%\nsection_end:1730000003:step_script\r\x1b[0K\x1b[31;1mERROR: Job failed: exit code 1\n\x1b[0;m\n",
			"2": "of a line cut short\nsecond\nthird\n",
			"3": "\xef\xbb\xbf2026-10-28T09:00:00.0000000Z ##[group]Run go test\n",
			"4": "one\ntwo\nthree\nfour\n",
			"5": "\x1b[0;m\n\n",
		},
		cut: map[string]bool{"2": true},
	}
	s, b := logService(t, stub)
	if err := s.ChangeToken("gitlab.com", token, false); err != nil {
		t.Fatal(err)
	}
	jobs := []forge.Job{{ID: "1"}, {ID: "2"}, {ID: "3"}, {ID: "4"}, {ID: "5"}, {ID: "6", NoLog: "A trigger job has no log."}, {ID: "7"}}
	logs := s.JobLogs(context.Background(), b, jobs, 3)
	want := []struct {
		lines []string
		cut   bool
		err   string
	}{
		{[]string{"$ go test", "100%", "ERROR: Job failed: exit code 1"}, true, ""},
		{[]string{"second", "third"}, true, ""},
		{[]string{"2026-10-28T09:00:00.0000000Z ##[group]Run go test"}, false, ""},
		{[]string{"two", "three", "four"}, true, ""},
		{nil, false, ""},
		{nil, false, "A trigger job has no log."},
		{nil, false, "GitLab has no log of it."},
	}
	for i, l := range logs {
		err := ""
		if l.Err != nil {
			err = l.Err.Error()
		}
		if l.Job.ID != jobs[i].ID || !reflect.DeepEqual(l.Lines, want[i].lines) || l.Cut != want[i].cut || err != want[i].err {
			t.Errorf("log %d is %+v, want %+v", i, l, want[i])
		}
	}
	if len(logs) != len(jobs) || len(stub.asked) != 6 || strings.Contains(strings.Join(stub.asked, ","), "6") || stub.tok != token || stub.keep <= 0 {
		t.Fatalf("the provider was asked for %v with %q keeping %d", stub.asked, stub.tok, stub.keep)
	}
}

// A host without a token asks nothing, and a provider that cannot read logs
// says so for every job.
func TestJobLogsNeedATokenAndAProviderThatReadsLogs(t *testing.T) {
	stub := &logStub{logs: map[string]string{"1": "log\n"}}
	s, b := logService(t, stub, plainProvider{})
	if logs := s.JobLogs(context.Background(), b, []forge.Job{{ID: "1"}}, 10); !errors.Is(logs[0].Err, forge.ErrNoToken) || len(stub.asked) != 0 {
		t.Fatalf("a host without a token: %+v, asked %v", logs, stub.asked)
	}
	plain := addedWithToken(t, s, forge.Input{Kind: "plain", WebURL: "https://plain.example.com"}, token)
	logs := s.JobLogs(context.Background(), forge.Binding{Host: plain, Path: "group/app"}, []forge.Job{{ID: "1"}, {ID: "2"}}, 10)
	if len(logs) != 2 || !errors.Is(logs[0].Err, forge.ErrNoLogs) || !errors.Is(logs[1].Err, forge.ErrNoLogs) {
		t.Fatalf("a provider without logs: %+v", logs)
	}
}

// endLogStub reads logs like its logStub on a forge that hands a job's log
// out only once the job ended.
type endLogStub struct {
	log *logStub
}

func (endLogStub) Kind() forge.Kind {
	return forge.Kind{ID: "ended", Label: "Ended"}
}

func (endLogStub) Test(context.Context, *forge.Client, forge.Host, string) (forge.Connection, error) {
	return forge.Connection{}, nil
}

func (p endLogStub) JobLog(ctx context.Context, c *forge.Client, b forge.Binding, token string, job forge.Job, keep int) ([]byte, bool, error) {
	return p.log.JobLog(ctx, c, b, token, job, keep)
}

// GitLab hands out the log of a running job as far as it is written, GitHub
// only once the job ended.
func TestGitLabHandsOutTheLogOfARunningJob(t *testing.T) {
	if _, ok := any(gitlab.Provider{}).(forge.LiveLogReader); !ok {
		t.Fatal("GitLab reads no running job's log")
	}
	if _, ok := any(github.Provider{}).(forge.LiveLogReader); ok {
		t.Fatal("GitHub reads a running job's log")
	}
}

// The log of a running job on a forge that hands it out while it runs is
// read and marked live, any other is not. On a forge that hands a log out
// only once its job ended, a job that waits or runs is not asked for and
// says so in the forge's name, one that ended or waits for a person is read.
func TestJobLogsFollowARunningJobWhereTheForgeHandsItsLogOut(t *testing.T) {
	logs := map[string]string{"1": "one\n", "2": "", "3": "three\n", "4": "four\n"}
	live := &logStub{logs: logs}
	s, b := logService(t, live)
	if err := s.ChangeToken("gitlab.com", token, false); err != nil {
		t.Fatal(err)
	}
	jobs := []forge.Job{{ID: "1", Status: forge.Running}, {ID: "2", Status: forge.Pending}, {ID: "3", Status: forge.Failed}, {ID: "4", Status: forge.Manual}}
	read := s.JobLogs(context.Background(), b, jobs, 10)
	for i, want := range []bool{true, false, false, false} {
		if read[i].Err != nil || read[i].Live != want {
			t.Errorf("on GitLab job %s reads %+v, live %v", jobs[i].ID, read[i], want)
		}
	}
	if len(live.asked) != 4 {
		t.Fatalf("GitLab was asked for %v", live.asked)
	}

	ended := &logStub{logs: logs}
	s, _ = logService(t, endLogStub{log: ended})
	host := addedWithToken(t, s, forge.Input{Kind: "ended", WebURL: "https://ended.example.com"}, token)
	read = s.JobLogs(context.Background(), forge.Binding{Host: host, Path: "group/app"}, jobs, 10)
	for i, l := range read {
		var later *forge.LogLater
		waits := jobs[i].Status == forge.Running || jobs[i].Status == forge.Pending
		if l.Live || waits != errors.As(l.Err, &later) || (waits && l.Err.Error() != "Ended hands out a job's log only once the job ended.") || (!waits && l.Err != nil) {
			t.Errorf("on a forge without live logs job %s reads %+v", jobs[i].ID, l)
		}
	}
	if strings.Join(ended.asked, ",") != "3,4" && strings.Join(ended.asked, ",") != "4,3" {
		t.Fatalf("a forge without live logs was asked for %v", ended.asked)
	}
}

// The jobs that failed a pipeline are those failed and not allowed to, a job
// is found by its id alone.
func TestAPipelineNamesItsFailingJobsAndFindsAJob(t *testing.T) {
	p := forge.Pipeline{Jobs: []forge.Job{
		{ID: "1", Status: forge.Success},
		{ID: "2", Status: forge.Failed, AllowFailure: true},
		{ID: "3", Status: forge.Failed},
		{ID: "", Name: "no id", Status: forge.Failed},
	}}
	failing := p.Failing()
	if len(failing) != 2 || failing[0].ID != "3" || failing[1].Name != "no id" {
		t.Fatalf("failing %+v", failing)
	}
	if j, ok := p.Job("2"); !ok || j.ID != "2" {
		t.Fatalf("job 2: %+v, %v", j, ok)
	}
	if _, ok := p.Job(""); ok {
		t.Fatal("a job found without an id")
	}
	if _, ok := p.Job("9"); ok {
		t.Fatal("a job found that is not there")
	}
}

// A redirect of a repository's read names the move as much as the web
// address, with and without where it leads.
func TestARedirectOfARepositoryReadNamesTheMove(t *testing.T) {
	b := forge.Binding{Host: gitlab.Builtin(), Path: "group/old"}
	fix := "which is not followed: if the repository was renamed or moved, give the origin its new path or set it under the project's Forge, else check the host's web address."
	if got := b.Redirected(&forge.RedirectError{Target: "https://gitlab.com/api/v4/projects/5"}).Error(); got != "The forge redirects the read of gitlab.com/group/old to https://gitlab.com/api/v4/projects/5, "+fix {
		t.Fatalf("with a target: %s", got)
	}
	if got := b.Redirected(&forge.RedirectError{}).Error(); got != "The forge answers the read of gitlab.com/group/old with a redirect, "+fix {
		t.Fatalf("without a target: %s", got)
	}
}
