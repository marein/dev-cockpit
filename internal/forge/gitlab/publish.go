package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/marein/dev-cockpit/internal/forge"
)

// mergeRequestDiff is one file of a merge request's diff, its hunks in diff.
// A file GitLab shows too large or collapsed comes without them.
type mergeRequestDiff struct {
	OldPath     string `json:"old_path"`
	NewPath     string `json:"new_path"`
	Diff        string `json:"diff"`
	DeletedFile bool   `json:"deleted_file"`
}

// ChangeDiff reads the merge request's diff refs, the commits its latest
// version of the diff is made of, and its diffs page by page, which GitLab
// lists from 15.7 on. A merge request just opened or pushed to has no diff
// refs until GitLab has made that version.
func (Provider) ChangeDiff(ctx context.Context, c *forge.Client, b forge.Binding, token string, ch forge.Change) (forge.Diff, error) {
	a := api(c, b.Host, token)
	mergeRequest := "/projects/" + url.PathEscape(b.Path) + "/merge_requests/" + strconv.Itoa(ch.Number)
	var mr struct {
		DiffRefs *struct {
			BaseSHA  string `json:"base_sha"`
			StartSHA string `json:"start_sha"`
			HeadSHA  string `json:"head_sha"`
		} `json:"diff_refs"`
	}
	if _, err := a.Get(ctx, mergeRequest, &mr); err != nil {
		return forge.Diff{}, changeRefusal(a, b, ch.Number, err)
	}
	if mr.DiffRefs == nil || mr.DiffRefs.HeadSHA == "" {
		return forge.Diff{}, fmt.Errorf("GitLab is still making the diff of merge request !%d, publish again in a moment.", ch.Number)
	}
	diffs, partial, err := pages[mergeRequestDiff](ctx, a, mergeRequest+"/diffs", nil)
	var status *forge.StatusError
	if errors.As(err, &status) && status.Code == http.StatusNotFound {
		return forge.Diff{}, fmt.Errorf("GitLab lists no diff of merge request !%d, publishing review comments needs GitLab 15.7 or later.", ch.Number)
	}
	if err != nil {
		return forge.Diff{}, readRefusal(a, b, err)
	}
	d := forge.Diff{Head: mr.DiffRefs.HeadSHA, Base: mr.DiffRefs.BaseSHA, Start: mr.DiffRefs.StartSHA, Files: map[string]forge.DiffFile{}, Partial: partial}
	for _, f := range diffs {
		if f.DeletedFile {
			continue
		}
		d.Files[f.NewPath] = forge.DiffFile{OldPath: f.OldPath, Lines: forge.HunkLines(f.Diff)}
	}
	return d, nil
}

// PublishNote opens a discussion on the diff at the line p names on the new
// side of the diff's latest version. A line both sides share needs its old
// line as well, else GitLab refuses the position.
func (Provider) PublishNote(ctx context.Context, c *forge.Client, b forge.Binding, token string, ch forge.Change, d forge.Diff, p forge.Placed) (forge.Note, error) {
	a := api(c, b.Host, token)
	position := map[string]any{
		"position_type": "text",
		"base_sha":      d.Base,
		"start_sha":     d.Start,
		"head_sha":      d.Head,
		"old_path":      p.OldPath,
		"new_path":      p.Path,
		"new_line":      p.Line,
	}
	if p.OldLine > 0 {
		position["old_line"] = p.OldLine
	}
	return startDiscussion(ctx, a, b, ch, map[string]any{"body": asText(p.Body), "position": position})
}

// startDiscussion opens a discussion of the merge request ch with what sent
// holds and answers its first note.
func startDiscussion(ctx context.Context, a forge.API, b forge.Binding, ch forge.Change, sent map[string]any) (forge.Note, error) {
	var made discussion
	path := "/projects/" + url.PathEscape(b.Path) + "/merge_requests/" + strconv.Itoa(ch.Number) + "/discussions"
	ref := "merge request !" + strconv.Itoa(ch.Number)
	if err := a.Post(ctx, path, sent, &made); err != nil {
		var status *forge.StatusError
		if errors.As(err, &status) && status.Code == http.StatusForbidden && !insufficientScope(status.Body) {
			return forge.Note{}, fmt.Errorf("The token's account may not comment on %s in %s.", ref, b.Address())
		}
		return forge.Note{}, writeRefusal(a, b, err, "comment on "+ref, ref)
	}
	if len(made.Notes) == 0 {
		return forge.Note{}, fmt.Errorf("GitLab answered the comment on %s without its note.", ref)
	}
	n := made.Notes[0]
	id := strconv.FormatInt(n.ID, 10)
	return forge.Note{ID: id, Author: n.Author.Username, Body: n.Body, Created: n.CreatedAt, URL: ch.URL + "#note_" + id}, nil
}

// PublishGeneral opens a discussion of the merge request without a place on
// its diff, a thread GitLab answers and resolves like one on a line.
func (Provider) PublishGeneral(ctx context.Context, c *forge.Client, b forge.Binding, token string, ch forge.Change, body string) (forge.Note, error) {
	return startDiscussion(ctx, api(c, b.Host, token), b, ch, map[string]any{"body": asText(body)})
}
