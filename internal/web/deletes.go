package web

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
)

// An assistant's delete command may name several coders, projects or
// assistants, and they travel as one request: one approval, one dialog, one
// run and one note. A kind resolves a name into a deleteTarget, everything
// after that knows no kind.

// deleteTarget is one thing a delete names, as its kind resolves it: Key is
// its stable identity, Name what the dialog and the note call it ("coder
// worker"), Details its rows in the dialog, Project the project it belongs to,
// and Delete removes it, answering the outcome sentence and the keys of the
// targets it took along. Delete resolves the target again, an approval may
// come half an hour later, and wait asks for the deletion's real end where one
// goes on off the request.
type deleteTarget struct {
	Key     string
	Name    string
	Details []askpass.Detail
	Project string
	Delete  func(wait bool) (text string, along []string, err error)
}

// deleteKind is a kind of delete: its approval kind, the plural the headline
// counts in, and how a name becomes a target.
type deleteKind struct {
	ID      string
	Plural  string
	Resolve func(key string) (deleteTarget, error)
}

// deleteResult is how one target's delete ended.
type deleteResult struct {
	Name string
	Text string
	Err  error
}

// handleDeletes answers a delete of the targets the form field names. A local
// call is an assistant's and waits for one approval covering all of them where
// the kind asks; otherwise every target is deleted at once and the answer
// names each outcome. one, where the route answered a single target before it
// took several, keeps answering a single target the way it always did.
func (s *Server) handleDeletes(kind deleteKind, field string, one gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if keys := c.PostFormArray(field); one == nil || len(keys) > 1 {
			s.deleteTargets(c, kind, keys)
			return
		}
		one(c)
	}
}

func (s *Server) deleteTargets(c *gin.Context, kind deleteKind, keys []string) {
	local := s.localCall(c)
	var owner string
	if local {
		var err error
		if owner, err = s.assistantCaller(c); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	var targets []deleteTarget
	seen := map[string]bool{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		target, err := kind.Resolve(key)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name at least one of the " + kind.Plural + " to delete."})
		return
	}
	if local && s.askApproval(c, deletesApproval(owner, kind, targets)) {
		return
	}
	results := []gin.H{}
	for _, r := range deleteAll(targets, false) {
		entry := gin.H{"name": r.Name}
		if r.Err != nil {
			entry["error"] = r.Err.Error()
		} else {
			entry["deleted"] = r.Text
		}
		results = append(results, entry)
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// deletesApproval is the one approval a delete of the targets waits for. Its
// key is the same for the same targets in any order, the dialog lists every
// target's rows, and it touches every project one of them belongs to.
func deletesApproval(owner string, kind deleteKind, targets []deleteTarget) approval.Request {
	req := approval.Request{Owner: owner, Kind: kind.ID, What: "Delete " + targets[0].Name}
	if len(targets) > 1 {
		req.What = fmt.Sprintf("Delete %d %s", len(targets), kind.Plural)
	}
	keys := make([]string, 0, len(targets))
	for _, t := range targets {
		keys = append(keys, t.Key)
		req.Details = append(req.Details, t.Details...)
		if t.Project != "" && !slices.Contains(req.Projects, t.Project) {
			req.Projects = append(req.Projects, t.Project)
		}
	}
	slices.Sort(keys)
	req.Key = strings.Join(keys, "\n")
	req.Run = func() (string, error) { return deletesOutcome(deleteAll(targets, true)) }
	return req
}

// deleteAll deletes every target and goes on where one fails. A target an
// earlier one took along is deleted, and says with which.
func deleteAll(targets []deleteTarget, wait bool) []deleteResult {
	results := make([]deleteResult, 0, len(targets))
	takenBy := map[string]string{}
	for _, t := range targets {
		if by, ok := takenBy[t.Key]; ok {
			text := strings.ToUpper(t.Name[:1]) + t.Name[1:] + " went with " + by + "."
			results = append(results, deleteResult{Name: t.Name, Text: text})
			continue
		}
		text, along, err := t.Delete(wait)
		for _, key := range along {
			takenBy[key] = t.Name
		}
		results = append(results, deleteResult{Name: t.Name, Text: text, Err: err})
	}
	return results
}

// deletesOutcome is what the note of an approved delete reads: a single
// target's outcome as it stands, several targets' failures with their reason
// first and then what was deleted, failed when any of them failed.
func deletesOutcome(results []deleteResult) (string, error) {
	if len(results) == 1 {
		return results[0].Text, results[0].Err
	}
	var failed, done []string
	for _, r := range results {
		if r.Err != nil {
			failed = append(failed, "Could not delete "+r.Name+": "+r.Err.Error()+".")
		} else {
			done = append(done, r.Text)
		}
	}
	text := strings.Join(append(failed, done...), " ")
	if len(failed) > 0 {
		return "", errors.New(text)
	}
	return text, nil
}
