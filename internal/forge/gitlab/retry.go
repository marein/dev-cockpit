package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/marein/dev-cockpit/internal/forge"
)

// notRetryable is how GitLab refuses to retry a job that cannot run again: one
// retried already, archived, or a deployment its environment rejected.
const notRetryable = "403 Forbidden - Job is not retryable"

// RetryPipeline has GitLab retry the pipeline, which runs its failed and
// canceled jobs again, then retries each of its failed trigger jobs on its
// own, which GitLab's retry of a pipeline leaves as they are.
func (Provider) RetryPipeline(ctx context.Context, c *forge.Client, b forge.Binding, token, _ string, p forge.Pipeline) error {
	a := api(c, b.Host, token)
	if err := a.Post(ctx, "/projects/"+url.PathEscape(b.Path)+"/pipelines/"+url.PathEscape(p.ID)+"/retry", nil, nil); err != nil {
		return writeRefusal(a, b, err, "retry pipeline #"+p.ID, "pipeline #"+p.ID)
	}
	for _, j := range p.Jobs {
		if j.Status != forge.Failed || j.NoLog != bridgeNoLog {
			continue
		}
		if err := retryJob(ctx, a, b, j); err != nil {
			return fmt.Errorf("Pipeline #%s runs its failed jobs again, the trigger job %q does not. %w", p.ID, j.Name, err)
		}
	}
	return nil
}

// RetryJob has GitLab retry one job, a trigger job too, which runs it again
// as a new job of the same pipeline.
func (Provider) RetryJob(ctx context.Context, c *forge.Client, b forge.Binding, token string, j forge.Job) error {
	return retryJob(ctx, api(c, b.Host, token), b, j)
}

func retryJob(ctx context.Context, a forge.API, b forge.Binding, j forge.Job) error {
	err := a.Post(ctx, "/projects/"+url.PathEscape(b.Path)+"/jobs/"+url.PathEscape(j.ID)+"/retry", nil, nil)
	if err == nil {
		return nil
	}
	var status *forge.StatusError
	if errors.As(err, &status) {
		switch {
		case status.Code == http.StatusForbidden && status.Message() == notRetryable:
			return fmt.Errorf("GitLab cannot retry %q: it ran again already, is archived, or its environment refused it.", j.Name)
		case status.Code == http.StatusNotFound && j.NoLog == bridgeNoLog:
			return fmt.Errorf("This GitLab retries no trigger job over its API, retry %q on its page.", j.Name)
		case status.Code == http.StatusBadRequest && status.Message() != "":
			return fmt.Errorf("GitLab refused to retry %q: %s.", j.Name, strings.TrimRight(status.Message(), "."))
		}
	}
	what := fmt.Sprintf("job %q", j.Name)
	return writeRefusal(a, b, err, "retry "+what, what)
}

// writeRefusal says what an answer GitLab gave to a write on the project
// means, act naming the write: a token without the scope api, an account
// whose role may not, what of the project the write names, where it names
// something, that GitLab hides or lacks, a write GitLab refuses in words of
// its own, else what readRefusal says.
func writeRefusal(a forge.API, b forge.Binding, err error, act, what string) error {
	var status *forge.StatusError
	if errors.As(err, &status) {
		switch {
		case status.Code == http.StatusForbidden && insufficientScope(status.Body):
			return errors.New("The token may not write to GitLab: give it the scope api.")
		case status.Code == http.StatusForbidden:
			return fmt.Errorf("The token's account may not %s in %s, it needs the Developer role there.", act, b.Address())
		case status.Code == http.StatusNotFound && what != "":
			return fmt.Errorf("GitLab knows no %s in %s, or the token may not read it.", what, b.Address())
		case (status.Code == http.StatusConflict || status.Code == http.StatusBadRequest || status.Code == http.StatusUnprocessableEntity) && status.Message() != "":
			return fmt.Errorf("GitLab refused to %s: %s.", act, strings.TrimRight(status.Message(), "."))
		}
	}
	return readRefusal(a, b, err)
}
