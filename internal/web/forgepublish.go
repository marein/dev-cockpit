package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/git"
	"github.com/marein/dev-cockpit/internal/linecomment"
	"github.com/marein/dev-cockpit/internal/plaintext"
	"github.com/marein/dev-cockpit/internal/project"
)

// maxForgeCommentID bounds the id of a line comment a publish names, one
// statefile.NewID mints and more.
const maxForgeCommentID = 64

// maxApprovalComments bounds the comments an approval of a publish shows one
// by one, the rest it counts.
const maxApprovalComments = 10

// forgeDraft is a comment the way it publishes: the comment, the draft of it
// at its line in the pushed head, and that line's text there, a general
// draft without either for a project comment.
type forgeDraft struct {
	comment linecomment.Comment
	draft   forge.Draft
	quote   string
}

// forgePublished is what a publish posted: the change request it posted to
// and each comment's note there, in the order of the comments.
type forgePublished struct {
	change forge.Change
	drafts []forgeDraft
	notes  []forge.Note
}

// handleProjectForgePublish publishes comments the owner wrote as review
// comments on the open change request of the branch HEAD of a project is on,
// each a review thread of its own on its line of the diff, a project comment
// a general comment: the ones the form names by id, or with all every one of
// them. Each line comment has to stand in the pushed head as it quotes its
// line and on a line the change request's diff shows, or none is published.
// A comment published becomes the thread of the forge it opened there. A
// click in the browser publishes at once. An assistant's call over the local
// socket waits for the user's approval, whose run resolves the project, its
// binding, the branch and the comments again by name and publishes them only
// as the user saw them.
func (s *Server) handleProjectForgePublish(c *gin.Context) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown project."})
		return
	}
	owner := ""
	if s.localCall(c) {
		if owner, err = s.assistantCaller(c); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	ids, all := c.PostFormArray("id"), c.PostForm("all") == "1"
	if refusal := forgePublishAsked(ids, all); refusal != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": refusal})
		return
	}
	ctx := c.Request.Context()
	w, _, code, err := s.forgeWatch(ctx, p, "")
	if err != nil {
		c.JSON(code, gin.H{"error": err.Error()})
		return
	}
	var said map[string]forge.Draft
	if owner != "" {
		picked, err := s.forgePublishPick(p, ids, all)
		if err != nil {
			c.JSON(forgePublishCode(err), gin.H{"error": err.Error()})
			return
		}
		drafts, err := forgeDrafts(ctx, p, w, picked)
		if err != nil {
			c.JSON(forgePublishCode(err), gin.H{"error": err.Error()})
			return
		}
		target, err := s.forge.PublishTarget(ctx, w, forgeDraftsOf(drafts))
		if err != nil {
			c.JSON(forgePublishCode(err), gin.H{"error": err.Error()})
			return
		}
		if s.askApproval(c, s.forgePublishApproval(owner, w, target.Change, drafts)) {
			return
		}
		ids, said = forgeSaid(drafts)
		all = false
	}
	done, err := s.publishForgeComments(ctx, p, w, ids, all, said)
	if err != nil {
		c.JSON(forgePublishCode(err), gin.H{"error": err.Error()})
		return
	}
	if owner != "" {
		c.JSON(http.StatusOK, gin.H{"ok": true, "published": forgePublishedText(w, done).Plain})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": forgePublishedMessage(w.Binding.Host.Kind, done)})
}

// forgePublishAsked says why a publish that names ids, or all, is no request
// to publish, "" when it is one.
func forgePublishAsked(ids []string, all bool) string {
	switch {
	case all && len(ids) > 0:
		return "Name comments by their ids, or all of them, not both."
	case !all && len(ids) == 0:
		return "Name the comments to publish by their ids, or all of them."
	case len(ids) > linecomment.MaxPerProject:
		return fmt.Sprintf("A publish names at most %d comments.", linecomment.MaxPerProject)
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || utf8.RuneCountInString(id) > maxForgeCommentID {
			return fmt.Sprintf("A comment's id has 1 to %d characters.", maxForgeCommentID)
		}
	}
	return ""
}

// forgePublishPick answers the comments of p a publish names, in the order
// the sheet lists them: those with the ids, or with all every one the owner
// wrote that has no reply. A thread of the forge, a published one named by
// the id it had too, one a guest wrote, one with replies and an id nobody
// holds are refused.
func (s *Server) forgePublishPick(p project.Project, ids []string, all bool) ([]linecomment.Comment, error) {
	refuse := func(format string, args ...any) ([]linecomment.Comment, error) {
		return nil, &forge.PublishRefusal{Reason: fmt.Sprintf(format, args...)}
	}
	list := s.lineComments.List(p.Name)
	var picked []linecomment.Comment
	if all {
		for _, comment := range list {
			if linecomment.Publishable(comment) {
				picked = append(picked, comment)
			}
		}
		if len(picked) == 0 {
			return refuse("%q holds no comment of yours to publish.", p.Name)
		}
		return picked, nil
	}
	named := func(comment linecomment.Comment, id string) bool {
		return comment.ID == id || comment.Was != "" && comment.Was == id
	}
	for _, comment := range list {
		if slices.ContainsFunc(ids, func(id string) bool { return named(comment, id) }) {
			picked = append(picked, comment)
		}
	}
	for _, id := range ids {
		at := slices.IndexFunc(picked, func(comment linecomment.Comment) bool { return named(comment, id) })
		if at < 0 {
			return refuse("%q holds no comment %q.", p.Name, id)
		}
		c := picked[at]
		switch {
		case c.Forge != nil:
			return refuse("%s is a review thread on the forge already.", commentPlace(c))
		case c.Link != "":
			return refuse("%s is a guest's, only your own publish.", commentPlace(c))
		case len(c.Replies) > 0:
			return refuse("%s has replies, it does not publish.", commentPlace(c))
		}
	}
	return picked, nil
}

// commentPlace names a comment the way a refusal of a publish does: by its
// file and line, a project comment by its id.
func commentPlace(c linecomment.Comment) string {
	if c.Project {
		return fmt.Sprintf("The project comment %q", c.ID)
	}
	return fmt.Sprintf("The comment at %q:%d", c.Path, c.Line)
}

// forgeDrafts places each comment picked at the line of its file in w's
// pushed head that holds its quote the way the comment follows the file:
// where it stands, else the one line holding it, a project comment as a
// general draft. A branch never pushed, a file the pushed head lacks and a
// quote it does not hold so are refused, see forgeUnpushed.
func forgeDrafts(ctx context.Context, p project.Project, w forge.Watch, picked []linecomment.Comment) ([]forgeDraft, error) {
	if err := forge.Unpushed(w); err != nil {
		return nil, err
	}
	repo := git.New(p.Path)
	files := map[string][]string{}
	drafts := make([]forgeDraft, 0, len(picked))
	for _, comment := range picked {
		if comment.Project {
			drafts = append(drafts, forgeDraft{comment: comment, draft: forge.Draft{General: true, Body: comment.Text}})
			continue
		}
		lines, seen := files[comment.Path]
		if !seen {
			content, ok, err := repo.FileAt(ctx, w.Head, comment.Path)
			if err != nil {
				return nil, err
			}
			if ok {
				lines = strings.Split(string(content), "\n")
			}
			files[comment.Path] = lines
		}
		if lines == nil {
			return nil, forgeUnpushed(p, comment, fmt.Sprintf("%q is not in the pushed head %s of %q", comment.Path, forge.ShortHead(w.Head), w.Branch))
		}
		at, ok := linecomment.Locate(lines, comment.Line, linecomment.FileLine(comment.LineText))
		if !ok {
			return nil, forgeUnpushed(p, comment, fmt.Sprintf("The line the comment at %q:%d quotes is not in the pushed head %s of %q", comment.Path, comment.Line, forge.ShortHead(w.Head), w.Branch))
		}
		drafts = append(drafts, forgeDraft{comment: comment, draft: forge.Draft{Path: comment.Path, Line: at, Shown: comment.Line, Body: comment.Text}, quote: lines[at-1]})
	}
	return drafts, nil
}

// forgeUnpushed refuses a comment whose file or quoted line the pushed head
// lacks, missing saying which. A push carries only what the saved file
// holds, a line typed and not saved yet or one gone from the file it never
// will. A saved file that does not read as text, too large or binary or no
// regular file, cannot tell which.
func forgeUnpushed(p project.Project, comment linecomment.Comment, missing string) error {
	if _, _, err := filesystem.ReadFileText(p.Path, comment.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &forge.PublishRefusal{Reason: missing + ", and the saved file does not read as text."}
	}
	if views, _ := linecomment.Reconcile(p.Path, []linecomment.Comment{comment}); views[0].Outdated {
		return &forge.PublishRefusal{Reason: fmt.Sprintf("The line the comment at %q:%d quotes is not in the saved file: save it, or delete the outdated comment or write it again on its line.", comment.Path, comment.Line)}
	}
	return &forge.PublishRefusal{Reason: missing + ", push it first."}
}

func forgeDraftsOf(drafts []forgeDraft) []forge.Draft {
	out := make([]forge.Draft, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, d.draft)
	}
	return out
}

// forgeSaid names the comments of drafts by their ids, each with the draft
// it makes now, its file, its line in the pushed head and its text, or a
// general one with its text, which a publish of them later has to make
// unchanged.
func forgeSaid(drafts []forgeDraft) ([]string, map[string]forge.Draft) {
	ids := make([]string, 0, len(drafts))
	said := make(map[string]forge.Draft, len(drafts))
	for _, d := range drafts {
		ids = append(ids, d.comment.ID)
		said[d.comment.ID] = d.draft
	}
	return ids, said
}

// publishForgeComments publishes the comments of p the ids name, or with all
// every one of the owner's, on the open change request of w's branch, and
// makes each one posted the thread of the forge it opened, the ones posted
// before a refusal too. With said each has to make the draft it names, in the
// same file on the same line of the pushed head with the same text. One
// publish runs at a time and picks its comments when its turn comes, so a
// comment another one published meanwhile is refused, not posted twice.
func (s *Server) publishForgeComments(ctx context.Context, p project.Project, w forge.Watch, ids []string, all bool, said map[string]forge.Draft) (forgePublished, error) {
	s.forgePublishMu.Lock()
	defer s.forgePublishMu.Unlock()
	picked, err := s.forgePublishPick(p, ids, all)
	if err != nil {
		return forgePublished{}, err
	}
	drafts, err := forgeDrafts(ctx, p, w, picked)
	if err != nil {
		return forgePublished{}, err
	}
	for _, d := range drafts {
		if was, ok := said[d.comment.ID]; said != nil && (!ok || was.Path != d.draft.Path || was.Line != d.draft.Line || was.Body != d.draft.Body || was.General != d.draft.General) {
			return forgePublished{}, &forge.PublishRefusal{Reason: fmt.Sprintf("%s changed since you were asked, nothing was published.", commentPlace(d.comment))}
		}
	}
	target, notes, err := s.forge.Publish(ctx, w, forgeDraftsOf(drafts))
	kind := w.Binding.Host.Kind
	if len(notes) > 0 {
		published := make(map[string]linecomment.ForgeThread, len(notes))
		for i, n := range notes {
			d := drafts[i]
			thread := linecomment.ForgeThread{
				Kind:    kind.ID,
				URL:     n.URL,
				Project: d.draft.General,
				Notes:   []linecomment.ForgeNote{{ID: n.ID, Author: n.Author, Text: n.Body, Created: n.Created}},
			}
			if !d.draft.General {
				thread.At = linecomment.Place{Path: d.draft.Path, Line: d.draft.Line, Quote: d.quote, Quoted: true}
			}
			published[d.comment.ID] = thread
		}
		if s.lineComments.Published(p.Name, published) {
			s.publishLineComments(p.Name)
		}
	}
	if err != nil && len(notes) > 0 {
		err = fmt.Errorf("%d of the %d comments are published on the %s %s, the rest is not. %w", len(notes), len(drafts), kind.ChangeNoun, kind.ChangeRef(target.Change.Number), err)
	}
	return forgePublished{change: target.Change, drafts: drafts, notes: notes}, err
}

// forgePublishCode is the status a failed publish answers: a conflict where
// the forge was not asked to post anything, a bad gateway where it refused.
func forgePublishCode(err error) int {
	var refused *forge.PublishRefusal
	var off *forge.BranchOff
	switch {
	case errors.As(err, &refused), errors.As(err, &off), errors.Is(err, forge.ErrNoToken), errors.Is(err, forge.ErrNoStatus), errors.Is(err, forge.ErrNoPublish):
		return http.StatusConflict
	}
	return http.StatusBadGateway
}

// forgePublishApproval is the approval an assistant's publish waits for, once
// per set of comments. Its run resolves the project, its binding, the branch
// HEAD is on and the comments again by name, and publishes them only where
// each still says what the user saw: one changed, gone or published meanwhile
// refuses it, and so does a project bound elsewhere or on another branch.
func (s *Server) forgePublishApproval(owner string, w forge.Watch, change forge.Change, drafts []forgeDraft) approval.Request {
	kind := w.Binding.Host.Kind
	project, branch, address := w.Project, w.Branch, w.Binding.Address()
	ref := kind.ChangeRef(change.Number)
	details := []askpass.Detail{{Label: "Action", Value: "Publish " + commentsNoun(drafts, false)}}
	for i, d := range drafts {
		if i >= maxApprovalComments {
			break
		}
		at := fmt.Sprintf("%s:%d", d.draft.Path, d.draft.Line)
		if d.draft.General {
			at = "project"
		}
		details = append(details, askpass.Detail{Label: "Comment", Value: at + " · " + d.comment.Text})
	}
	if rest := len(drafts) - maxApprovalComments; rest > 0 {
		details = append(details, askpass.Detail{Label: "And", Value: fmt.Sprintf("%d more comments", rest)})
	}
	on := fmt.Sprintf("%s %s · %s", kind.ChangeNoun, ref, change.Title)
	if change.URL != "" {
		on += " · " + change.URL
	}
	details = append(details,
		askpass.Detail{Label: "On", Value: on},
		askpass.Detail{Label: "Branch", Value: fmt.Sprintf("%s at %s", branch, forge.ShortHead(w.Head))},
		askpass.Detail{Label: "Forge", Value: kind.Label + " · " + address},
		askpass.Detail{Label: "Project", Value: project},
	)
	ids, said := forgeSaid(drafts)
	key := slices.Sorted(slices.Values(ids))
	return approval.Request{
		Owner:    owner,
		Kind:     approvalForgePublish,
		Key:      project + "\n" + strings.Join(key, "\n"),
		What:     fmt.Sprintf("Publish %s on %s %s of %s in %s", commentsNoun(drafts, true), kind.ChangeNoun, ref, plaintext.Visible(branch), project),
		Details:  details,
		Projects: []string{project},
		Run: func() (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), forgeWriteTimeout)
			defer cancel()
			p, err := s.projects.FindByName(project)
			if err != nil {
				return "", errors.New("the project no longer exists")
			}
			w, _, _, err := s.forgeWatch(ctx, p, "")
			if err != nil {
				return "", err
			}
			if now := w.Binding.Address(); now != address {
				return "", fmt.Errorf("%q is bound to %s now, not to %s", project, now, address)
			}
			if w.Branch != branch {
				return "", fmt.Errorf("HEAD of %q is on %q now, not on %q", project, w.Branch, branch)
			}
			done, err := s.publishForgeComments(ctx, p, w, ids, false, said)
			if err != nil {
				return "", err
			}
			return forgePublishedText(w, done).Note, nil
		},
	}
}

// commentsNoun names the comments of drafts, line comments, project comments
// or comments where both kinds publish, counted, or bare as an action names
// them.
func commentsNoun(drafts []forgeDraft, counted bool) string {
	general := 0
	for _, d := range drafts {
		if d.draft.General {
			general++
		}
	}
	kind := "line comment"
	switch {
	case general == len(drafts):
		kind = "project comment"
	case general > 0:
		kind = "comment"
	}
	switch {
	case !counted:
		return kind + "s"
	case len(drafts) == 1:
		return "a " + kind
	}
	return fmt.Sprintf("%d %ss", len(drafts), kind)
}

// forgePublishedText is what an assistant reads of a publish that went
// through, in the answer and in the note of an approval.
func forgePublishedText(w forge.Watch, done forgePublished) assistant.ForgeOutcome {
	kind := w.Binding.Host.Kind
	data := assistant.ForgePublish{
		Forge:   kind.Label,
		Address: w.Binding.Address(),
		Noun:    kind.ChangeNoun,
		Ref:     kind.ChangeRef(done.change.Number),
		Title:   done.change.Title,
		Branch:  w.Branch,
		Project: w.Project,
	}
	for i, n := range done.notes {
		d := done.drafts[i].draft
		data.Notes = append(data.Notes, assistant.ForgePublishedNote{Path: d.Path, Line: d.Line, URL: n.URL, Project: d.General})
	}
	return assistant.ForgePublished(data)
}

// forgePublishedMessage is what the user reads of a publish a click ran, the
// same words each time: the notes stand among the line comments.
func forgePublishedMessage(kind forge.Kind, done forgePublished) string {
	on := fmt.Sprintf("%s %s", kind.ChangeNoun, kind.ChangeRef(done.change.Number))
	if len(done.notes) == 1 {
		return fmt.Sprintf("The comment is published on %s.", on)
	}
	return fmt.Sprintf("The %d comments are published on %s.", len(done.notes), on)
}
