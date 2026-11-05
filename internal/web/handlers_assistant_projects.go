package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/git"
	"github.com/marein/dev-cockpit/internal/linecomment"
	"github.com/marein/dev-cockpit/internal/plaintext"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// assistantProjectsPath serves the Projects tab of one assistant's aside, the
// watch path keeps the git of its projects polled while the tab is on screen.
const (
	assistantProjectsPath      = "/assistants/projects"
	assistantProjectsWatchPath = "/assistants/projects/watch"
	assistantProjectViewPath   = "/assistants/projects/view"
	assistantProjectDiffPath   = "/assistants/projects/diff"
)

// closedProjectsShown bounds the quiet cards, without an open job and
// nothing that needs the user, like closedJobsShown bounds the closed jobs,
// and closedCardJobsShown the closed rows on one card: an assistant's jobs
// pile up in the projects it keeps coming back to.
const (
	closedProjectsShown = 5
	closedCardJobsShown = 3
)

// projectGitReads bounds the status reads one render runs at once.
const projectGitReads = 4

// handleAssistantProjects serves the Projects tab on its own, which the tab
// pulls when a job, the project set or a working copy moved. It is read on the
// page, an assistant's own command has no use for it.
func (s *Server) handleAssistantProjects(c *gin.Context) {
	id, ok := s.assistantProjectsOwner(c)
	if !ok {
		return
	}
	data := s.assistantProjectsData(c.Request.Context(), id, listsAll(c))
	data.CSRFToken = s.csrfToken(c)
	c.HTML(http.StatusOK, "assistant_projects_list.gohtml", data)
}

// handleAssistantProjectsWatch renews the git watch of the projects the tab
// shows. It writes nothing, like the editor's git watch it keeps a poller
// running for a short window.
func (s *Server) handleAssistantProjectsWatch(c *gin.Context) {
	id, ok := s.assistantProjectsOwner(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, s.watchAssistantProjects(id, listsAll(c)))
}

func (s *Server) assistantProjectsOwner(c *gin.Context) (string, bool) {
	if s.localCall(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "The projects of an assistant are read on its page."})
		return "", false
	}
	id := strings.TrimSpace(c.Query("assistant"))
	if _, err := s.assistants.Get(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "There is no such assistant."})
		return "", false
	}
	return id, true
}

// assistantProjectsURLs are the list, the watch, the project view and the
// file diff path of one assistant's tab, below base for a guest. The view
// takes the project, the diff the project, the list and the path.
func assistantProjectsURLs(base, owner string) (list, watch, view, diff string) {
	query := "?assistant=" + url.QueryEscape(owner)
	return base + assistantProjectsPath + query, base + assistantProjectsWatchPath + query, base + assistantProjectViewPath + query, base + assistantProjectDiffPath + query
}

// handleAssistantProjectView serves one card opened in the aside with its
// line comment threads and its changes, which the tab pulls when the card
// opens and when something on it moved. A project no job of the assistant
// names answers 404, the tab goes back to its cards.
func (s *Server) handleAssistantProjectView(c *gin.Context) {
	id, ok := s.assistantProjectsOwner(c)
	if !ok {
		return
	}
	view, ok := s.assistantProjectView(c, id, nil)
	if !ok {
		c.String(http.StatusNotFound, "The assistant works in no such project.")
		return
	}
	c.HTML(http.StatusOK, "assistant_project_view.gohtml", view)
}

// handleShareAssistantProjectView is the project view a guest's page pulls,
// the owner's read only, a project no job of the link's assistant names the
// plain 404.
func (s *Server) handleShareAssistantProjectView(c *gin.Context) {
	g, _ := guestOf(c)
	view, ok := s.assistantProjectView(c, g.assistant.ID, g)
	if !ok {
		s.handleNotFound(c)
		return
	}
	c.HTML(http.StatusOK, "assistant_project_view.gohtml", view)
}

// handleAssistantProjectDiff answers a changed file of the project view, the
// two sides its row draws as a diff when it unfolds, of the list the query
// names. A path no row of that list shows answers 404, the row goes with the
// next pull of the view.
func (s *Server) handleAssistantProjectDiff(c *gin.Context) {
	id, ok := s.assistantProjectsOwner(c)
	if !ok {
		return
	}
	diff, status, err := s.projectFileDiff(c.Request.Context(), id, c.Query("project"), c.Query("list"), c.Query("path"))
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, diff)
}

// handleShareAssistantProjectDiff is the diff a guest's row unfolds, the
// owner's, a file no row of the link's assistant shows the plain 404.
func (s *Server) handleShareAssistantProjectDiff(c *gin.Context) {
	g, _ := guestOf(c)
	diff, status, err := s.projectFileDiff(c.Request.Context(), g.assistant.ID, c.Query("project"), c.Query("list"), c.Query("path"))
	switch {
	case status == http.StatusNotFound:
		s.handleNotFound(c)
	case err != nil:
		c.JSON(status, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusOK, diff)
	}
}

// projectDiff is what a changed file's row draws: the file as the left side
// of its list has it and as it stands on the disk, or Note, the line it shows
// instead.
type projectDiff struct {
	Original string `json:"original"`
	Content  string `json:"content"`
	Note     string `json:"note,omitempty"`
}

var errDiffUnlisted = errors.New("The file is not in this list.")

// projectFileDiff reads the file at path of the project name as its row of
// the list of Changes shows it, the left side against the disk. Only a
// project whose files the view lists answers, see projectChangeList. A file
// the row shows a note for answers that note, and so does one that turns out
// no regular file, binary or over a diff limit of the editor, see
// overDiffLimits. The status goes with the error.
func (s *Server) projectFileDiff(ctx context.Context, owner, name, list, path string) (projectDiff, int, error) {
	if !s.assistantsWorkIn([]string{owner}, name) {
		return projectDiff{}, http.StatusNotFound, errors.New("The assistant works in no such project.")
	}
	p, err := s.projects.FindByName(name)
	if err != nil {
		return projectDiff{}, http.StatusNotFound, errors.New("The assistant works in no such project.")
	}
	unread := errors.New("git did not answer.")
	repo := git.New(p.Path)
	read, listed, err := projectChangeList(ctx, p, list)
	if err != nil {
		return projectDiff{}, http.StatusBadGateway, unread
	}
	if !listed {
		return projectDiff{}, http.StatusNotFound, errDiffUnlisted
	}
	rows := changedFiles(p.Name, list, read.Files)
	at := slices.IndexFunc(rows, func(f render.AssistantChangedFile) bool { return f.Path == path })
	if at < 0 {
		return projectDiff{}, http.StatusNotFound, errDiffUnlisted
	}
	f := rows[at]
	if note := f.DiffNote(); note != "" {
		return projectDiff{Note: note}, http.StatusOK, nil
	}
	if irregularFile(p.Path, f.Path) {
		return projectDiff{Note: render.DiffNoteIrregular}, http.StatusOK, nil
	}
	original := f.Path
	if f.From != "" {
		original = f.From
	}
	before, _, err := repo.FileAt(ctx, read.Base, original)
	if err != nil {
		return projectDiff{}, http.StatusBadGateway, unread
	}
	_, info, err := filesystem.ResolveExistingFile(p.Path, f.Path)
	if err != nil {
		return projectDiff{}, http.StatusNotFound, errDiffUnlisted
	}
	set := s.editorSettings()
	if note := overDiffLimits(set, int(min(info.Size(), filesystem.MaxEditableBytes+1)), 0); note != "" {
		return projectDiff{Note: note}, http.StatusOK, nil
	}
	text, _, err := filesystem.ReadFileText(p.Path, f.Path)
	switch {
	case errors.Is(err, filesystem.ErrBinary):
		return projectDiff{Note: render.DiffNoteBinary}, http.StatusOK, nil
	case err != nil:
		return projectDiff{}, http.StatusNotFound, errDiffUnlisted
	}
	for _, side := range [][]byte{before, []byte(text)} {
		if note := overDiffLimits(set, len(side), textLines(side)); note != "" {
			return projectDiff{Note: note}, http.StatusOK, nil
		}
		if filesystem.CheckEditableText(side) != nil {
			return projectDiff{Note: render.DiffNoteBinary}, http.StatusOK, nil
		}
	}
	return projectDiff{Original: string(before), Content: text}, http.StatusOK, nil
}

// overDiffLimits is the line a side of size bytes and lines shows in place of
// its diff where it holds more than the editor's diff limits, Settings ›
// Editor › Git, a limit of zero off, the way the editor asks before it diffs.
// The aside does not ask, and a file larger than the editor reads at all
// stands over a limit of that size.
func overDiffLimits(set editorSettings, size, lines int) string {
	switch {
	case set.DiffMaxKiB > 0 && size > set.DiffMaxKiB<<10:
		return render.DiffNoteBytes(set.DiffMaxKiB)
	case size > filesystem.MaxEditableBytes:
		return render.DiffNoteBytes(filesystem.MaxEditableBytes >> 10)
	case set.DiffMaxLines > 0 && lines > set.DiffMaxLines:
		return render.DiffNoteLines(set.DiffMaxLines)
	}
	return ""
}

// textLines counts the lines of a text the way the editor's countLines
// does: none in an empty one, one more than its line feeds otherwise.
func textLines(text []byte) int {
	if len(text) == 0 {
		return 0
	}
	return bytes.Count(text, []byte{'\n'}) + 1
}

// irregularFile says the working copy holds rel as something other than a
// regular file, the folder of a submodule or a symlink, whose row draws no
// diff: git compares a submodule's commit and a link's target, not a file's
// lines. The folder resolves under root, the entry is looked at unfollowed.
func irregularFile(root, rel string) bool {
	dir, err := filesystem.ResolveUnder(root, path.Dir(rel))
	if err != nil {
		return false
	}
	info, err := os.Lstat(filepath.Join(dir, path.Base(rel)))
	return err == nil && !info.Mode().IsRegular()
}

// projectChangeList reads the comparison a list of the Changes section of p
// shows, the files of the pair or the uncommitted ones, false for any other
// list and where the view lists no file: a project without a repository of
// its own, one below the root of another repository included, a repository
// without a commit, and a pair that has no comparison.
func projectChangeList(ctx context.Context, p project.Project, list string) (git.Comparison, bool, error) {
	if !p.GitRepo {
		return git.Comparison{}, false, nil
	}
	switch list {
	case render.ChangesUncommitted:
		read, err := uncommittedFiles(ctx, git.New(p.Path))
		return read, read.Repo, err
	case render.ChangesFiles:
	default:
		return git.Comparison{}, false, nil
	}
	read, err := git.New(p.Path).Since(ctx)
	switch {
	case errors.Is(err, git.ErrNoSplit), errors.Is(err, git.ErrRevision):
		return git.Comparison{}, false, nil
	case err != nil:
		return git.Comparison{}, false, err
	}
	return git.Comparison{Repo: read.Repo, Base: read.Base, Files: read.Files, Truncated: read.Truncated}, read.Repo, nil
}

// uncommittedFiles is what the working copy holds on top of HEAD, the
// editor's compare of HEAD with the working copy, under the default timeout
// as a whole like the pair's read.
func uncommittedFiles(ctx context.Context, repo *git.Repo) (git.Comparison, error) {
	bounded, cancel := context.WithTimeout(ctx, git.DefaultTimeout)
	defer cancel()
	return repo.Compare(bounded, git.CompareRequest{From: "HEAD", Worktree: true})
}

// assistantProjectView is the card of the project the query names with its
// threads and changes, false where no job of owner names it. A guest's card
// leads where its link does, and its threads offer nothing to change.
func (s *Server) assistantProjectView(c *gin.Context, owner string, g *guest) (render.AssistantProjectView, bool) {
	name := c.Query("project")
	data, projects := s.assistantProjectCards(owner, true)
	at := slices.IndexFunc(data.Projects, func(card render.AssistantProjectCard) bool { return card.Name == name })
	if at < 0 {
		return render.AssistantProjectView{}, false
	}
	cards, p := data.Projects[at:at+1], projects[at]
	ctx := c.Request.Context()
	reads := s.readProjectGit(ctx, cards, []project.Project{p})
	s.countProjectReviews(cards)
	more := c.QueryArray("more")
	view := render.AssistantProjectView{
		Card:    cards[0],
		Reviews: s.projectReviews(c, p, cards[0], g == nil),
		Changes: s.projectChanges(ctx, p, cards[0], reads[0], more),
		Section: c.Query("section"),
	}
	if g != nil {
		lead := s.guestLead(g)
		guestCard(&view.Card, lead)
		for _, list := range [][]render.AssistantThread{view.Reviews.Open, view.Reviews.Resolved} {
			for i := range list {
				list[i].FileURL = lead(list[i].FileURL)
			}
		}
		if view.Changes != nil {
			view.Changes.CompareURL = lead(view.Changes.CompareURL)
			for _, files := range []*render.AssistantFileList{view.Changes.Uncommitted, &view.Changes.Files} {
				if files == nil {
					continue
				}
				files.URL = lead(files.URL)
				for _, list := range [][]render.AssistantChangedFile{files.Shown, files.Rest} {
					for i := range list {
						list[i].URL = lead(list[i].URL)
					}
				}
			}
		}
	}
	return view, true
}

// projectReviews are the line comment threads of p, the project comments
// first, whose card holds its git state and what the forge poller read of its
// branch: the review threads and general threads of that branch as the line
// comments hold them and the local ones, judged against the disk, and on a
// forge the comments a publish would post.
// Only the owner reads the ids of the forge's threads and what a thread
// takes: a reply, a resolve, a reopen of a local one, a publish, a hand off
// and the deletes of a thread and a reply the editor offers. The notes offer
// no edit, the editor does.
func (s *Server) projectReviews(c *gin.Context, p project.Project, card render.AssistantProjectCard, owner bool) render.AssistantProjectReviews {
	r := render.AssistantProjectReviews{Project: p.Name}
	b, polled, bound := s.forgePolledOf(p)
	if bound {
		r.Branch = polled.Watch.Branch
	}
	if bound && owner {
		replies, resolves := s.forge.AnswersThreads(b)
		r.Threads = render.NewForgeThreadsOffer(b, polled, replies, resolves)
		r.Publish = render.NewForgePublishOffer(b, polled, s.forge.Publishes(b))
		forgePath := "/projects/" + url.PathEscape(p.Name) + "/forge"
		r.ThreadURL, r.PublishURL = forgePath+"/thread", forgePath+"/publish"
	}
	checkout := ""
	if card.Git.Read && !card.Git.Detached {
		checkout = card.Git.Branch
	}
	if owner {
		r.CommentsURL = "/projects/" + url.PathEscape(p.Name) + "/editor/comments"
	}
	for _, v := range s.reconciledLineComments(p) {
		if card.Forge != nil && linecomment.Publishable(v.Comment) {
			r.Unpublished = append(r.Unpublished, v.ID)
			if v.Project {
				r.UnpublishedGeneral++
			}
		}
		t := render.AssistantThread{
			ID:       v.ID,
			Project:  v.Project,
			Path:     v.Path,
			Line:     v.Line,
			Quote:    strings.TrimSpace(v.LineText),
			Outdated: v.Outdated,
			Notes:    s.lineNotes(c, v.Comment),
		}
		if !v.Project {
			t.FileURL = editorFileURL(p.Name, filesystem.FileRef{Path: v.Path, Line: v.Line})
		}
		for i := range t.Notes {
			t.Notes[i].Edit = false
			t.Notes[i].Drop = t.Notes[i].Drop && owner
		}
		t.Deletes = owner && mayDeleteThread(c, v.Comment)
		switch {
		case v.Forge != nil:
			kind := s.forgeKind(v.Forge.Kind)
			t.ForgeURL, t.ForgeLabel, t.ForgeIcon, t.Unresolvable = v.Forge.URL, kind.Label, kind.Icon, v.Forge.Unresolvable
			switch {
			case !owner:
			case v.Forge.Thread != "" && v.Project:
				t.Thread = v.Forge.Thread
				t.Handoff = threadHandoff("General thread", v.Forge.Thread, p.Name, r.Branch, "", 0)
			case v.Forge.Thread != "":
				t.Thread = v.Forge.Thread
				t.Handoff = threadHandoff("Review thread", v.Forge.Thread, p.Name, r.Branch, v.Path, v.Line)
			case v.Project:
				t.Handoff = threadHandoff("Project comment", v.ID, p.Name, r.Branch, "", 0)
			}
		default:
			t.Local = true
			if owner {
				t.Writes = true
				noun := "Line comment"
				if v.Project {
					noun = "Project comment"
				}
				t.Handoff = threadHandoff(noun, v.ID, p.Name, checkout, v.Path, v.Line)
			}
		}
		if v.Resolved {
			r.Resolved = append(r.Resolved, t)
		} else {
			r.Open = append(r.Open, t)
		}
	}
	return r
}

// threadHandoff is the draft the hand off of a thread puts into the
// assistant's message box: what the thread is with its id, the project, the
// branch and where the thread stands, none for one on the whole project, a
// line of its own the user writes under.
func threadHandoff(noun, id, project, branch, path string, line int) string {
	on, at := "", ""
	if branch != "" {
		on = " on branch " + plaintext.Visible(branch)
	}
	if path != "" {
		at = fmt.Sprintf(" at %s:%d", plaintext.Visible(path), line)
	}
	return fmt.Sprintf("%s %s in project %s%s%s.\n", noun, plaintext.Visible(id), project, on, at)
}

// projectJobs are the jobs of one assistant in one project, the project they
// name, what the forge poller holds of its branch and why it needs the user.
type projectJobs struct {
	name   string
	jobs   []assistant.Job
	open   bool
	latest time.Time
	p      project.Project
	forge  *render.AssistantProjectForge
	needs  []string
}

// assistantProjectsData is the tab: the cards with the git state and the
// review counts of each.
func (s *Server) assistantProjectsData(ctx context.Context, owner string, all bool) render.AssistantProjectsData {
	data, projects := s.assistantProjectCards(owner, all)
	s.readProjectGit(ctx, data.Projects, projects)
	s.countProjectReviews(data.Projects)
	return data
}

// assistantProjectCards builds the cards without git: every project a job of
// owner names that still exists, the ones that need the user first, then the
// ones with an open job, each group by its newest job activity. It answers the
// projects of the cards it renders, the ones whose git the tab shows.
func (s *Server) assistantProjectCards(owner string, all bool) (render.AssistantProjectsData, []project.Project) {
	byName := map[string]*projectJobs{}
	var named []*projectJobs
	for _, job := range s.watcher.ListOf(owner) {
		if job.Project == "" {
			continue
		}
		g := byName[job.Project]
		if g == nil {
			g = &projectJobs{name: job.Project}
			byName[job.Project] = g
			named = append(named, g)
		}
		g.jobs = append(g.jobs, job)
		g.open = g.open || job.State.Open()
		for _, at := range []time.Time{job.CreatedAt, job.UpdatedAt} {
			if at.After(g.latest) {
				g.latest = at
			}
		}
	}
	var groups []*projectJobs
	var found []project.Project
	for _, g := range named {
		p, err := s.projects.FindByName(g.name)
		if err != nil {
			continue
		}
		g.p = p
		groups = append(groups, g)
		found = append(found, p)
	}
	bound := s.forgeByProject(found)
	polled := s.forgePolled(found, bound)
	for _, g := range groups {
		if pl, ok := polled[g.p.Name]; ok {
			g.forge = render.NewAssistantProjectForge(g.p.Name, bound[g.p.Name], pl)
		}
		if g.forge != nil && g.forge.Failed() {
			g.needs = append(g.needs, "The pipeline failed.")
		}
		if reason := stuckJob(g.jobs); reason != "" {
			g.needs = append(g.needs, reason)
		}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		needs := len(groups[i].needs) > 0
		if needs != (len(groups[j].needs) > 0) {
			return needs
		}
		if !needs && groups[i].open != groups[j].open {
			return groups[i].open
		}
		if !groups[i].latest.Equal(groups[j].latest) {
			return groups[i].latest.After(groups[j].latest)
		}
		return groups[i].name < groups[j].name
	})

	data := render.AssistantProjectsData{}
	var projects []project.Project
	marks := s.jobMarks()
	closed := 0
	for _, g := range groups {
		if len(g.needs) > 0 {
			data.Needs++
		} else if !g.open {
			closed++
			if closed > closedProjectsShown {
				data.Older++
				data.Unfolded = all
				if !all {
					data.Folded = append(data.Folded, g.p.Name)
					continue
				}
			}
		}
		card := render.AssistantProjectCard{
			Name:  g.p.Name,
			URL:   projectLanding(g.p.Name),
			Open:  g.open,
			Needs: g.needs,
			Git:   render.AssistantProjectGit{Repo: g.p.GitRepo},
			Forge: g.forge,
		}
		closedJobs := 0
		for _, job := range g.jobs {
			if !job.State.Open() {
				closedJobs++
				if closedJobs > closedCardJobsShown {
					card.Older++
				}
			}
			card.Jobs = append(card.Jobs, s.assistantJobView(job, marks))
		}
		data.Projects = append(data.Projects, card)
		projects = append(projects, g.p)
	}
	return data, projects
}

// stuckJob says why the newest job of an assistant in a project needs the
// user, "" when it does not: it closed blocked or expired, and nothing the
// assistant started there since took over.
func stuckJob(jobs []assistant.Job) string {
	var newest *assistant.Job
	for i := range jobs {
		if newest == nil || jobs[i].CreatedAt.After(newest.CreatedAt) {
			newest = &jobs[i]
		}
	}
	switch {
	case newest == nil:
		return ""
	case newest.State == assistant.JobBlocked:
		return "Its newest job is blocked."
	case newest.State == assistant.JobExpired:
		return "Its newest job expired."
	}
	return ""
}

// countProjectReviews counts on each card the open and the resolved line
// comment threads, the forge's and the local ones, on a line and on the
// project alike, and on a card bound to a forge the comments a publish would
// post.
func (s *Server) countProjectReviews(cards []render.AssistantProjectCard) {
	for i := range cards {
		card := &cards[i]
		for _, comment := range s.lineComments.List(card.Name) {
			if comment.Resolved {
				card.Resolved++
			} else {
				card.Threads++
			}
			if card.Forge != nil && linecomment.Publishable(comment) {
				card.Unpublished++
			}
		}
	}
}

// readProjectGit fills each card's git state from one status read of its
// working copy and one read of the pair the editor's compare opens on, a few
// at a time. It answers the reads of the pairs.
func (s *Server) readProjectGit(ctx context.Context, cards []render.AssistantProjectCard, projects []project.Project) []git.Since {
	reads := make([]git.Since, len(cards))
	slots := make(chan struct{}, projectGitReads)
	var wg sync.WaitGroup
	for i := range cards {
		if !cards[i].Git.Repo {
			continue
		}
		wg.Add(1)
		go func(card *render.AssistantProjectCard, read *git.Since, path string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			repo := git.New(path)
			summary, ok := repo.Summary(ctx)
			if !ok {
				return
			}
			since, err := repo.Since(ctx)
			*read = since
			card.Git = render.AssistantProjectGit{
				Repo:     true,
				Read:     true,
				Branch:   summary.Branch.Name,
				Detached: summary.Branch.Detached,
				Upstream: summary.Branch.Upstream,
				Counted:  summary.Branch.Counted,
				Ahead:    summary.Branch.Ahead,
				Behind:   summary.Branch.Behind,
				Changed:  summary.Changed,
				Since:    sinceView(since, err),
			}
		}(&cards[i], &reads[i], projects[i].Path)
	}
	wg.Wait()
	return reads
}

// sinceView is the pair's read as the card and the Changes section count it.
// A repository without a commit has no base and reads nothing.
func sinceView(read git.Since, err error) render.AssistantProjectSince {
	switch {
	case errors.Is(err, git.ErrNoSplit):
		return render.AssistantProjectSince{Error: fmt.Sprintf("%s shares no history with %s.", read.To.Name, read.From.Name)}
	case err != nil:
		return render.AssistantProjectSince{Error: "git did not answer."}
	case !read.Repo:
		return render.AssistantProjectSince{}
	}
	v := render.AssistantProjectSince{
		Read:    true,
		From:    read.From.Name,
		Commits: read.Commits,
		Changed: read.Changed,
		Added:   read.Added,
		Removed: read.Removed,
	}
	if read.Base != read.From.SHA {
		v.Split = git.ShortSHA(read.Base)
	}
	return v
}

// The first rows of the Changes section, of the files with the largest change
// first and of the newest commits.
const (
	changedFilesShown   = 10
	changedCommitsShown = 10
)

// projectChanges is the Changes section of p, whose card holds its git state
// and read the pair with its files, nil where git did not answer the status.
// It reads what the working copy holds uncommitted where the status counted
// any, and the commits since the split. The rows past the first ones of a
// list are rendered where more names the list, and the rest of the commits
// read only then.
func (s *Server) projectChanges(ctx context.Context, p project.Project, card render.AssistantProjectCard, read git.Since, more []string) *render.AssistantProjectChanges {
	if !card.Git.Read {
		return nil
	}
	changes := &render.AssistantProjectChanges{Project: p.Name, Since: card.Git.Since}
	if card.Git.Changed > 0 {
		uncommitted, err := uncommittedFiles(ctx, git.New(p.Path))
		switch {
		case err != nil:
			changes.UncommittedUnread = true
		case len(uncommitted.Files) > 0:
			list := changedFileList(p.Name, render.ChangesUncommitted, uncommitted.Files, more, uncommitted.Truncated)
			changes.Uncommitted = &list
		}
	}
	if !changes.Since.Read {
		return changes
	}
	changes.CompareURL = editorCompareURL(p.Name, "")
	changes.Files = changedFileList(p.Name, render.ChangesFiles, read.Files, more, read.Truncated)
	commits := slices.Contains(more, render.ChangesCommits)
	if read.Commits == 0 {
		changes.LogRead = true
		return changes
	}
	limit := changedCommitsShown
	if commits {
		limit = read.Commits
	}
	log, err := git.New(p.Path).Between(ctx, read.Base, read.To.SHA, limit)
	if err != nil {
		return changes
	}
	changes.LogRead = true
	entries := make([]render.AssistantCommit, 0, len(log))
	for _, c := range log {
		entries = append(entries, render.AssistantCommit{Short: c.Short, Subject: c.Summary, Author: c.Author, At: time.Unix(c.Time, 0)})
	}
	changes.Commits = render.NewAssistantRows(entries, read.Commits, changedCommitsShown, commits, commits && len(log) < read.Commits)
	return changes
}

// changedFileList is the list of project named list of the files of its
// comparison, the largest change first, its rest rendered where more names
// the list.
func changedFileList(project, list string, files []git.RevisionChange, more []string, truncated bool) render.AssistantFileList {
	rows := changedFiles(project, list, files)
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Added+rows[i].Removed > rows[j].Added+rows[j].Removed
	})
	return render.NewAssistantFileList(project, list, rows, changedFilesShown, slices.Contains(more, list), truncated, changedFileURL(project, list, ""))
}

// changedFiles are the rows of the list named list of the files of its
// comparison in git's order, each opening the editor on it as its list
// compares it.
func changedFiles(project, list string, files []git.RevisionChange) []render.AssistantChangedFile {
	rows := make([]render.AssistantChangedFile, 0, len(files))
	for _, f := range files {
		rows = append(rows, render.AssistantChangedFile{
			List:    list,
			Path:    f.Path,
			From:    f.From,
			Status:  f.Status,
			Added:   f.Added,
			Removed: f.Removed,
			Binary:  f.Binary,
			URL:     changedFileURL(project, list, f.Path),
		})
	}
	return rows
}

// changedFileURL opens the editor on path as the list named list compares it,
// without a path on the whole list: an uncommitted file in the commit view
// with its diff against HEAD, a file of the pair in the compare.
func changedFileURL(project, list, path string) string {
	if list != render.ChangesUncommitted {
		return editorCompareURL(project, path)
	}
	query := url.Values{"view": {"commit"}}
	if path != "" {
		query.Set("file", path)
	}
	return "/projects/" + url.PathEscape(project) + "/editor?" + query.Encode()
}

// editorCompareURL opens the editor's compare on the pair the repository
// suggests, with path opened in it, see the editor's compare view, and
// without a path the compare alone.
func editorCompareURL(project, path string) string {
	query := url.Values{"view": {"compare"}}
	if path == "" {
		query.Set("pair", "suggested")
	} else {
		query.Set("file", path)
	}
	return "/projects/" + url.PathEscape(project) + "/editor?" + query.Encode()
}

// watchAssistantProjects takes the git watch of every card's repository, the
// answer the editor's watch gives: whether polling is on and for how long the
// watch holds.
func (s *Server) watchAssistantProjects(owner string, all bool) gin.H {
	set := s.editorSettings()
	watching := set.GitPollSeconds > 0
	_, projects := s.assistantProjectCards(owner, all)
	for _, p := range projects {
		if p.GitRepo {
			s.watchProjectGit(p, set)
		}
	}
	return gin.H{"watching": watching, "seconds": int(gitWatchWindow / time.Second)}
}

// guestProjectsData is the tab as a guest of the link reads it: every card,
// a project leading into the share where the link grants it and a coder
// where the link shows it.
func (s *Server) guestProjectsData(ctx context.Context, g *guest, id string, all bool) render.AssistantProjectsData {
	data := s.assistantProjectsData(ctx, id, all)
	data.Access = g.access
	lead := s.guestLead(g)
	for i := range data.Projects {
		guestCard(&data.Projects[i], lead)
	}
	return data
}

// guestCard is a card as a guest of the link reads it: the project leading
// into the share where the link grants it, a coder where the link shows it,
// no retry and none of the owner's news.
func guestCard(card *render.AssistantProjectCard, lead func(string) string) {
	card.URL = lead("/projects/" + url.PathEscape(card.Name) + "/editor")
	if card.Forge != nil {
		card.Forge.Retry, card.Forge.RetryURL = nil, ""
	}
	for j := range card.Jobs {
		card.Jobs[j].URL, card.Jobs[j].EditorURL = lead(card.Jobs[j].URL), lead(card.Jobs[j].EditorURL)
		card.Jobs[j].News = false
	}
}

// handleShareAssistantProjects is the Projects tab a guest's page pulls, the
// owner's fragment read only.
func (s *Server) handleShareAssistantProjects(c *gin.Context) {
	g, _ := guestOf(c)
	c.HTML(http.StatusOK, "assistant_projects_list.gohtml", s.guestProjectsData(c.Request.Context(), g, g.assistant.ID, listsAll(c)))
}

// handleShareAssistantProjectsWatch is the owner's watch for the link's
// assistant: its projects are polled whether the link grants them or not,
// since the guest reads their cards.
func (s *Server) handleShareAssistantProjectsWatch(c *gin.Context) {
	g, _ := guestOf(c)
	c.JSON(http.StatusOK, s.watchAssistantProjects(g.assistant.ID, listsAll(c)))
}

// assistantsWorkIn answers whether a job of one of the assistants names the
// project, which is when a guest of those assistants hears its git signal.
func (s *Server) assistantsWorkIn(assistants []string, name string) bool {
	for _, id := range assistants {
		for _, job := range s.watcher.ListOf(id) {
			if job.Project == name {
				return true
			}
		}
	}
	return false
}
