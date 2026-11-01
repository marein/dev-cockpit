package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/marein/dev-cockpit/internal/forge"
)

// The bounds of one read: the pull requests of a branch, newest first, and
// the pages of a commit's check runs and commit statuses, at most 100 each.
const (
	pullsRead   = 20
	checksPage  = 100
	maxChecksAt = 10
)

type pull struct {
	Number   int     `json:"number"`
	Title    string  `json:"title"`
	Draft    bool    `json:"draft"`
	State    string  `json:"state"`
	MergedAt *string `json:"merged_at"`
	HTMLURL  string  `json:"html_url"`
	Base     struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Head struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
}

type checkRuns struct {
	TotalCount int `json:"total_count"`
	CheckRuns  []struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HTMLURL    string `json:"html_url"`
		App        *struct {
			Slug string `json:"slug"`
			Name string `json:"name"`
		} `json:"app"`
	} `json:"check_runs"`
}

// actions is the app GitHub Actions posts its check runs as, one per job
// and with the job's id.
const actions = "github-actions"

const (
	statusNoLog   = "A commit status has no log on GitHub."
	statusNoRetry = "GitHub cannot run a commit status again."
)

type combinedStatus struct {
	TotalCount int `json:"total_count"`
	Statuses   []struct {
		ID        int64  `json:"id"`
		Context   string `json:"context"`
		State     string `json:"state"`
		TargetURL string `json:"target_url"`
	} `json:"statuses"`
}

// BranchStatus reads the pull requests whose head is branch of the
// repository itself, asked as owner:branch and held against the head's
// repository, since a fork of the same owner shares the owner, none where
// the repository has pull requests switched off; and the check runs and
// commit statuses of head, combined into one pipeline. The combined status
// already holds the newest status of each context.
func (Provider) BranchStatus(ctx context.Context, c *forge.Client, b forge.Binding, token, branch, head string) (forge.BranchStatus, error) {
	a := api(c, b.Host, token)
	owner, name, ok := strings.Cut(b.Path, "/")
	if !ok || strings.Contains(name, "/") {
		return forge.BranchStatus{}, fmt.Errorf("A GitHub repository path is owner/name, %s is none.", b.Address())
	}
	repo := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	var status forge.BranchStatus
	query := url.Values{"head": {owner + ":" + branch}, "state": {"all"}, "sort": {"created"}, "direction": {"desc"}, "per_page": {strconv.Itoa(pullsRead)}}
	var pulls []pull
	if _, err := a.Get(ctx, repo+"/pulls?"+query.Encode(), &pulls); err != nil && !pullsSwitchedOff(ctx, a, b, err) {
		return status, readRefusal(a, b, err)
	}
	var changes []forge.Change
	for _, p := range pulls {
		if p.Head.Repo == nil || !strings.EqualFold(p.Head.Repo.FullName, b.Path) || p.Head.Ref != branch {
			continue
		}
		changes = append(changes, p.neutral())
	}
	status.Change = forge.PickChange(changes, head)
	if head == "" {
		return status, nil
	}
	commit := repo + "/commits/" + url.PathEscape(head)
	var jobs []forge.Job
	runsPartial, err := paged(func(page int) (int, int, error) {
		var runs checkRuns
		if _, err := a.Get(ctx, fmt.Sprintf("%s/check-runs?per_page=%d&page=%d", commit, checksPage, page), &runs); err != nil {
			return 0, 0, err
		}
		for _, r := range runs.CheckRuns {
			job := forge.Job{ID: strconv.FormatInt(r.ID, 10), Name: r.Name, Status: checkStatus(r.Status, r.Conclusion), URL: r.HTMLURL}
			switch {
			case r.App == nil:
				job.NoLog = "The check run is no GitHub Actions job, GitHub keeps no log of it."
				job.NoRetry = "The check run is no GitHub Actions job, GitHub cannot re-run it here."
			case r.App.Slug != actions:
				job.NoLog = fmt.Sprintf("The check run comes from %q, GitHub keeps no log of it.", r.App.Name)
				job.NoRetry = fmt.Sprintf("The check run comes from %q, only that app runs it again.", r.App.Name)
			}
			jobs = append(jobs, job)
		}
		return len(runs.CheckRuns), runs.TotalCount, nil
	})
	if err != nil {
		return status, readRefusal(a, b, err)
	}
	statusesPartial, err := paged(func(page int) (int, int, error) {
		var combined combinedStatus
		if _, err := a.Get(ctx, fmt.Sprintf("%s/status?per_page=%d&page=%d", commit, checksPage, page), &combined); err != nil {
			return 0, 0, err
		}
		for _, s := range combined.Statuses {
			jobs = append(jobs, forge.Job{ID: strconv.FormatInt(s.ID, 10), Name: s.Context, Status: commitStatus(s.State), URL: s.TargetURL, NoLog: statusNoLog, NoRetry: statusNoRetry})
		}
		return len(combined.Statuses), combined.TotalCount, nil
	})
	if err != nil {
		return status, readRefusal(a, b, err)
	}
	if len(jobs) == 0 {
		return status, nil
	}
	status.Pipeline = &forge.Pipeline{Status: forge.Rollup(jobs), URL: b.WebURL() + "/commit/" + url.PathEscape(head), Jobs: jobs, Partial: runsPartial || statusesPartial}
	return status, nil
}

// JobLog reads the log of an Actions job, whose id is its check run's. GitHub
// answers with a redirect to a download address that holds for a minute,
// read as a request of its own without the token.
func (Provider) JobLog(ctx context.Context, c *forge.Client, b forge.Binding, token string, j forge.Job, keep int) ([]byte, bool, error) {
	a := api(c, b.Host, token)
	target, err := a.Redirect(ctx, repository(b)+"/actions/jobs/"+url.PathEscape(j.ID)+"/logs")
	if err != nil {
		var status *forge.StatusError
		if errors.As(err, &status) {
			switch status.Code {
			case http.StatusForbidden:
				return nil, false, fmt.Errorf("The token may not read the Actions logs of %s.", b.Address())
			case http.StatusNotFound, http.StatusGone:
				return nil, false, fmt.Errorf("GitHub has no log of %q: the job has not finished, or its log has expired.", j.Name)
			}
		}
		return nil, false, readRefusal(a, b, err)
	}
	raw, cut, err := a.TailAt(ctx, target, keep)
	var status *forge.StatusError
	var redirect *forge.RedirectError
	switch {
	case err == nil:
		return raw, cut, nil
	case errors.As(err, &status):
		return nil, false, fmt.Errorf("The address GitHub hands out to download the log of %q answered %d %s, try again.", j.Name, status.Code, http.StatusText(status.Code))
	case errors.As(err, &redirect):
		return nil, false, fmt.Errorf("The address GitHub hands out to download the log of %q redirects further, which is not followed.", j.Name)
	}
	return nil, false, fmt.Errorf("The log of %q could not be downloaded. %s", j.Name, err)
}

// paged reads a list page by page, each read answering how many items it
// got and how many the list holds, until the list is in or the bound of
// pages is reached, which it reports.
func paged(read func(page int) (int, int, error)) (bool, error) {
	for page := 1; ; page++ {
		got, total, err := read(page)
		if err != nil {
			return false, err
		}
		if page*checksPage >= total || got < checksPage {
			return false, nil
		}
		if page == maxChecksAt {
			return true, nil
		}
	}
}

func (p pull) neutral() forge.Change {
	return forge.Change{Number: p.Number, Title: p.Title, Draft: p.Draft, State: pullState(p), URL: p.HTMLURL, Target: p.Base.Ref, Head: p.Head.SHA}
}

func pullState(p pull) forge.ChangeState {
	switch {
	case p.MergedAt != nil:
		return forge.Merged
	case p.State == "closed":
		return forge.Closed
	}
	return forge.Open
}

// checkStatus is a check run's status and conclusion in the neutral words.
// A neutral conclusion fails nothing and reads as skipped, one waiting for
// an action as waiting for a person, a stale one GitHub gave up on as
// canceled.
func checkStatus(status, conclusion string) forge.Status {
	switch status {
	case "in_progress":
		return forge.Running
	case "completed":
	default:
		return forge.Pending
	}
	switch conclusion {
	case "success":
		return forge.Success
	case "failure", "timed_out", "startup_failure":
		return forge.Failed
	case "cancelled", "stale":
		return forge.Canceled
	case "action_required":
		return forge.Manual
	}
	return forge.Skipped
}

func commitStatus(state string) forge.Status {
	switch state {
	case "success":
		return forge.Success
	case "failure", "error":
		return forge.Failed
	}
	return forge.Pending
}

// readRefusal says what an answer GitHub gave to a read of the repository
// means: a repository it hides, lacks or moved, else what refusal says of
// the token.
func readRefusal(a forge.API, b forge.Binding, err error) error {
	var redirect *forge.RedirectError
	if errors.As(err, &redirect) {
		return b.Redirected(redirect)
	}
	var status *forge.StatusError
	if errors.As(err, &status) {
		switch status.Code {
		case http.StatusForbidden:
			return fmt.Errorf("The token may not read the pull requests, check runs or commit statuses of %s.", b.Address())
		case http.StatusNotFound:
			return fmt.Errorf("GitHub knows no repository %s, or the token may not read it.", b.Address())
		}
	}
	return refusal(a, err)
}
