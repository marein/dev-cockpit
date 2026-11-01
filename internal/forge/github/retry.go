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

// runsRead bounds the workflow runs of a commit a re-run reads.
const runsRead = 100

type workflowRuns struct {
	WorkflowRuns []workflowRun `json:"workflow_runs"`
}

type workflowRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// RetryPipeline re-runs the failed jobs of every GitHub Actions workflow run
// of head that failed, with the jobs that depend on them, which GitHub does
// one run at a time. A check run of another app and a commit status are no
// job of a run, BranchStatus says why they cannot run again.
func (Provider) RetryPipeline(ctx context.Context, c *forge.Client, b forge.Binding, token, head string, _ forge.Pipeline) error {
	a := api(c, b.Host, token)
	query := url.Values{"head_sha": {head}, "exclude_pull_requests": {"true"}, "per_page": {strconv.Itoa(runsRead)}}
	var runs workflowRuns
	if _, err := a.Get(ctx, repository(b)+"/actions/runs?"+query.Encode(), &runs); err != nil {
		return writeRefusal(a, b, err, "workflow run at the pushed head")
	}
	var done []string
	for _, r := range runs.WorkflowRuns {
		if r.HeadSHA != head || r.Status != "completed" || (r.Conclusion != "failure" && r.Conclusion != "timed_out") {
			continue
		}
		if err := a.Post(ctx, fmt.Sprintf("%s/actions/runs/%d/rerun-failed-jobs", repository(b), r.ID), nil, nil); err != nil {
			err = writeRefusal(a, b, err, fmt.Sprintf("workflow run %q", r.Name))
			if len(done) == 0 {
				return err
			}
			return fmt.Errorf("The failed jobs of %s run again, those of %q do not. %w", strings.Join(done, ", "), r.Name, err)
		}
		done = append(done, strconv.Quote(r.Name))
	}
	if len(done) == 0 {
		return errors.New("GitHub lists no failed workflow run at the pushed head.")
	}
	return nil
}

// RetryJob re-runs one GitHub Actions job, whose id is its check run's, with
// the jobs that depend on it.
func (Provider) RetryJob(ctx context.Context, c *forge.Client, b forge.Binding, token string, j forge.Job) error {
	a := api(c, b.Host, token)
	if err := a.Post(ctx, repository(b)+"/actions/jobs/"+url.PathEscape(j.ID)+"/rerun", nil, nil); err != nil {
		return writeRefusal(a, b, err, fmt.Sprintf("job %q", j.Name))
	}
	return nil
}

// repository is the API's path of the repository b binds, owner/name.
func repository(b forge.Binding) string {
	owner, name, _ := strings.Cut(b.Path, "/")
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// tokenScope says what a token needs to write what permission covers.
func tokenScope(permission string) string {
	return fmt.Sprintf("a fine grained token needs the permission %s read and write, a classic one the scope repo.", permission)
}

// writeRefusal says what an answer GitHub gave to a re-run of what means: a
// token that may not, which GitHub answers with 403, or with 404 where it
// hides the repository from the token; a re-run GitHub refuses in words of
// its own; else what readRefusal says.
func writeRefusal(a forge.API, b forge.Binding, err error, what string) error {
	var status *forge.StatusError
	if !errors.As(err, &status) {
		return readRefusal(a, b, err)
	}
	scope := tokenScope("Actions")
	message := status.Message()
	switch {
	case status.Code == http.StatusForbidden && (message == "" || strings.HasPrefix(message, "Resource not accessible") || strings.HasPrefix(message, "Must have admin rights")):
		return fmt.Errorf("The token may not re-run jobs of %s: %s", b.Address(), scope)
	case status.Code == http.StatusNotFound:
		return fmt.Errorf("GitHub knows no %s in %s, or the token may not re-run it: %s", what, b.Address(), scope)
	case message != "" && (status.Code == http.StatusForbidden || status.Code == http.StatusConflict || status.Code == http.StatusUnprocessableEntity):
		return fmt.Errorf("GitHub refused to re-run %s: %s.", what, strings.TrimRight(message, "."))
	}
	return readRefusal(a, b, err)
}
