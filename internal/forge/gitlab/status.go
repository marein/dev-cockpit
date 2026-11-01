package gitlab

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/marein/dev-cockpit/internal/forge"
)

// The bounds of one read: the merge requests of a branch, newest first, the
// pipelines of a commit, and the pages of a list read page by page, the
// lists a pipeline's jobs are read from and a merge request's discussions.
const (
	mergeRequestsRead = 20
	pipelinesRead     = 20
	perPage           = 100
	maxPages          = 10
)

// The words for a job GitLab keeps no log of, and for one it cannot run
// again.
const (
	bridgeNoLog     = "A trigger job has no log, the jobs of its downstream pipeline do."
	externalNoLog   = "An external commit status has no log on GitLab."
	externalNoRetry = "GitLab cannot run an external commit status again."
)

type mergeRequest struct {
	IID             int    `json:"iid"`
	Title           string `json:"title"`
	Draft           bool   `json:"draft"`
	State           string `json:"state"`
	WebURL          string `json:"web_url"`
	TargetBranch    string `json:"target_branch"`
	SHA             string `json:"sha"`
	SourceProjectID int64  `json:"source_project_id"`
	TargetProjectID int64  `json:"target_project_id"`
}

type pipeline struct {
	ID     int64  `json:"id"`
	Ref    string `json:"ref"`
	Status string `json:"status"`
	WebURL string `json:"web_url"`
}

type job struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Stage        string `json:"stage"`
	Status       string `json:"status"`
	WebURL       string `json:"web_url"`
	AllowFailure bool   `json:"allow_failure"`
}

type bridge struct {
	job
	Downstream *struct {
		WebURL string `json:"web_url"`
	} `json:"downstream_pipeline"`
}

type commitStatus struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	TargetURL    string `json:"target_url"`
	AllowFailure bool   `json:"allow_failure"`
	PipelineID   int64  `json:"pipeline_id"`
}

// BranchStatus reads the merge requests of the project whose source branch
// is branch, and the newest pipeline of head that ran for the branch or for
// the picked merge request, with what it ran. The project is named by its
// path, subgroups and all, encoded into one segment.
func (Provider) BranchStatus(ctx context.Context, c *forge.Client, b forge.Binding, token, branch, head string) (forge.BranchStatus, error) {
	a := api(c, b.Host, token)
	project := "/projects/" + url.PathEscape(b.Path)
	var status forge.BranchStatus
	query := url.Values{"source_branch": {branch}, "order_by": {"created_at"}, "sort": {"desc"}, "per_page": {strconv.Itoa(mergeRequestsRead)}}
	var found []mergeRequest
	if _, err := a.Get(ctx, project+"/merge_requests?"+query.Encode(), &found); err != nil {
		return status, readRefusal(a, b, err)
	}
	var changes []forge.Change
	for _, mr := range found {
		if mr.SourceProjectID != mr.TargetProjectID {
			continue
		}
		changes = append(changes, mr.neutral())
	}
	status.Change = forge.PickChange(changes, head)
	if head == "" {
		return status, nil
	}
	refs := []string{branch}
	if status.Change != nil {
		refs = append(refs, fmt.Sprintf("refs/merge-requests/%d/head", status.Change.Number))
	}
	query = url.Values{"sha": {head}, "order_by": {"id"}, "sort": {"desc"}, "per_page": {strconv.Itoa(pipelinesRead)}}
	var pipelines []pipeline
	if _, err := a.Get(ctx, project+"/pipelines?"+query.Encode(), &pipelines); err != nil {
		return status, readRefusal(a, b, err)
	}
	at := slices.IndexFunc(pipelines, func(p pipeline) bool { return slices.Contains(refs, p.Ref) })
	if at < 0 {
		return status, nil
	}
	p := pipelines[at]
	jobs, partial, err := pipelineJobs(ctx, a, project, p, head)
	if err != nil {
		return status, readRefusal(a, b, err)
	}
	status.Pipeline = &forge.Pipeline{ID: strconv.FormatInt(p.ID, 10), Status: jobStatus(p.Status), URL: p.WebURL, Jobs: jobs, Partial: partial}
	return status, nil
}

// pipelineJobs reads what a pipeline ran, the newest try of each, in the
// order the pipeline made them: its jobs, its trigger jobs, and the external
// commit statuses posted to it, which the commit's statuses hold among the
// jobs and trigger jobs of every pipeline of the commit. A GitLab before
// 16.3 names no pipeline of a status, there they stay out, a job of another
// pipeline on the same ref would pass for an external one. It says whether
// a list had more than the pages it reads; then the statuses are not read,
// a job left out would read as an external one.
func pipelineJobs(ctx context.Context, a forge.API, project string, p pipeline, head string) ([]forge.Job, bool, error) {
	builds, partial, err := pages[job](ctx, a, fmt.Sprintf("%s/pipelines/%d/jobs", project, p.ID), nil)
	if err != nil {
		return nil, false, err
	}
	bridges, bridgesPartial, err := pages[bridge](ctx, a, fmt.Sprintf("%s/pipelines/%d/bridges", project, p.ID), nil)
	if err != nil {
		return nil, false, err
	}
	partial = partial || bridgesPartial
	var statuses []commitStatus
	if !partial {
		query := url.Values{"ref": {p.Ref}, "pipeline_id": {strconv.FormatInt(p.ID, 10)}}
		if statuses, partial, err = pages[commitStatus](ctx, a, project+"/repository/commits/"+url.PathEscape(head)+"/statuses", query); err != nil {
			return nil, false, err
		}
	}
	seen := map[int64]bool{}
	var out []forge.Job
	for _, j := range builds {
		seen[j.ID] = true
		out = append(out, j.neutral())
	}
	for _, br := range bridges {
		seen[br.ID] = true
		j := br.neutral()
		if br.Downstream != nil && br.Downstream.WebURL != "" {
			j.URL = br.Downstream.WebURL
		}
		j.NoLog = bridgeNoLog
		out = append(out, j)
	}
	for _, st := range statuses {
		if seen[st.ID] || st.PipelineID != p.ID {
			continue
		}
		seen[st.ID] = true
		out = append(out, forge.Job{ID: strconv.FormatInt(st.ID, 10), Name: st.Name, Status: jobStatus(st.Status), URL: st.TargetURL, AllowFailure: st.AllowFailure, NoLog: externalNoLog, NoRetry: externalNoRetry})
	}
	slices.SortStableFunc(out, func(x, y forge.Job) int {
		a, _ := strconv.ParseInt(x.ID, 10, 64)
		b, _ := strconv.ParseInt(y.ID, 10, 64)
		return cmp.Compare(a, b)
	})
	return out, partial, nil
}

// pages reads a list page by page and says whether it had more than the
// pages it reads.
func pages[T any](ctx context.Context, a forge.API, path string, query url.Values) ([]T, bool, error) {
	var all []T
	for page := 1; ; page++ {
		q := url.Values{"per_page": {strconv.Itoa(perPage)}, "page": {strconv.Itoa(page)}}
		for name, values := range query {
			q[name] = values
		}
		var found []T
		header, err := a.Get(ctx, path+"?"+q.Encode(), &found)
		if err != nil {
			return nil, false, err
		}
		all = append(all, found...)
		if header.Get("X-Next-Page") == "" || len(found) == 0 {
			return all, false, nil
		}
		if page == maxPages {
			return all, true, nil
		}
	}
}

func (mr mergeRequest) neutral() forge.Change {
	return forge.Change{Number: mr.IID, Title: mr.Title, Draft: mr.Draft, State: changeState(mr.State), URL: mr.WebURL, Target: mr.TargetBranch, Head: mr.SHA}
}

func (j job) neutral() forge.Job {
	return forge.Job{ID: strconv.FormatInt(j.ID, 10), Name: j.Name, Stage: j.Stage, Status: jobStatus(j.Status), URL: j.WebURL, AllowFailure: j.AllowFailure}
}

// JobLog reads a job's trace, the log the runner wrote, which GitLab answers
// whole as plain text.
func (Provider) JobLog(ctx context.Context, c *forge.Client, b forge.Binding, token string, j forge.Job, keep int) ([]byte, bool, error) {
	a := api(c, b.Host, token)
	raw, cut, err := a.Tail(ctx, "/projects/"+url.PathEscape(b.Path)+"/jobs/"+url.PathEscape(j.ID)+"/trace", keep)
	if err == nil {
		return raw, cut, nil
	}
	var status *forge.StatusError
	if errors.As(err, &status) {
		switch {
		case status.Code == http.StatusForbidden && !insufficientScope(status.Body):
			return nil, false, fmt.Errorf("The token may not read the log of %q on GitLab.", j.Name)
		case status.Code == http.StatusNotFound:
			return nil, false, fmt.Errorf("GitLab has no log of %q in %s, or the token may not read it.", j.Name, b.Address())
		}
	}
	return nil, false, readRefusal(a, b, err)
}

// LiveLogs marks that GitLab hands out the trace of a running job, as far as
// its runner sent it.
func (Provider) LiveLogs() {}

// jobStatus is a pipeline's or job's status in the neutral words. One GitLab
// adds later is taken as not finished, so nothing reads it as an end.
func jobStatus(status string) forge.Status {
	switch status {
	case "running", "canceling":
		return forge.Running
	case "success":
		return forge.Success
	case "failed":
		return forge.Failed
	case "canceled":
		return forge.Canceled
	case "skipped":
		return forge.Skipped
	case "manual":
		return forge.Manual
	}
	return forge.Pending
}

// changeState is a merge request's state in the neutral words. A locked one
// is being merged and still open.
func changeState(state string) forge.ChangeState {
	switch state {
	case "merged":
		return forge.Merged
	case "closed":
		return forge.Closed
	}
	return forge.Open
}

// readRefusal says what an answer GitLab gave to a read of the project
// means: a project it hides, lacks or moved, else what refusal says of the
// token.
func readRefusal(a forge.API, b forge.Binding, err error) error {
	var redirect *forge.RedirectError
	if errors.As(err, &redirect) {
		return b.Redirected(redirect)
	}
	var status *forge.StatusError
	if errors.As(err, &status) {
		switch {
		case status.Code == http.StatusForbidden && !insufficientScope(status.Body):
			return fmt.Errorf("The token may not read %s on GitLab.", b.Address())
		case status.Code == http.StatusNotFound:
			return fmt.Errorf("GitLab knows no project %s, or the token may not read it.", b.Address())
		}
	}
	return refusal(a, err)
}
