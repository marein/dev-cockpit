package forge

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// Status is where a pipeline or one of its jobs stands, in the same few
// words for every forge.
type Status string

const (
	Pending  Status = "pending"
	Running  Status = "running"
	Success  Status = "success"
	Failed   Status = "failed"
	Canceled Status = "canceled"
	Skipped  Status = "skipped"
	Manual   Status = "manual"
)

// ChangeState is where a change request stands.
type ChangeState string

const (
	Open   ChangeState = "open"
	Merged ChangeState = "merged"
	Closed ChangeState = "closed"
)

// Change is a change request, a merge request on GitLab and a pull request
// on GitHub: its number, title, whether it is a draft, where it stands, its
// page, the branch it merges into and the commit its source branch stands
// at on the forge.
type Change struct {
	Number int
	Title  string
	Draft  bool
	State  ChangeState
	URL    string
	Target string
	Head   string
}

// Pipeline is what a forge ran on one commit: a GitLab pipeline, or the
// check runs and commit statuses of a commit on GitHub, combined. ID is the
// forge's own where it numbers pipelines. Partial says the jobs are the
// first ones of more than one read takes.
type Pipeline struct {
	ID      string
	Status  Status
	URL     string
	Jobs    []Job
	Partial bool
}

// Job is one step of a pipeline: a GitLab job, trigger job or external
// commit status, a GitHub check run or commit status. ID is the forge's own
// where it numbers them, Stage the group a forge runs it in where it has
// one. AllowFailure marks a job whose failure does not fail the pipeline.
// NoLog says in the provider's words why the forge keeps no log of the job,
// "" when it keeps one, NoRetry why the forge cannot run it again, "" when
// it can.
type Job struct {
	ID           string
	Name         string
	Stage        string
	Status       Status
	URL          string
	AllowFailure bool
	NoLog        string
	NoRetry      string
}

// Opened reports whether c is a change request that stands open, false for
// none.
func (c *Change) Opened() bool {
	return c != nil && c.State == Open
}

// Failing are the jobs that failed the pipeline, those failed and not
// allowed to.
func (p Pipeline) Failing() []Job {
	var failing []Job
	for _, j := range p.Jobs {
		if j.Status == Failed && !j.AllowFailure {
			failing = append(failing, j)
		}
	}
	return failing
}

// Job is the pipeline's job with that id.
func (p Pipeline) Job(id string) (Job, bool) {
	at := slices.IndexFunc(p.Jobs, func(j Job) bool { return j.ID == id && id != "" })
	if at < 0 {
		return Job{}, false
	}
	return p.Jobs[at], true
}

// BranchStatus is what a forge says about one branch: its change request and
// the pipeline of the commit it was last pushed at, each nil when there is
// none, and for a pushed branch without an open change request the branch of
// the repository one merges into unless it names another, its default
// branch, "" where the forge opens none or did not tell.
type BranchStatus struct {
	Change   *Change
	Pipeline *Pipeline
	Default  string
}

var (
	// ErrNoToken answers a read on a host without a token, which asks the
	// forge nothing.
	ErrNoToken = errors.New("The host has no token: add one on its row under Settings › Forges.")
	// ErrNoStatus answers a read on a forge whose provider cannot read
	// change requests and pipelines.
	ErrNoStatus = errors.New("This forge cannot read change requests and pipelines.")
)

// Redirected words a redirect a read of b's repository got. A forge answers
// so for a repository renamed or moved whose old path the origin still
// names, as much as for a host whose web address is off.
func (b Binding) Redirected(r *RedirectError) error {
	fix := "if the repository was renamed or moved, give the origin its new path or set it under the project's Forge, else check the host's web address."
	if r.Target == "" {
		return fmt.Errorf("The forge answers the read of %s with a redirect, which is not followed: %s", b.Address(), fix)
	}
	return fmt.Errorf("The forge redirects the read of %s to %s, which is not followed: %s", b.Address(), r.Target, fix)
}

// BranchStatus asks the forge w is bound to for the change request of w's
// branch by its name there, and the pipeline of the commit the branch was
// last pushed at, none for a branch never seen pushed; and where the branch
// is pushed without an open change request for the default branch of the
// repository too. That read failing fails none of the rest, which stands
// without it. A branch the forge features are off for is not read, and one
// whose change request the forge answers merging into such a branch answers
// BranchOff too.
func (s *Service) BranchStatus(ctx context.Context, w Watch) (BranchStatus, error) {
	if err := w.Refused(); err != nil {
		return BranchStatus{}, err
	}
	b := w.Binding
	reader, ok := s.statusReader(b)
	if !ok {
		return BranchStatus{}, ErrNoStatus
	}
	token, err := s.token(b.Host.ID)
	if err != nil {
		return BranchStatus{}, err
	}
	if token == "" {
		return BranchStatus{}, ErrNoToken
	}
	status, err := reader.BranchStatus(ctx, s.client, b, token, w.Remote, w.Head)
	if err != nil {
		return status, err
	}
	if err := targetRefused(b.Host.Kind, status.Change); err != nil {
		return BranchStatus{}, err
	}
	if w.Head == "" || status.Change.Opened() {
		return status, nil
	}
	if opener, ok := s.changeOpener(b); ok {
		status.Default, _ = s.defaultBranch(ctx, opener, b, token)
	}
	return status, nil
}

func (s *Service) statusReader(b Binding) (StatusReader, bool) {
	p, ok := s.provider(b.Host.Kind.ID)
	if !ok {
		return nil, false
	}
	reader, ok := p.(StatusReader)
	return reader, ok
}

// PickChange picks the change request a branch stands for out of those of
// the bound repository whose source it is, newest first: the newest open
// one, else the newest merged or closed one at head, so an old one of a
// branch name used again stays out.
func PickChange(changes []Change, head string) *Change {
	for i := range changes {
		if changes[i].State == Open {
			return &changes[i]
		}
	}
	for i := range changes {
		if head != "" && changes[i].Head == head {
			return &changes[i]
		}
	}
	return nil
}

// Rollup is the status of a pipeline made of its jobs alone, the way a forge
// without pipelines of its own has it: running while any job runs, pending
// while any waits to, then failed, canceled or waiting for a person where
// any job ended so, skipped when every job was, else success. A job allowed
// to fail fails nothing.
func Rollup(jobs []Job) Status {
	seen := map[Status]bool{}
	for _, j := range jobs {
		if j.Status == Failed && j.AllowFailure {
			continue
		}
		seen[j.Status] = true
	}
	for _, s := range []Status{Running, Pending, Failed, Canceled, Manual} {
		if seen[s] {
			return s
		}
	}
	if len(seen) == 1 && seen[Skipped] {
		return Skipped
	}
	return Success
}
