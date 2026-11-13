package render

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
)

// AssistantProjectsData is the Projects tab of an assistant's aside: one card
// per project a job of this assistant names. Access and CSRFToken are the
// page's, a stopped coder's row resumes it for the owner alone.
type AssistantProjectsData struct {
	Projects []AssistantProjectCard
	// Needs is how many of the projects need the user, the tab's count.
	Needs int
	// Older is how many cards without an open job and nothing that needs the
	// user stand past the newest ones, Unfolded says the tab shows them.
	// Folded are their projects while it does not. The tab pulls on their
	// forge signal, a failed pipeline brings such a card up.
	Older     int
	Unfolded  bool
	Folded    []string
	Access    Access
	CSRFToken string
}

// OlderLabel is what the fold row of the older cards says.
func (d AssistantProjectsData) OlderLabel() FoldLabel {
	return FoldLabel{Shut: "Show " + plural(d.Older, "older project"), Open: "Show fewer projects"}
}

// FoldLabel is what a control over the rows past the first ones of a list
// says, Shut while they are folded, Open while they stand.
type FoldLabel struct {
	Shut string
	Open string
}

// AssistantProjectCard is one project with the local git state of its
// working copy, what the forge poller holds of its branch and the
// assistant's jobs in it.
type AssistantProjectCard struct {
	// Owner is the assistant whose aside shows the card.
	Owner string
	Name  string
	// URL leads to the project, empty for a guest whose link does not grant it.
	URL  string
	Open bool
	// Needs are the reasons the project needs the user, none when it does not.
	Needs []string
	Git   AssistantProjectGit
	// Forge is nil for a project the forge poller holds nothing to show of.
	Forge *AssistantProjectForge
	Jobs  []AssistantJobView
	// Older is how many closed jobs fold under the newest ones.
	Older int
	// Threads counts the open line comment threads, the forge's and the local
	// ones, Resolved the resolved ones, Unpublished the line comments a
	// publish would post on the forge.
	Threads     int
	Resolved    int
	Unpublished int
}

// ReviewLine counts the open threads and the unpublished comments. A card
// without a changes line has no other way into its project, so where nothing
// is open it counts the resolved threads.
func (c AssistantProjectCard) ReviewLine() string {
	var parts []string
	if c.Threads > 0 {
		parts = append(parts, plural(c.Threads, "open thread"))
	}
	if c.Unpublished > 0 {
		parts = append(parts, plural(c.Unpublished, "unpublished comment"))
	}
	if len(parts) == 0 && c.Resolved > 0 && !c.Git.Read {
		parts = append(parts, plural(c.Resolved, "resolved thread"))
	}
	return strings.Join(parts, " · ")
}

// NeedsWhy names every reason the card needs the user.
func (c AssistantProjectCard) NeedsWhy() string {
	return strings.Join(c.Needs, " ")
}

// AssistantProjectGit is one status read of the project's working copy and
// what its branch brought since its base. Repo false is a project without git,
// Read false a status git did not answer.
type AssistantProjectGit struct {
	Repo     bool
	Read     bool
	Branch   string
	Detached bool
	Upstream string
	Counted  bool
	Ahead    int
	Behind   int
	Changed  int
	Since    AssistantProjectSince
}

// AssistantProjectSince is the pair the editor's compare opens on, the place
// git.DefaultCompare measures HEAD against with the working copy, which the
// card's changes line and the Changes section count: the commits HEAD has on
// top of the split, and the files and lines of the comparison. Read false is
// a comparison that did not answer, Error says why. Split is the short hash
// of the merge base where it is not From's own commit.
type AssistantProjectSince struct {
	Read    bool
	Error   string
	From    string
	Split   string
	Commits int
	Changed int
	Added   int
	Removed int
}

// To is the editor's name of the right side of the pair.
func (s AssistantProjectSince) To() string {
	return "Working copy"
}

// Base names the pair the counts are of.
func (s AssistantProjectSince) Base() string {
	if s.Split != "" {
		return s.To() + " since the split from " + s.From + " at " + s.Split
	}
	return s.To() + " since " + s.From
}

// CommitsLine counts the commits since the base, FilesLine the files.
func (s AssistantProjectSince) CommitsLine() string {
	return plural(s.Commits, "commit")
}

func (s AssistantProjectSince) FilesLine() string {
	return plural(s.Changed, "file")
}

// ShownJobs are the job rows a card shows open, OlderJobs those under its fold.
func (c AssistantProjectCard) ShownJobs() []AssistantJobView {
	return c.Jobs[:len(c.Jobs)-c.Older]
}

func (c AssistantProjectCard) OlderJobs() []AssistantJobView {
	return c.Jobs[len(c.Jobs)-c.Older:]
}

// OlderLabel is what the fold row of the card's older jobs says.
func (c AssistantProjectCard) OlderLabel() FoldLabel {
	return FoldLabel{Shut: "Show " + plural(c.Older, "older coder"), Open: "Show fewer coders"}
}

// FoldID names the fold of the card's older jobs.
func (c AssistantProjectCard) FoldID() string {
	return "assistant-project-" + asideKey(c.Owner, c.Name) + "-older"
}

// asideKey stands for a project, a list or a file in the ids of one
// assistant's aside. A name may hold what an id selector cannot, so it is
// hashed, and the assistant goes in with it: several assistants stand side by
// side as editor tabs, each with its aside, and Bootstrap finds a fold or a
// tab by its id in the whole document.
func asideKey(owner, name string) string {
	sum := sha256.Sum256([]byte(owner + "\x00" + name))
	return hex.EncodeToString(sum[:8])
}

// AssistantProjectForge is what the forge poller read of the branch a card's
// project has checked out, the project row's badge with what the card adds:
// NoChange says the branch has no change request, empty for the default
// branch, and the run of the failed jobs again with the route it posts to,
// nil where the forge refuses it or for a guest.
type AssistantProjectForge struct {
	ForgeBadge
	NoChange string
	Retry    *ForgeRetry
	RetryURL string
}

// NewAssistantProjectForge is the forge part of the card of project, nil
// for a branch never pushed.
func NewAssistantProjectForge(project string, b forge.Binding, p forge.Polled) *AssistantProjectForge {
	if p.Watch.Head == "" {
		return nil
	}
	badge := NewForgeBadge(b, p)
	if badge == nil {
		badge = &ForgeBadge{Kind: b.Host.Kind, Branch: p.Watch.Branch, Head: p.Watch.Head}
	}
	f := &AssistantProjectForge{ForgeBadge: *badge}
	if f.Change == nil && p.Err == nil && p.Status.Default != p.Watch.Remote {
		noun := b.Host.Kind.ChangeNoun
		if noun == "" {
			noun = "change request"
		}
		f.NoChange = "No " + noun
	}
	if r := f.ForgeBadge.Retry(); r != nil && r.Blocked == "" {
		f.Retry = r
		f.RetryURL = "/projects/" + url.PathEscape(project) + "/forge/retry"
	}
	return f
}

// Failed reports whether the pipeline of the branch failed.
func (f AssistantProjectForge) Failed() bool {
	return f.Pipeline != nil && f.Pipeline.Status == forge.Failed
}

// ChangeColor is the colour of where the change request stands.
func (f AssistantProjectForge) ChangeColor() string {
	switch {
	case f.Change.State == forge.Merged:
		return "purple"
	case f.Change.State == forge.Open && !f.Change.Draft:
		return "green"
	}
	return "secondary"
}

// ForgeStage is one stage of a pipeline, its jobs rolled up into one status.
// A job outside every stage stands as a stage of its own.
type ForgeStage struct {
	Name   string
	Status forge.Status
}

// Stages are the pipeline's stages in the order their first job comes.
func (f AssistantProjectForge) Stages() []ForgeStage {
	if f.Pipeline == nil {
		return nil
	}
	var stages []ForgeStage
	jobs := map[int][]forge.Job{}
	at := map[string]int{}
	for _, j := range f.Pipeline.Jobs {
		i, ok := at[j.Stage]
		if !ok || j.Stage == "" {
			i = len(stages)
			name := j.Stage
			if name == "" {
				name = j.Name
			}
			stages = append(stages, ForgeStage{Name: name})
			if j.Stage != "" {
				at[j.Stage] = i
			}
		}
		jobs[i] = append(jobs[i], j)
	}
	for i := range stages {
		stages[i].Status = forge.Rollup(jobs[i])
	}
	return stages
}

// Bar is the stage's look on the strip, a running one moving.
func (s ForgeStage) Bar() string {
	bar := "bg-" + statusColor(s.Status)
	if s.Status == forge.Running {
		bar += " progress-bar-striped progress-bar-animated"
	}
	return bar
}

// StagesLabel names every stage with its status.
func (f AssistantProjectForge) StagesLabel() string {
	var parts []string
	for _, s := range f.Stages() {
		parts = append(parts, s.Name+" "+string(s.Status))
	}
	return "Stages: " + strings.Join(parts, ", ")
}

// PipelineNote is what the pipeline line says after its stages: the first
// job that failed it and how many more did, the first job that runs, else
// the pipeline's status.
func (f AssistantProjectForge) PipelineNote() string {
	pl := f.Pipeline
	if pl.Status == forge.Failed {
		if failing := pl.Failing(); len(failing) > 0 {
			if len(failing) > 1 {
				return fmt.Sprintf("%s +%d", failing[0].Name, len(failing)-1)
			}
			return failing[0].Name
		}
	}
	if pl.Status == forge.Running {
		for _, j := range pl.Jobs {
			if j.Status == forge.Running {
				return j.Name
			}
		}
	}
	return string(pl.Status)
}

// AssistantProjectView is one card opened inside the aside: its head, its
// line comment threads and, where git reads the project, its changes, each a
// section of its own. Section is the one that shows. Back leads to the cards
// again.
type AssistantProjectView struct {
	Card    AssistantProjectCard
	Reviews AssistantProjectReviews
	Changes *AssistantProjectChanges
	Section string
}

// The sections of a project opened in the aside.
const (
	SectionReviews = "reviews"
	SectionChanges = "changes"
)

// Shows answers the section that shows, Changes where it is asked for and
// stands, else Reviews.
func (v AssistantProjectView) Shows() string {
	if v.Changes != nil && v.Section == SectionChanges {
		return SectionChanges
	}
	return SectionReviews
}

// PaneID starts the ids of the two sections and their switch.
func (v AssistantProjectView) PaneID() string {
	return "assistant-project-" + asideKey(v.Card.Owner, v.Card.Name)
}

// Segmented says Changes stands beside Reviews, so a switch picks between
// them.
func (v AssistantProjectView) Segmented() bool {
	return v.Changes != nil
}

// AssistantProjectChanges is the Changes section of a project opened in the
// aside: on top what the working copy holds uncommitted, nil where it holds
// nothing, UncommittedUnread where git did not answer that, then the pair it
// compares and its counts, the files of that comparison with the largest
// change first and the commits since the split newest first, each list cut to
// its first rows. CompareURL opens the editor's compare on the pair, empty for
// a guest whose link does not grant the project.
type AssistantProjectChanges struct {
	Owner             string
	Project           string
	Since             AssistantProjectSince
	Uncommitted       *AssistantFileList
	UncommittedUnread bool
	Files             AssistantFileList
	Commits           AssistantRows[AssistantCommit]
	LogRead           bool
	CompareURL        string
}

// The lists of the Changes section whose rest a fold unfolds, a value of the
// view's more parameter each. A file's row names the first two, its diff
// reads the comparison of that list.
const (
	ChangesUncommitted = "uncommitted"
	ChangesFiles       = "files"
	ChangesCommits     = "commits"
)

// CommitsFold names the fold of the commits past the first ones.
func (c AssistantProjectChanges) CommitsFold() string {
	return changesFold(c.Owner, c.Project, ChangesCommits)
}

// changesFold names the fold of a list's rows past the first ones.
func changesFold(owner, project, list string) string {
	return "assistant-project-" + asideKey(owner, project) + "-" + list
}

// OlderCommitsLabel is what the fold of the commits past the first ones
// says.
func (c AssistantProjectChanges) OlderCommitsLabel() FoldLabel {
	return FoldLabel{Shut: "Show " + plural(c.Commits.More, "older commit"), Open: "Show fewer commits"}
}

// AssistantFileList is a list of changed files of the Changes section, List
// its name, cut to its first rows. URL opens the editor on the whole list
// where git cut it, empty for a guest whose link does not grant the project.
type AssistantFileList struct {
	Owner   string
	Project string
	List    string
	AssistantRows[AssistantChangedFile]
	URL string
}

// Fold names the fold of the rows past the first ones.
func (l AssistantFileList) Fold() string {
	return changesFold(l.Owner, l.Project, l.List)
}

// Count is how many files the list holds, MoreLabel is what the fold of
// those past the first ones says.
func (l AssistantFileList) Count() int {
	return len(l.Shown) + l.More
}

func (l AssistantFileList) MoreLabel() FoldLabel {
	return FoldLabel{Shut: "Show " + plural(l.More, "more file"), Open: "Show fewer files"}
}

// AssistantRows is a list of the Changes section cut to its first rows. More
// counts the rows past them, Rest holds those once the reader unfolded them,
// they are rendered only then. Truncated says git's output cap cut the list
// itself.
type AssistantRows[T any] struct {
	Shown     []T
	Rest      []T
	More      int
	Unfolded  bool
	Truncated bool
}

// NewAssistantRows cuts rows, the head of a list of total, to the first shown,
// the rest kept where unfolded.
func NewAssistantRows[T any](rows []T, total, shown int, unfolded, truncated bool) AssistantRows[T] {
	l := AssistantRows[T]{Shown: rows, Unfolded: unfolded, Truncated: truncated}
	if len(rows) > shown {
		l.Shown = rows[:shown]
		if unfolded {
			l.Rest = rows[shown:]
		}
	}
	l.More = max(total-len(l.Shown), 0)
	return l
}

// NewAssistantFileList is the list of project named list, files cut to the
// first shown, each row's bar scaled to the largest change among them all.
func NewAssistantFileList(owner, project, list string, files []AssistantChangedFile, shown int, unfolded, truncated bool, url string) AssistantFileList {
	largest := 0
	for _, f := range files {
		largest = max(largest, f.Added+f.Removed)
	}
	if largest > 0 {
		for i := range files {
			files[i].AddedBar = files[i].Added * 100 / largest
			files[i].RemovedBar = files[i].Removed * 100 / largest
		}
	}
	return AssistantFileList{Owner: owner, Project: project, List: list, AssistantRows: NewAssistantRows(files, len(files), shown, unfolded, truncated), URL: url}
}

// The lines a file shows in place of a diff it does not draw.
const (
	DiffNoteBinary    = "A binary file, no diff."
	DiffNoteDeleted   = "A deleted file, no diff."
	DiffNoteIrregular = "Not a regular file, no diff."
)

// DiffNoteLines and DiffNoteBytes are the lines of a file whose larger side
// holds more lines or KiB than the editor's diff limit, which they name.
func DiffNoteLines(limit int) string {
	return fmt.Sprintf("More than %d lines, not shown here.", limit)
}

func DiffNoteBytes(kib int) string {
	return fmt.Sprintf("Larger than %d KiB, not shown here.", kib)
}

// AssistantChangedFile is one changed path of a list of Changes, List its
// name: git's status letter, "?" for a file git does not track, the path a
// rename came from, the lines that came and went and the share of the largest
// change in the list they take. URL opens the editor on the file as its list
// compares it, empty for a guest whose link does not grant the project.
type AssistantChangedFile struct {
	Owner      string
	List       string
	Path       string
	From       string
	Status     string
	Added      int
	Removed    int
	Binary     bool
	URL        string
	AddedBar   int
	RemovedBar int
}

// The editor's marks of a changed path (GIT_MARKS in @dc/editor-marks), keyed
// the way it reads a comparison's letter (revdiffKind).
var changedMarks = map[string]struct{ mark, class, label string }{
	"added":     {"A", "text-green", "Added"},
	"deleted":   {"D", "text-red", "Deleted"},
	"modified":  {"M", "text-yellow", "Modified"},
	"renamed":   {"R", "text-azure", "Renamed"},
	"untracked": {"U", "text-cyan", "Untracked"},
}

func (f AssistantChangedFile) kind() string {
	switch f.Status {
	case "A", "C":
		return "added"
	case "D":
		return "deleted"
	case "R":
		return "renamed"
	case "?":
		return "untracked"
	}
	return "modified"
}

// Mark is the letter the editor marks the file with, MarkClass its colour and
// MarkLabel what it says.
func (f AssistantChangedFile) Mark() string      { return changedMarks[f.kind()].mark }
func (f AssistantChangedFile) MarkClass() string { return changedMarks[f.kind()].class }
func (f AssistantChangedFile) MarkLabel() string { return changedMarks[f.kind()].label }

// Name is the file's own name, Dir the folder it stands in, empty at the root.
func (f AssistantChangedFile) Name() string {
	return f.Path[strings.LastIndex(f.Path, "/")+1:]
}

func (f AssistantChangedFile) Dir() string {
	if i := strings.LastIndex(f.Path, "/"); i >= 0 {
		return f.Path[:i]
	}
	return ""
}

// Renamed says the file came from another path.
func (f AssistantChangedFile) Renamed() bool {
	return f.From != "" && f.From != f.Path
}

// Fold names the fold of the file's diff, the same over a refresh of the
// list and apart from the same file's in the other list.
func (f AssistantChangedFile) Fold() string {
	return "assistant-diff-" + f.List + "-" + asideKey(f.Owner, f.Path)
}

// DiffNote is the line the file shows in place of its diff where its row
// already tells, empty where the diff route reads the file.
func (f AssistantChangedFile) DiffNote() string {
	switch {
	case f.Binary:
		return DiffNoteBinary
	case f.kind() == "deleted":
		return DiffNoteDeleted
	}
	return ""
}

// AssistantCommit is one commit since the base: its short hash, subject,
// author and when it was made.
type AssistantCommit struct {
	Short   string
	Subject string
	Author  string
	At      time.Time
}

// When is the commit's time for the time element, empty where none is known.
func (c AssistantCommit) When() string {
	if c.At.Unix() <= 0 {
		return ""
	}
	return c.At.UTC().Format(time.RFC3339)
}

// AssistantProjectReviews are the line comment threads of a project, the
// project comments first, the threads of the change request of the branch the
// forge poller read and the local ones alike, open first, the resolved ones
// folded, and on a forge the comments a publish would post, UnpublishedGeneral
// of them project comments. Branch is that branch, which a reply and a
// resolve on the forge name. Publish and Threads are what the forge takes
// there, nil for none or for a guest, who changes nothing. CommentsURL is
// where the editor's line comments take a local thread's writes and a new
// project comment, empty for a guest.
type AssistantProjectReviews struct {
	Owner              string
	Project            string
	Branch             string
	Open               []LineThread
	Resolved           []LineThread
	Unpublished        []string
	UnpublishedGeneral int
	Publish            *ForgePublishOffer
	PublishURL         string
	Threads            *ForgeThreadsOffer
	ThreadURL          string
	CommentsURL        string
}

// Count is the number of open threads, the section's count.
func (r AssistantProjectReviews) Count() int {
	return len(r.Open)
}

// ResolvedFold names the fold of the resolved threads.
func (r AssistantProjectReviews) ResolvedFold() string {
	return "assistant-project-" + asideKey(r.Owner, r.Project) + "-resolved"
}

// UnpublishedLine counts the line comments and the project comments a
// publish would post.
func (r AssistantProjectReviews) UnpublishedLine() string {
	var parts []string
	if lines := len(r.Unpublished) - r.UnpublishedGeneral; lines > 0 {
		parts = append(parts, plural(lines, "line comment"))
	}
	if r.UnpublishedGeneral > 0 {
		parts = append(parts, plural(r.UnpublishedGeneral, "project comment"))
	}
	return strings.Join(parts, " and ") + " not published"
}

// PublishLabel names the publish: a review while only line comments go, which
// become its review threads, else a publish, which posts a project comment as
// a general comment.
func (r AssistantProjectReviews) PublishLabel() string {
	if r.UnpublishedGeneral > 0 {
		return "Publish"
	}
	return "Publish as review"
}

// ResolvedLabel is what the fold of the resolved threads says.
func (r AssistantProjectReviews) ResolvedLabel() FoldLabel {
	return FoldLabel{Shut: "Show " + plural(len(r.Resolved), "resolved thread"), Open: "Hide resolved threads"}
}
