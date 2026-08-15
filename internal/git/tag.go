package git

import (
	"context"
	"errors"
	"strings"
)

// Tag names a commit. A message makes it an annotated tag, which is what a
// release is: it carries who tagged and when, and git refuses it without an
// identity, in git's words like every other refusal here. Without a message it
// is the lightweight kind, a name on a commit and nothing else. A name git
// does not accept, or one that is already taken, comes back the same way; this
// call never moves an existing tag, because a tag that quietly moves is a
// release that means two different things to two people.
func (r *Repo) Tag(ctx context.Context, name, rev, message string) error {
	if err := validTagArg(name); err != nil {
		return err
	}
	args := []string{"tag"}
	if strings.TrimSpace(message) != "" {
		args = append(args, "-a", "-m", message)
	}
	args = append(args, name)
	if rev = strings.TrimSpace(rev); rev != "" {
		if strings.HasPrefix(rev, "-") {
			return errors.New("A commit is required.")
		}
		args = append(args, rev)
	}
	_, err := r.run(ctx, args, nil)
	return err
}

// PushTag sends one tag to the remote, and only that tag: a push of
// everything the repository happens to hold locally is somebody else's
// decision to make on the command line. Which remote is the same question the
// upstream of a new branch asks, so it has the same answer, the single
// configured one or origin among several; with nothing to name this says so
// instead of guessing a destination for something as public as a release.
func (r *Repo) PushTag(ctx context.Context, name string) error {
	if err := validTagArg(name); err != nil {
		return err
	}
	remote, ok := r.pickRemote(ctx)
	if !ok {
		return errors.New("There is no single remote to push the tag to. Push it from the command line.")
	}
	w := *r
	w.timeout = remoteTimeout
	_, err := w.run(ctx, []string{"push", remote, "refs/tags/" + name}, nil)
	return err
}

// DeleteTag takes the name away here and says nothing about what a remote
// holds: a tag that was pushed stays where it was pushed until somebody says
// otherwise, which is DeleteRemoteTag's own call. A name this repository does
// not have comes back in git's words.
func (r *Repo) DeleteTag(ctx context.Context, name string) error {
	if err := validTagArg(name); err != nil {
		return err
	}
	_, err := r.run(ctx, []string{"tag", "-d", name}, nil)
	return err
}

// DeleteRemoteTag takes the tag off the remote, which is the half of a
// deletion everybody else sees, so it is never implied by the local one and
// always asked for. The remote is the same unambiguous one the tag was pushed
// to; a tag the remote does not hold is git's answer and not ours.
func (r *Repo) DeleteRemoteTag(ctx context.Context, name string) error {
	if err := validTagArg(name); err != nil {
		return err
	}
	remote, ok := r.pickRemote(ctx)
	if !ok {
		return errors.New("There is no single remote to take the tag off. Do it from the command line.")
	}
	w := *r
	w.timeout = remoteTimeout
	_, err := w.run(ctx, []string{"push", remote, "--delete", "refs/tags/" + name}, nil)
	return err
}

// validTagArg refuses what could read as an option; everything else about the
// name is git's call, and it has more rules about a ref name than are worth
// repeating here.
func validTagArg(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("A tag name is required.")
	}
	if strings.HasPrefix(name, "-") {
		return errors.New("A tag name must not start with a dash.")
	}
	return nil
}
