package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/linecomment"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/share"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// checkCommentPath verifies that a comment's path stays inside the project.
// The file itself does not have to exist: a note may outlive its file, and
// the sheet still wants to show it.
func checkCommentPath(c *gin.Context, root, rel string) bool {
	if strings.TrimSpace(rel) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A file path is required."})
		return false
	}
	if _, err := filesystem.ResolveUnder(root, rel); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return false
	}
	return true
}

// lineCommentView is one comment the way a read answers it: the stored
// thread, whether its quote still matches the file, judged on every read and
// never stored, so a file that comes back heals its comments by itself, and
// where a thread of the forge came from. Resolved is the thread's state. Mine
// marks a note the reading guest wrote under its own link. Nothing of the link a guest wrote a note under
// shows, another guest has no business with it. Project marks a thread on the
// whole project, its path empty and its line 0. Was is the id a published
// comment had before, which a page that held it follows.
type lineCommentView struct {
	ID        string           `json:"id"`
	Project   bool             `json:"project,omitempty"`
	Path      string           `json:"path"`
	Line      int              `json:"line"`
	LineText  string           `json:"lineText,omitempty"`
	Text      string           `json:"text"`
	Author    string           `json:"author,omitempty"`
	Forge     *forgeThreadView `json:"forge,omitempty"`
	Replies   []lineReplyView  `json:"replies,omitempty"`
	Resolved  bool             `json:"resolved,omitempty"`
	Was       string           `json:"was,omitempty"`
	UpdatedAt time.Time        `json:"updatedAt"`
	Outdated  bool             `json:"outdated,omitempty"`
	Mine      bool             `json:"mine,omitempty"`
}

// lineReplyView is one reply of a thread the way a read answers it, Mine
// marking one the reading guest wrote.
type lineReplyView struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Author    string    `json:"author,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
	Mine      bool      `json:"mine,omitempty"`
}

// forgeThreadView is what a reader sees of where a thread of the forge came
// from: the kind of forge with its name and icon, the thread's page there and
// whether the forge resolves it. The owner, who answers and resolves threads,
// reads the thread's id too, none for a note the forge keeps alone.
type forgeThreadView struct {
	Kind         string `json:"kind"`
	Label        string `json:"label"`
	Icon         string `json:"icon"`
	URL          string `json:"url,omitempty"`
	Thread       string `json:"thread,omitempty"`
	Unresolvable bool   `json:"unresolvable,omitempty"`
}

// commentWriterOf answers who a request writes line comments as, the guest
// of the link it came in through or the owner.
func commentWriterOf(c *gin.Context) linecomment.Writer {
	if g, ok := guestOf(c); ok {
		return linecomment.Writer{Link: g.share.ID, Name: g.share.Name}
	}
	return linecomment.Writer{}
}

// commentView is a judged comment the way w reads it.
func (s *Server) commentView(w linecomment.Writer, v linecomment.View) lineCommentView {
	view := lineCommentView{
		ID:        v.ID,
		Project:   v.Project,
		Path:      v.Path,
		Line:      v.Line,
		LineText:  v.LineText,
		Text:      v.Text,
		Author:    v.Author,
		UpdatedAt: v.UpdatedAt,
		Resolved:  v.Resolved,
		Was:       v.Was,
		Outdated:  v.Outdated,
		Mine:      w.Link != "" && v.Link == w.Link,
	}
	for _, r := range v.Replies {
		view.Replies = append(view.Replies, lineReplyView{ID: r.ID, Text: r.Text, Author: r.Author, UpdatedAt: r.UpdatedAt, Mine: w.Link != "" && r.Link == w.Link})
	}
	if f := v.Forge; f != nil {
		kind := s.forgeKind(f.Kind)
		view.Forge = &forgeThreadView{Kind: f.Kind, Label: kind.Label, Icon: kind.Icon, URL: f.URL, Unresolvable: f.Unresolvable}
		if w.Link == "" {
			view.Forge.Thread = f.Thread
		}
	}
	return view
}

// forgeKind is the kind of forge with that id, its id and the globe for one
// no provider of this build knows.
func (s *Server) forgeKind(id string) forge.Kind {
	if s.forge != nil {
		if k, ok := s.forge.Kind(id); ok {
			return k
		}
	}
	return forge.Kind{ID: id, Label: id, Icon: "ti-world"}
}

// reconciledLineComments is the one read every answer goes through: it judges
// the stored comments against the disk, persists what could be rebound and
// publishes the move, so every open editor follows the repair live.
func (s *Server) reconciledLineComments(p project.Project) []linecomment.View {
	views, changed := s.lineComments.Reconciled(p.Name, p.Path)
	if changed {
		s.publishLineComments(p.Name)
	}
	return views
}

// handleEditorComments answers the project's line comments, which is what
// the gutter marks, the sheet and every catch-up after the linecomments
// event pull, each judged against the disk on the way out. `?path=` narrows
// the answer for the CLI's list, it may repeat and each value ors in an
// exact file, a folder, or a glob with * and **, and leaves the project
// comments out; the browser asks without one and gets everything.
func (s *Server) handleEditorComments(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	w := commentWriterOf(c)
	patterns := c.QueryArray("path")
	list := []lineCommentView{}
	for _, comment := range s.reconciledLineComments(p) {
		if len(patterns) == 0 || comment.OnPath(patterns) {
			list = append(list, s.commentView(w, comment))
		}
	}
	c.JSON(http.StatusOK, gin.H{"comments": list})
}

// handleEditorCommentThread answers the notes of the thread with the id as the
// thread dialog shows them, the one rendering of a thread's notes. A thread
// deleted meanwhile answers 404, the dialog keeps what it showed.
func (s *Server) handleEditorCommentThread(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	id := c.Query("id")
	for _, comment := range s.lineComments.List(p.Name) {
		if comment.ID == id {
			c.HTML(http.StatusOK, "line_comment_notes.gohtml", s.lineNotes(c, comment))
			return
		}
	}
	c.String(http.StatusNotFound, "The thread is gone.")
}

// lineNotes are the notes of comment as the request's writer reads them: a
// note of the forge by its author there or the forge's name, your own as You,
// the owner's to a guest as Owner and a guest's by its link's name, with the
// edit and the delete of what the writer may change. A thread of the forge
// changes there and offers neither.
func (s *Server) lineNotes(c *gin.Context, comment linecomment.Comment) []render.LineNote {
	w := commentWriterOf(c)
	writes := true
	if g, ok := guestOf(c); ok {
		writes = g.access.Can(share.Comments)
	}
	name := func(author, link string) string {
		switch {
		case comment.Forge != nil && author != "":
			return author
		case comment.Forge != nil:
			return s.forgeKind(comment.Forge.Kind).Label
		case w.Link != "" && link == w.Link, w.Link == "" && author == "":
			return "You"
		case author != "":
			return author
		}
		return "Owner"
	}
	local := comment.Forge == nil && writes
	notes := []render.LineNote{{Name: name(comment.Author, comment.Link), At: comment.UpdatedAt, Text: comment.Text, Edit: local && w.May(comment)}}
	for _, r := range comment.Replies {
		mine := local && w.MayReply(r)
		notes = append(notes, render.LineNote{ID: r.ID, Name: name(r.Author, r.Link), At: r.UpdatedAt, Text: r.Text, Edit: mine, Drop: mine})
	}
	return notes
}

type editorCommentSaveRequest struct {
	ID       string  `json:"id"`
	Project  bool    `json:"project"`
	Path     string  `json:"path"`
	Line     int     `json:"line"`
	LineText *string `json:"lineText"`
	Text     string  `json:"text"`
}

// noteText is the text of a note a request sends, trimmed, refused when it
// is empty or longer than a note holds.
func noteText(c *gin.Context, raw, noun string) (string, bool) {
	text := strings.TrimSpace(raw)
	switch {
	case text == "":
		c.JSON(http.StatusBadRequest, gin.H{"error": "A " + noun + " is required."})
		return "", false
	case len(text) > linecomment.MaxText:
		c.JSON(http.StatusBadRequest, gin.H{"error": "That " + noun + " is too long."})
		return "", false
	}
	return text, true
}

// answerThread answers a write of one thread: a refusal with its status, a
// thread or reply deleted on another device meanwhile as gone instead of the
// request echoed back as stored, else the thread as w reads it. Only a write
// that changed something is published, so the other devices are not woken for
// a note they already hold. It reports whether the write went through.
func (s *Server) answerThread(c *gin.Context, p project.Project, w linecomment.Writer, thread linecomment.Comment, changed bool, err error) bool {
	if errors.Is(err, linecomment.ErrNotYours) || errors.Is(err, linecomment.ErrForgeThread) {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return false
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return false
	}
	if changed {
		s.publishLineComments(p.Name)
	}
	if thread.ID == "" {
		c.JSON(http.StatusOK, gin.H{"gone": true})
		return false
	}
	c.JSON(http.StatusOK, gin.H{"comment": s.commentView(w, linecomment.View{Comment: thread})})
	return true
}

// handleEditorCommentSave stores the first note of a thread: a new one
// without an id, an edited one under its id, and a renamed file's note under
// its id with the new path. With project the thread stands on the whole
// project and the request names no path, line or quote, without it a line
// comment names a path and a line. A guest's note is written under its link,
// a new one is news for the owner, and a note of anybody else is refused to
// it, a thread of the forge to everybody.
func (s *Server) handleEditorCommentSave(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCommentSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	if !checkCommentPlace(c, p.Path, req) {
		return
	}
	if req.Text, ok = noteText(c, req.Text, "comment"); !ok {
		return
	}
	// The quote is the sender's buffer line, stored untouched even when it
	// is empty: a note born in a dirty buffer talks about content the disk
	// does not know yet, other readers see it outdated until the save. A
	// request without the field is a caller without a buffer, the CLI's add,
	// and there the anchor is read from the disk, where it lives; a file
	// that cannot be read as text or a line past its end answer empty, and
	// the comment stands without a quote.
	quote := ""
	switch {
	case req.LineText != nil:
		quote = *req.LineText
	case !req.Project:
		quote = linecomment.DiskQuote(p.Path, req.Path, req.Line)
	}
	w := commentWriterOf(c)
	comment, changed, err := s.lineComments.Save(p.Name, linecomment.Comment{
		ID:       req.ID,
		Project:  req.Project,
		Path:     req.Path,
		Line:     req.Line,
		LineText: linecomment.FileLine(quote),
		Text:     req.Text,
	}, w)
	if s.answerThread(c, p, w, comment, changed, err) && req.ID == "" {
		s.guestWrote(p, w, comment, comment.Text, false)
	}
}

// checkCommentPlace verifies what a new or edited first note stands on: a
// project comment names no path, no line and no quote, a line comment a path
// inside the project and a line.
func checkCommentPlace(c *gin.Context, root string, req editorCommentSaveRequest) bool {
	if req.Project {
		if req.Path != "" || req.Line != 0 || req.LineText != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "A project comment has no file and no line."})
			return false
		}
		return true
	}
	if !checkCommentPath(c, root, req.Path) {
		return false
	}
	if req.Line < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A line number is required."})
		return false
	}
	return true
}

type editorCommentReplyRequest struct {
	Thread string `json:"thread"`
	ID     string `json:"id"`
	Text   string `json:"text"`
}

// handleEditorCommentReply stores one reply of a thread: a new one without an
// id, an edited one under its id. A guest's reply is written under its link,
// a new one is news for the owner, and an edit of anybody else's is refused
// to it. A thread of the forge takes its replies there.
func (s *Server) handleEditorCommentReply(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCommentReplyRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Thread == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	text, ok := noteText(c, req.Text, "reply")
	if !ok {
		return
	}
	w := commentWriterOf(c)
	thread, changed, err := s.lineComments.Reply(p.Name, req.Thread, linecomment.Reply{ID: req.ID, Text: text}, w)
	if s.answerThread(c, p, w, thread, changed, err) && req.ID == "" {
		s.guestWrote(p, w, thread, text, true)
	}
}

type editorCommentReplyDeleteRequest struct {
	Thread string `json:"thread"`
	ID     string `json:"id"`
}

// handleEditorCommentReplyDelete takes one reply from its thread, for a guest
// only one written under its link.
func (s *Server) handleEditorCommentReplyDelete(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCommentReplyDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Thread == "" || req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	w := commentWriterOf(c)
	thread, changed, err := s.lineComments.RemoveReply(p.Name, req.Thread, req.ID, w)
	s.answerThread(c, p, w, thread, changed, err)
}

type editorCommentResolveRequest struct {
	ID       string `json:"id"`
	Resolved bool   `json:"resolved"`
}

// handleEditorCommentResolve marks a thread resolved or open again, for a
// guest only one it opened.
func (s *Server) handleEditorCommentResolve(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCommentResolveRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	w := commentWriterOf(c)
	thread, changed, err := s.lineComments.Resolve(p.Name, req.ID, req.Resolved, w)
	s.answerThread(c, p, w, thread, changed, err)
}

// GuestComment is a note a guest wrote on a project, the first of a thread or
// a reply, with where its thread stands, the whole project when OnProject
// says so.
type GuestComment struct {
	Project   string
	Guest     string
	OnProject bool
	Path      string
	Line      int
	Text      string
	Reply     bool
}

// SetGuestComment hands every new thread and reply a guest writes to notify,
// which words the owner's news.
func (s *Server) SetGuestComment(notify func(GuestComment)) {
	s.guestComment = notify
}

// guestWrote rings the owner's news of a note a guest just wrote, nothing for
// one of the owner.
func (s *Server) guestWrote(p project.Project, w linecomment.Writer, thread linecomment.Comment, text string, reply bool) {
	if w.Link == "" || s.guestComment == nil {
		return
	}
	s.guestComment(GuestComment{Project: p.Name, Guest: w.Name, OnProject: thread.Project, Path: thread.Path, Line: thread.Line, Text: text, Reply: reply})
}

type editorCommentsDeleteRequest struct {
	IDs      []string `json:"ids"`
	Paths    []string `json:"paths"`
	Outdated bool     `json:"outdated"`
	All      bool     `json:"all"`
}

// handleEditorCommentsDelete takes notes away: the named ones, whole files or
// folders through paths (each value an exact file, a folder, or a glob with
// * and **, ored together, never a project comment), the outdated ones — judged in the same read that
// answers the list, so a quote that can still be rebound is repaired first
// and never counts as an orphan; paths narrow which orphans fall — or with
// all everything the project holds, which is what the sheet's delete-all
// spends. The paths are match expressions against stored comments, never
// filesystem access, so they are not resolved against the disk. A guest's
// delete reaches only the notes written under its link, whatever it names,
// and nobody's a thread of the forge, which goes there or when the forge is
// no longer read. A thread goes with its replies, so a guest's delete takes
// one only when it wrote every note of it. The answer counts what fell, which
// is what the CLI reports.
func (s *Server) handleEditorCommentsDelete(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCommentsDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	named := map[string]bool{}
	pick := func(comment linecomment.Comment) bool { return named[comment.ID] }
	switch {
	case req.All:
		pick = func(linecomment.Comment) bool { return true }
	case req.Outdated:
		for _, view := range s.reconciledLineComments(p) {
			if view.Outdated && (len(req.Paths) == 0 || view.OnPath(req.Paths)) {
				named[view.ID] = true
			}
		}
	case len(req.Paths) > 0:
		pick = func(comment linecomment.Comment) bool { return comment.OnPath(req.Paths) }
	default:
		for _, id := range req.IDs {
			named[id] = true
		}
	}
	w := commentWriterOf(c)
	removed := s.lineComments.Remove(p.Name, func(comment linecomment.Comment) bool { return pick(comment) && w.MayAll(comment) })
	if removed > 0 {
		s.publishLineComments(p.Name)
	}
	c.JSON(http.StatusOK, gin.H{"deleted": removed > 0, "removed": removed})
}

type editorCommentsMoveRequest struct {
	Path     string                   `json:"path"`
	Comments []editorCommentMoveEntry `json:"comments"`
}

type editorCommentMoveEntry struct {
	ID       string `json:"id"`
	Line     int    `json:"line"`
	LineText string `json:"lineText"`
}

// handleEditorCommentsMove syncs where one file's notes stand after the open
// buffer edited around them: the client maps the lines through its changes
// and lands every pause here, debounced like the commit draft. A guest moves
// only the notes written under its link, the others stay where they are.
func (s *Server) handleEditorCommentsMove(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCommentsMoveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	if !checkCommentPath(c, p.Path, req.Path) {
		return
	}
	positions := make([]linecomment.Comment, 0, len(req.Comments))
	for _, entry := range req.Comments {
		if entry.ID == "" || entry.Line < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
			return
		}
		positions = append(positions, linecomment.Comment{
			ID:       entry.ID,
			Line:     entry.Line,
			LineText: linecomment.FileLine(entry.LineText),
		})
	}
	if s.lineComments.Move(p.Name, req.Path, positions, commentWriterOf(c)) {
		s.publishLineComments(p.Name)
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
}
