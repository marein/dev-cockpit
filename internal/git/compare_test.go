package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// compareRepo builds a history with a split in it: main gets a base commit,
// a branch grows an addition, a modification, a deletion and two renames on
// top of it, and main moves on with a change of its own after the split.
// It answers the branch name.
func compareRepo(t *testing.T, dir string) {
	t.Helper()
	commitRepo(t, dir)
	runGit(t, dir, "symbolic-ref", "HEAD", "refs/heads/main")
	writeAt(t, dir, "keep/modify.txt", "one\ntwo\nthree\n")
	writeAt(t, dir, "keep/delete.txt", "gone\n")
	writeAt(t, dir, "keep/exact.txt", "same\ncontent\n")
	writeAt(t, dir, "keep/edited.txt", "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\n")
	writeAt(t, dir, "keep/main-only.txt", "base\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "base")
	runGit(t, dir, "tag", "v1")
	runGit(t, dir, "switch", "-q", "-c", "feature")
	writeAt(t, dir, "keep/modify.txt", "one\ntwo changed\nthree\n")
	writeAt(t, dir, "keep/added.txt", "new\n")
	runGit(t, dir, "rm", "-q", "keep/delete.txt", "keep/exact.txt", "keep/edited.txt")
	writeAt(t, dir, "moved/exact.txt", "same\ncontent\n")
	writeAt(t, dir, "moved/edited.txt", "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10 edited\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "feature work")
	runGit(t, dir, "switch", "-q", "main")
	writeAt(t, dir, "keep/main-only.txt", "base moved on\n")
	writeAt(t, dir, "keep/main-new.txt", "only main has this\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "main moves on")
}

func changesByPath(files []RevisionChange) map[string]RevisionChange {
	byPath := map[string]RevisionChange{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	return byPath
}

func TestCompareSinceTheSplitListsOnlyWhatTheBranchDid(t *testing.T) {
	dir := t.TempDir()
	compareRepo(t, dir)

	got, err := New(dir).Compare(context.Background(), CompareRequest{From: "main", To: "feature", Since: true})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if !got.Repo || got.From.Name != "main" || got.To.Name != "feature" {
		t.Fatalf("the sides are not named back: %+v", got)
	}
	if got.Base == got.From.SHA {
		t.Fatalf("since the split must measure from the merge base, not from main's tip")
	}
	if base := gitOut(t, dir, "merge-base", "main", "feature"); got.Base != base {
		t.Fatalf("base %s, git says %s", got.Base, base)
	}
	byPath := changesByPath(got.Files)
	if len(byPath) != 5 {
		t.Fatalf("five paths differ since the split, got %d: %+v", len(byPath), got.Files)
	}
	if f := byPath["keep/modify.txt"]; f.Status != "M" || f.Added != 1 || f.Removed != 1 {
		t.Fatalf("the modification: %+v", f)
	}
	if f := byPath["keep/added.txt"]; f.Status != "A" || f.Added != 1 {
		t.Fatalf("the addition: %+v", f)
	}
	if f := byPath["keep/delete.txt"]; f.Status != "D" || f.Removed != 1 {
		t.Fatalf("the deletion: %+v", f)
	}
	if f := byPath["moved/exact.txt"]; f.Status != "R" || f.From != "keep/exact.txt" || f.Added != 0 {
		t.Fatalf("the exact rename must be one entry with its source: %+v", f)
	}
	if f := byPath["moved/edited.txt"]; f.Status != "R" || f.From != "keep/edited.txt" || f.Added != 1 || f.Removed != 1 {
		t.Fatalf("the edited rename must be one entry with its source and its numbers: %+v", f)
	}
	if _, ok := byPath["keep/main-new.txt"]; ok {
		t.Fatalf("what main did after the split is not what the branch did")
	}
}

func TestCompareDirectListsEverythingThatDiffers(t *testing.T) {
	dir := t.TempDir()
	compareRepo(t, dir)

	got, err := New(dir).Compare(context.Background(), CompareRequest{From: "main", To: "feature"})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if got.Base != got.From.SHA {
		t.Fatalf("a direct comparison measures from the left side itself")
	}
	byPath := changesByPath(got.Files)
	if len(byPath) != 7 {
		t.Fatalf("seven paths differ between the tips, got %d: %+v", len(byPath), got.Files)
	}
	// Seen from main, the file only main has is gone on the branch, and the
	// file main moved on with is modified, which is git's own reading of the
	// two tips.
	if f := byPath["keep/main-new.txt"]; f.Status != "D" {
		t.Fatalf("main's own addition reads as a deletion on the branch: %+v", f)
	}
	if f := byPath["keep/main-only.txt"]; f.Status != "M" {
		t.Fatalf("main's own modification reads as modified: %+v", f)
	}
	want := strings.Fields(gitOut(t, dir, "diff", "--name-only", "main", "feature"))
	if len(want) != len(byPath) {
		t.Fatalf("git names %d paths, the comparison %d", len(want), len(byPath))
	}
	for _, path := range want {
		if _, ok := byPath[path]; !ok {
			t.Fatalf("git names %s, the comparison does not", path)
		}
	}
}

func TestCompareResolvesWhatGitResolvesAndNamesTheSideItRefuses(t *testing.T) {
	dir := t.TempDir()
	compareRepo(t, dir)
	repo := New(dir)

	got, err := repo.Compare(context.Background(), CompareRequest{From: "v1", To: "HEAD"})
	if err != nil {
		t.Fatalf("a tag and HEAD: %v", err)
	}
	if got.From.SHA != strings.TrimSpace(gitOut(t, dir, "rev-parse", "v1^{commit}")) {
		t.Fatalf("the tag resolves to its commit: %+v", got.From)
	}
	if len(got.Files) != 2 {
		t.Fatalf("main moved two files since v1, got %+v", got.Files)
	}
	got, err = repo.Compare(context.Background(), CompareRequest{From: "HEAD~1", To: "main", Since: true})
	if err != nil || len(got.Files) != 2 {
		t.Fatalf("HEAD~1 is a revision like any other: %v %+v", err, got.Files)
	}

	_, err = repo.Compare(context.Background(), CompareRequest{From: "nowhere", To: "main"})
	var refused *RevisionError
	if !errors.As(err, &refused) || refused.Side != "from" || refused.Rev != "nowhere" || !errors.Is(err, ErrRevision) {
		t.Fatalf("an unknown from must be refused by name: %v", err)
	}
	_, err = repo.Compare(context.Background(), CompareRequest{From: "main", To: "--output=/tmp/x"})
	if !errors.As(err, &refused) || refused.Side != "to" {
		t.Fatalf("an option shaped to must be refused as a revision: %v", err)
	}
}

func TestCompareTheSameCommitAnswersNothing(t *testing.T) {
	dir := t.TempDir()
	compareRepo(t, dir)

	got, err := New(dir).Compare(context.Background(), CompareRequest{From: "main", To: "main", Since: true})
	if err != nil || len(got.Files) != 0 || got.Base != got.To.SHA {
		t.Fatalf("main against main: %v %+v", err, got)
	}
	// Since the split with nothing on the right side since it: v1 is behind
	// main, so main brought nothing to v1.
	got, err = New(dir).Compare(context.Background(), CompareRequest{From: "main", To: "v1", Since: true})
	if err != nil || len(got.Files) != 0 {
		t.Fatalf("a tag behind its branch changed nothing since the split: %v %+v", err, got)
	}
}

func TestCompareRefusesRevisionsWithoutSharedHistory(t *testing.T) {
	dir := t.TempDir()
	compareRepo(t, dir)
	runGit(t, dir, "switch", "-q", "--orphan", "island")
	writeAt(t, dir, "island.txt", "alone\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "island")

	_, err := New(dir).Compare(context.Background(), CompareRequest{From: "main", To: "island", Since: true})
	if !errors.Is(err, ErrNoSplit) {
		t.Fatalf("no merge base must say so: %v", err)
	}
	got, err := New(dir).Compare(context.Background(), CompareRequest{From: "main", To: "island"})
	if err != nil || len(got.Files) == 0 {
		t.Fatalf("a direct comparison needs no shared history: %v %+v", err, got)
	}
}

func TestCompareStaysInsideASubdirectoryProject(t *testing.T) {
	dir := t.TempDir()
	compareRepo(t, dir)
	sub := dir + "/keep"

	got, err := New(sub).Compare(context.Background(), CompareRequest{From: "main", To: "feature", Since: true})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	byPath := changesByPath(got.Files)
	for path := range byPath {
		if strings.HasPrefix(path, "keep/") || strings.HasPrefix(path, "moved/") {
			t.Fatalf("a path is not relative to the project: %s", path)
		}
	}
	if f := byPath["modify.txt"]; f.Status != "M" || f.Added != 1 {
		t.Fatalf("the project's own file, by its own path: %+v", f)
	}
	if _, ok := byPath["exact.txt"]; !ok {
		t.Fatalf("a file moved out of the project is a deletion here: %+v", got.Files)
	}
}

func TestCompareWithoutARepositoryOrACommitAnswersNoRepo(t *testing.T) {
	dir := t.TempDir()
	got, err := New(dir).Compare(context.Background(), CompareRequest{From: "a", To: "b"})
	if err != nil || got.Repo {
		t.Fatalf("no repository: %v %+v", err, got)
	}
	commitRepo(t, dir)
	got, err = New(dir).Compare(context.Background(), CompareRequest{})
	if err != nil || got.Repo {
		t.Fatalf("no commit yet: %v %+v", err, got)
	}
}

func TestDefaultCompareMeasuresABranchAgainstMainAndMainAgainstItsLastTag(t *testing.T) {
	dir := t.TempDir()
	compareRepo(t, dir)
	repo := New(dir)

	runGit(t, dir, "switch", "-q", "feature")
	if got := repo.DefaultCompare(context.Background()); got.From != "main" || got.To != "feature" {
		t.Fatalf("on a branch the preset is main to the branch: %+v", got)
	}
	runGit(t, dir, "switch", "-q", "main")
	if got := repo.DefaultCompare(context.Background()); got.From != "v1" || got.To != "main" {
		t.Fatalf("on main the preset is the last tag to main: %+v", got)
	}
	// A tag at HEAD itself is not a place to measure from, the one before it is.
	runGit(t, dir, "tag", "v2")
	if got := repo.DefaultCompare(context.Background()); got.From != "v1" || got.To != "main" {
		t.Fatalf("a tag on HEAD is skipped for the one before: %+v", got)
	}
	// An empty request takes the preset, and the answer names it.
	got, err := repo.Compare(context.Background(), CompareRequest{Since: true})
	if err != nil || got.From.Name != "v1" || got.To.Name != "main" || len(got.Files) != 2 {
		t.Fatalf("the preset answers as a comparison: %v %+v", err, got)
	}
}

func TestDefaultCompareWithoutATagFallsBackToTheCommitBefore(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "one\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "first")
	repo := New(dir)
	head := strings.TrimSpace(gitOut(t, dir, "symbolic-ref", "--short", "HEAD"))
	if got := repo.DefaultCompare(context.Background()); got.From != head || got.To != head {
		t.Fatalf("one commit compares with itself: %+v", got)
	}
	writeAt(t, dir, "a.txt", "two\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "second")
	if got := repo.DefaultCompare(context.Background()); got.From != head+"~1" || got.To != head {
		t.Fatalf("without a tag the commit before HEAD: %+v", got)
	}
	runGit(t, dir, "switch", "-q", "--detach")
	if got := repo.DefaultCompare(context.Background()); got.From != "HEAD~1" || got.To != "HEAD" {
		t.Fatalf("detached is HEAD: %+v", got)
	}
}

// worktreeOnFeature stands on the branch of compareRepo with work on top that
// no commit holds: a modification, a staged addition, a file gone from the
// disk, and untracked files of every kind the listing has to cope with, an
// ignored one among them.
func worktreeOnFeature(t *testing.T, dir string) {
	t.Helper()
	compareRepo(t, dir)
	runGit(t, dir, "switch", "-q", "feature")
	writeAt(t, dir, "keep/modify.txt", "one\ntwo changed\nthree\nfour\n")
	writeAt(t, dir, "staged.txt", "staged\n")
	runGit(t, dir, "add", "staged.txt")
	if err := os.Remove(filepath.Join(dir, "keep/main-only.txt")); err != nil {
		t.Fatal(err)
	}
	writeAt(t, dir, "loose/new.txt", "a\nb\nc")
	writeAt(t, dir, "loose/blob.bin", "bin\x00ary\n")
	writeAt(t, dir, "keep/extra.txt", "x\ny\n")
	writeAt(t, dir, "loose/noise.log", "ignored\n")
	writeAt(t, dir, ".git/info/exclude", "*.log\n")
	if err := os.Symlink(filepath.Join(dir, "keep/modify.txt"), filepath.Join(dir, "loose/link")); err != nil {
		t.Fatal(err)
	}
}

func TestCompareWithTheWorkingCopyListsWhatNoCommitHolds(t *testing.T) {
	dir := t.TempDir()
	worktreeOnFeature(t, dir)

	got, err := New(dir).Compare(context.Background(), CompareRequest{From: "main", Since: true, Worktree: true})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if !got.Worktree || got.To.Name != "" || got.To.SHA != "" || got.From.Name != "main" {
		t.Fatalf("the working copy has no name and no commit: %+v", got)
	}
	if base := gitOut(t, dir, "merge-base", "main", "HEAD"); got.Base != base {
		t.Fatalf("since the split measures from the merge base with HEAD %s, got %s", base, got.Base)
	}
	byPath := changesByPath(got.Files)
	if f := byPath["keep/modify.txt"]; f.Status != "M" || f.Added != 2 || f.Removed != 1 {
		t.Fatalf("the modification counts against the base, not the branch tip: %+v", f)
	}
	if f := byPath["staged.txt"]; f.Status != "A" || f.Added != 1 {
		t.Fatalf("a staged addition is a change of the working copy: %+v", f)
	}
	if f := byPath["keep/main-only.txt"]; f.Status != "D" || f.Removed != 1 {
		t.Fatalf("a file gone from the disk is deleted: %+v", f)
	}
	if f := byPath["moved/edited.txt"]; f.Status != "R" || f.From != "keep/edited.txt" {
		t.Fatalf("the branch's committed rename stays one entry: %+v", f)
	}
	if f := byPath["loose/new.txt"]; f.Status != "?" || f.Added != 3 || f.Removed != 0 || f.Binary {
		t.Fatalf("an untracked file counts every line, the last one without its newline too: %+v", f)
	}
	if f := byPath["loose/blob.bin"]; f.Status != "?" || !f.Binary || f.Added != 0 {
		t.Fatalf("an untracked binary file counts nothing and says why: %+v", f)
	}
	if f, ok := byPath["loose/link"]; !ok || f.Added != 0 || f.Binary {
		t.Fatalf("an untracked symlink is listed and never followed: %+v", f)
	}
	if _, ok := byPath["loose/noise.log"]; ok {
		t.Fatalf("an ignored file is not a change")
	}
	tracked := strings.Fields(gitOut(t, dir, "diff", "--name-only", "-M", got.Base))
	untracked := strings.Fields(gitOut(t, dir, "ls-files", "--others", "--exclude-standard"))
	if len(got.Files) != len(tracked)+len(untracked) {
		t.Fatalf("git names %d tracked and %d untracked paths, the comparison %d: %+v", len(tracked), len(untracked), len(got.Files), got.Files)
	}
}

func TestCompareWithTheWorkingCopyDirectlyMeasuresFromTheLeftSide(t *testing.T) {
	dir := t.TempDir()
	worktreeOnFeature(t, dir)

	got, err := New(dir).Compare(context.Background(), CompareRequest{From: "main", Worktree: true})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if got.Base != got.From.SHA || got.Base != gitOut(t, dir, "rev-parse", "main") {
		t.Fatalf("a direct comparison measures from main itself: %+v", got)
	}
	byPath := changesByPath(got.Files)
	if f := byPath["keep/main-new.txt"]; f.Status != "D" {
		t.Fatalf("what only main has reads as gone from the working copy: %+v", f)
	}
	if f := byPath["loose/new.txt"]; f.Status != "?" || f.Added != 3 {
		t.Fatalf("the untracked file: %+v", f)
	}

	_, err = New(dir).Compare(context.Background(), CompareRequest{From: "nowhere", Worktree: true})
	var refused *RevisionError
	if !errors.As(err, &refused) || refused.Side != "from" {
		t.Fatalf("an unknown from is refused by name with the working copy too: %v", err)
	}
	got, err = New(dir).Compare(context.Background(), CompareRequest{To: "--output=/tmp/x", Worktree: true, Since: true})
	if err != nil || !got.Worktree || got.To.Name != "" {
		t.Fatalf("the working copy never reads To: %v %+v", err, got)
	}
}

func TestCompareWithTheWorkingCopyStaysInsideASubdirectoryProject(t *testing.T) {
	dir := t.TempDir()
	worktreeOnFeature(t, dir)

	got, err := New(filepath.Join(dir, "keep")).Compare(context.Background(), CompareRequest{From: "main", Since: true, Worktree: true})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	byPath := changesByPath(got.Files)
	if f := byPath["extra.txt"]; f.Status != "?" || f.Added != 2 {
		t.Fatalf("the project's own untracked file, by its own path and counted: %+v", f)
	}
	if f := byPath["modify.txt"]; f.Status != "M" {
		t.Fatalf("the project's own modification: %+v", f)
	}
	for path := range byPath {
		if strings.HasPrefix(path, "loose/") || strings.HasPrefix(path, "keep/") || path == "staged.txt" {
			t.Fatalf("a path outside the project or not relative to it: %s", path)
		}
	}
}

func TestSplitPointIsWhereHEADLeftTheRevision(t *testing.T) {
	dir := t.TempDir()
	compareRepo(t, dir)
	runGit(t, dir, "switch", "-q", "feature")
	repo := New(dir)

	base, at, err := repo.SplitPoint(context.Background(), "main")
	if err != nil {
		t.Fatalf("split from main: %v", err)
	}
	if at != gitOut(t, dir, "rev-parse", "main") || base != gitOut(t, dir, "merge-base", "main", "HEAD") || base == at {
		t.Fatalf("main moved on after the split, so the split is the merge base and not main's tip: base %s at %s", base, at)
	}
	base, at, err = repo.SplitPoint(context.Background(), "v1")
	if err != nil || base != at || at != gitOut(t, dir, "rev-parse", "v1^{commit}") {
		t.Fatalf("an ancestor of HEAD is its own split: %v %s %s", err, base, at)
	}
	for _, rev := range []string{"nowhere", "--output=/tmp/x", ""} {
		if _, _, err := repo.SplitPoint(context.Background(), rev); !errors.Is(err, ErrRevision) {
			t.Fatalf("%q must be refused as a revision: %v", rev, err)
		}
	}
	runGit(t, dir, "switch", "-q", "--orphan", "island")
	writeAt(t, dir, "island.txt", "alone\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "island")
	if _, _, err := repo.SplitPoint(context.Background(), "main"); !errors.Is(err, ErrNoSplit) {
		t.Fatalf("no shared history has no split: %v", err)
	}
}

func TestParseNameStatusReadsRenamesAndACutAnswer(t *testing.T) {
	files, cut := parseNameStatus([]byte("M\x00a.txt\x00R095\x00old.txt\x00new.txt\x00A\x00b.txt\x00"))
	if cut || len(files) != 3 {
		t.Fatalf("three entries, none cut: %v %+v", cut, files)
	}
	if files[1].Status != "R" || files[1].From != "old.txt" || files[1].Path != "new.txt" {
		t.Fatalf("the rename: %+v", files[1])
	}
	files, cut = parseNameStatus([]byte("M\x00a.txt\x00R095\x00old.txt\x00ne"))
	if !cut || len(files) != 1 || files[0].Path != "a.txt" {
		t.Fatalf("a cut answer keeps the whole entries and says so: %v %+v", cut, files)
	}
	if files, cut := parseNameStatus(nil); cut || len(files) != 0 {
		t.Fatalf("nothing differs: %v %+v", cut, files)
	}
}
