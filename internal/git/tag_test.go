package git

import (
	"context"
	"strings"
	"testing"
)

func TestTagNamesACommitAndTheHistoryShowsIt(t *testing.T) {
	work, _ := remotePair(t)
	head := headOf(t, work)

	if err := New(work).Tag(context.Background(), "v1.0.0", head, ""); err != nil {
		t.Fatalf("tag: %v", err)
	}
	if got := gitOut(t, work, "rev-parse", "v1.0.0^{commit}"); got != head {
		t.Fatalf("the tag sits on %s instead of %s", got, head)
	}

	page, err := New(work).Log(context.Background(), "", 0, 10)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(page.Commits) == 0 {
		t.Fatal("the history is empty")
	}
	if got := strings.Join(page.Commits[0].Tags, ","); got != "v1.0.0" {
		t.Fatalf("the newest commit carries %q", got)
	}

	// A tag names a commit, a branch says where the repository stands: the
	// decoration carries both and only the tag belongs in the answer.
	for _, tag := range page.Commits[0].Tags {
		if strings.Contains(tag, "master") || strings.Contains(tag, "HEAD") {
			t.Fatalf("a branch travelled as a tag: %q", tag)
		}
	}

	// An older commit without a tag carries none at all.
	writeAt(t, work, "b.txt", "b\n")
	if _, err := New(work).Commit(context.Background(), "second", []string{"b.txt"}, false); err != nil {
		t.Fatalf("commit: %v", err)
	}
	page, err = New(work).Log(context.Background(), "", 0, 10)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if page.Commits[0].Tags != nil {
		t.Fatalf("the untagged commit carries %v", page.Commits[0].Tags)
	}
	if got := strings.Join(page.Commits[1].Tags, ","); got != "v1.0.0" {
		t.Fatalf("the tagged commit lost its tag: %q", got)
	}
}

// A message makes it an annotated tag, which is what a release is; without one
// it stays the lightweight name on a commit.
func TestTagWithAMessageIsAnnotated(t *testing.T) {
	work, _ := remotePair(t)

	if err := New(work).Tag(context.Background(), "v2.0.0", headOf(t, work), "the second one"); err != nil {
		t.Fatalf("tag: %v", err)
	}
	if got := gitOut(t, work, "cat-file", "-t", "v2.0.0"); got != "tag" {
		t.Fatalf("the tag object is a %s", got)
	}
	if got := gitOut(t, work, "tag", "-l", "--format=%(contents:subject)", "v2.0.0"); got != "the second one" {
		t.Fatalf("the message is %q", got)
	}

	if err := New(work).Tag(context.Background(), "v2.0.1", headOf(t, work), ""); err != nil {
		t.Fatalf("tag: %v", err)
	}
	if got := gitOut(t, work, "cat-file", "-t", "v2.0.1"); got != "commit" {
		t.Fatalf("a tag without a message must stay lightweight, it is a %s", got)
	}
}

// A name that is taken is git's refusal, and the tag that stands keeps
// pointing where it pointed: a tag that quietly moves is a release that means
// two different things to two people.
func TestTagRefusesToMoveAnExistingName(t *testing.T) {
	work, _ := remotePair(t)
	first := headOf(t, work)
	if err := New(work).Tag(context.Background(), "v1.0.0", first, ""); err != nil {
		t.Fatalf("tag: %v", err)
	}
	writeAt(t, work, "b.txt", "b\n")
	if _, err := New(work).Commit(context.Background(), "second", []string{"b.txt"}, false); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := New(work).Tag(context.Background(), "v1.0.0", headOf(t, work), ""); err == nil {
		t.Fatal("a name that is taken must be refused")
	}
	if got := gitOut(t, work, "rev-parse", "v1.0.0^{commit}"); got != first {
		t.Fatalf("the refused tag moved to %s", got)
	}
	if err := New(work).Tag(context.Background(), "", "", ""); err == nil {
		t.Fatal("an empty name must be refused")
	}
	if err := New(work).Tag(context.Background(), "--force", "", ""); err == nil {
		t.Fatal("a name that reads as an option must be refused")
	}
}

func TestPushTagSendsThatTagAlone(t *testing.T) {
	work, remote := remotePair(t)
	if err := New(work).Tag(context.Background(), "v1.0.0", headOf(t, work), ""); err != nil {
		t.Fatalf("tag: %v", err)
	}
	if err := New(work).Tag(context.Background(), "v1.0.1", headOf(t, work), ""); err != nil {
		t.Fatalf("tag: %v", err)
	}

	if err := New(work).PushTag(context.Background(), "v1.0.0"); err != nil {
		t.Fatalf("push tag: %v", err)
	}

	if got := gitOut(t, remote, "tag", "-l"); got != "v1.0.0" {
		t.Fatalf("the remote holds %q", got)
	}
}

// Deleting is two decisions, never one: the name goes here, and what the
// remote holds stays until that is asked for on its own.
func TestDeleteTagLeavesTheRemoteAlone(t *testing.T) {
	work, remote := remotePair(t)
	if err := New(work).Tag(context.Background(), "v1.0.0", headOf(t, work), ""); err != nil {
		t.Fatalf("tag: %v", err)
	}
	if err := New(work).PushTag(context.Background(), "v1.0.0"); err != nil {
		t.Fatalf("push tag: %v", err)
	}

	if err := New(work).DeleteTag(context.Background(), "v1.0.0"); err != nil {
		t.Fatalf("delete tag: %v", err)
	}
	if got := gitOut(t, work, "tag", "-l"); got != "" {
		t.Fatalf("the tag stands here: %q", got)
	}
	if got := gitOut(t, remote, "tag", "-l"); got != "v1.0.0" {
		t.Fatalf("the local deletion took the remote's tag: %q", got)
	}

	if err := New(work).DeleteRemoteTag(context.Background(), "v1.0.0"); err != nil {
		t.Fatalf("delete remote tag: %v", err)
	}
	if got := gitOut(t, remote, "tag", "-l"); got != "" {
		t.Fatalf("the remote still holds %q", got)
	}

	// A name nobody has is git's answer, and an empty one never reaches git.
	if err := New(work).DeleteTag(context.Background(), "v9.9.9"); err == nil {
		t.Fatal("a tag that does not exist must be refused")
	}
	if err := New(work).DeleteTag(context.Background(), ""); err == nil {
		t.Fatal("an empty name must be refused")
	}
}

// Where somebody's release goes is not a guess: several remotes without an
// origin among them end in a sentence instead of a destination, and nothing
// leaves this repository.
func TestPushTagWithoutOneRemoteToNameSaysSo(t *testing.T) {
	work, remote := remotePair(t)
	runGit(t, work, "remote", "rename", "origin", "first")
	runGit(t, work, "remote", "add", "second", t.TempDir())
	if err := New(work).Tag(context.Background(), "v1.0.0", headOf(t, work), ""); err != nil {
		t.Fatalf("tag: %v", err)
	}

	err := New(work).PushTag(context.Background(), "v1.0.0")

	if err == nil {
		t.Fatal("a tag push without one remote to name must be refused")
	}
	if got := gitOut(t, remote, "tag", "-l"); got != "" {
		t.Fatalf("the remote holds %q", got)
	}
}
