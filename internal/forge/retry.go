package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNoRetry answers a retry on a forge whose provider cannot run failed
// jobs again.
var ErrNoRetry = errors.New("This forge cannot run failed jobs again.")

// Retry names what a retry runs again: the pipeline of a branch at the
// commit the branch was pushed at, by the forge's id where it numbers
// pipelines, and one of its jobs by its id, "" for the jobs that failed the
// pipeline. A Head or Pipeline left "" takes the one that stands.
type Retry struct {
	Head     string
	Pipeline string
	Job      string
}

// Retried is what a retry runs again as the forge has it: the pipeline of a
// branch at the commit the branch was pushed at, and the job, nil for the
// jobs that failed the pipeline.
type Retried struct {
	Head     string
	Pipeline Pipeline
	Job      *Job
}

// Retry names r in full, the way a retry of the same asks for it again.
func (r Retried) Retry() Retry {
	named := Retry{Head: r.Head, Pipeline: r.Pipeline.ID}
	if r.Job != nil {
		named.Job = r.Job.ID
	}
	return named
}

// RetryRefusal is a retry refused before the forge was asked to run anything:
// what it names is not where the forge has it now, or cannot run again.
type RetryRefusal struct {
	Reason string
}

func (e *RetryRefusal) Error() string {
	return e.Reason
}

// RetryBlocked says why a retry of the pipeline would run none of the jobs
// that failed it again, "" when it runs one.
func (p Pipeline) RetryBlocked() string {
	failing := p.Failing()
	if len(failing) == 0 {
		return "No listed job failed the pipeline, none runs again."
	}
	if len(p.LeftOut()) < len(failing) {
		return ""
	}
	return "No job that failed the pipeline can run again. " + Unretried(failing)
}

// LeftOut are the jobs that failed the pipeline which the forge cannot run
// again, those a retry of the pipeline leaves as they are.
func (p Pipeline) LeftOut() []Job {
	var out []Job
	for _, j := range p.Failing() {
		if j.NoRetry != "" {
			out = append(out, j)
		}
	}
	return out
}

// Unretried says of each job by its name why the forge cannot run it again.
func Unretried(jobs []Job) string {
	parts := make([]string, 0, len(jobs))
	for _, j := range jobs {
		parts = append(parts, fmt.Sprintf("%q: %s", j.Name, j.NoRetry))
	}
	return strings.Join(parts, " ")
}

// RetryTarget reads w's branch from the forge and answers what a retry of r
// runs again, refused where the branch moved on from r's head, the pipeline
// is another than r's, or what r names did not fail or cannot run again.
func (s *Service) RetryTarget(ctx context.Context, w Watch, r Retry) (Retried, error) {
	if _, _, err := s.retrier(w.Binding); err != nil {
		return Retried{}, err
	}
	return s.retryTarget(ctx, w, r)
}

// Retry runs again what RetryTarget answers for r and has the poller read
// the branch at once and at the fast pace a while, its pipeline running
// again, also where the forge refused: a refusal may come after part of the
// retry ran.
func (s *Service) Retry(ctx context.Context, w Watch, r Retry) (Retried, error) {
	retrier, token, err := s.retrier(w.Binding)
	if err != nil {
		return Retried{}, err
	}
	target, err := s.retryTarget(ctx, w, r)
	if err != nil {
		return Retried{}, err
	}
	if target.Job != nil {
		err = retrier.RetryJob(ctx, s.client, w.Binding, token, *target.Job)
	} else {
		err = retrier.RetryPipeline(ctx, s.client, w.Binding, token, target.Head, target.Pipeline)
	}
	s.poll.wrote(w)
	if err != nil {
		return Retried{}, err
	}
	return target, nil
}

func (s *Service) retryTarget(ctx context.Context, w Watch, r Retry) (Retried, error) {
	refuse := func(format string, args ...any) (Retried, error) {
		return Retried{}, &RetryRefusal{Reason: fmt.Sprintf(format, args...)}
	}
	switch {
	case w.Head == "":
		return refuse("%q is not pushed, it has no pipeline to run again.", w.Branch)
	case r.Head != "" && r.Head != w.Head:
		return refuse("%q was pushed again and stands at %s now, its pipeline is another one.", w.Branch, ShortHead(w.Head))
	}
	status, err := s.Status(ctx, w, true)
	if err != nil {
		return Retried{}, err
	}
	p := status.Pipeline
	switch {
	case p == nil:
		return refuse("%q has no pipeline at its pushed head %s.", w.Branch, ShortHead(w.Head))
	case r.Pipeline != "" && p.ID == "":
		return refuse("%s numbers no pipelines, name none.", w.Binding.Host.Kind.Label)
	case r.Pipeline != "" && r.Pipeline != p.ID:
		return refuse("The pipeline of %q is #%s now, not #%s.", w.Branch, p.ID, r.Pipeline)
	}
	target := Retried{Head: w.Head, Pipeline: *p}
	if r.Job == "" {
		if p.Status != Failed {
			return refuse("The pipeline of %q is %s, not failed.", w.Branch, p.Status)
		}
		if blocked := p.RetryBlocked(); blocked != "" {
			return Retried{}, &RetryRefusal{Reason: blocked}
		}
		return target, nil
	}
	job, ok := p.Job(r.Job)
	switch {
	case !ok:
		return refuse("The pipeline of %q has no job %q, a job that ran again stands under a new id.", w.Branch, r.Job)
	case job.Status != Failed:
		return refuse("The job %q is %s, not failed.", job.Name, job.Status)
	case job.NoRetry != "":
		return Retried{}, &RetryRefusal{Reason: job.NoRetry}
	}
	target.Job = &job
	return target, nil
}

func (s *Service) retrier(b Binding) (Retrier, string, error) {
	p, ok := s.provider(b.Host.Kind.ID)
	if !ok {
		return nil, "", ErrNoRetry
	}
	retrier, ok := p.(Retrier)
	if !ok {
		return nil, "", ErrNoRetry
	}
	token, err := s.token(b.Host.ID)
	if err != nil {
		return nil, "", err
	}
	if token == "" {
		return nil, "", ErrNoToken
	}
	return retrier, token, nil
}

// ShortHead is a commit the way a sentence names it, by its first twelve
// characters.
func ShortHead(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
