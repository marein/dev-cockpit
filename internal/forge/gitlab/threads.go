package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
)

type discussion struct {
	ID    string `json:"id"`
	Notes []note `json:"notes"`
}

type note struct {
	ID     int64  `json:"id"`
	Body   string `json:"body"`
	Author struct {
		Username string `json:"username"`
	} `json:"author"`
	CreatedAt  time.Time `json:"created_at"`
	System     bool      `json:"system"`
	Resolvable bool      `json:"resolvable"`
	Resolved   bool      `json:"resolved"`
	Position   *position `json:"position"`
}

// position is where a diff note stands: the base and the head of the
// version of the diff it was written on, and its line on the new side, the
// old side or both, a line both sides share; none for a note on a whole file
// or an image.
type position struct {
	BaseSHA string `json:"base_sha"`
	HeadSHA string `json:"head_sha"`
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
	OldLine *int   `json:"old_line"`
	NewLine *int   `json:"new_line"`
}

// Threads reads the discussions of the merge request, a thread each, after
// the merge request itself, whose latest version of the diff tells which
// diff notes are outdated: GitLab moves a note along to a new version while
// its line stays, one whose line changed stays on the version it was written
// on. A note links to its place on the merge request's page, the API names
// no page of its own.
func (Provider) Threads(ctx context.Context, c *forge.Client, b forge.Binding, token string, number int) (forge.Review, error) {
	a := api(c, b.Host, token)
	mergeRequest := "/projects/" + url.PathEscape(b.Path) + "/merge_requests/" + strconv.Itoa(number)
	var mr struct {
		WebURL   string `json:"web_url"`
		DiffRefs *struct {
			HeadSHA string `json:"head_sha"`
		} `json:"diff_refs"`
	}
	if _, err := a.Get(ctx, mergeRequest, &mr); err != nil {
		return forge.Review{}, changeRefusal(a, b, number, err)
	}
	latest := ""
	if mr.DiffRefs != nil {
		latest = mr.DiffRefs.HeadSHA
	}
	discussions, partial, err := pages[discussion](ctx, a, mergeRequest+"/discussions", nil)
	if err != nil {
		return forge.Review{}, changeRefusal(a, b, number, err)
	}
	review := forge.Review{Partial: partial}
	for _, d := range discussions {
		t, diff, ok := d.thread(mr.WebURL, latest)
		switch {
		case !ok:
		case diff:
			review.Diff = append(review.Diff, t)
		default:
			review.General = append(review.General, t)
		}
	}
	return review, nil
}

// thread is the discussion as a thread without GitLab's system notes, and
// whether it stands on the diff; none when only system notes are left. It
// stands where its first note does and is resolved when every note that
// can be is.
func (d discussion) thread(page, latest string) (forge.Thread, bool, bool) {
	t := forge.Thread{ID: d.ID}
	var at *position
	resolved := true
	for _, n := range d.Notes {
		if n.System {
			continue
		}
		if len(t.Notes) == 0 {
			at = n.Position
		}
		if n.Resolvable {
			t.Resolvable = true
			resolved = resolved && n.Resolved
		}
		id := strconv.FormatInt(n.ID, 10)
		t.Notes = append(t.Notes, forge.Note{ID: id, Author: n.Author.Username, Body: n.Body, Created: n.CreatedAt, URL: page + "#note_" + id})
	}
	if len(t.Notes) == 0 {
		return forge.Thread{}, false, false
	}
	t.Resolved = t.Resolvable && resolved
	if at == nil {
		return t, false, true
	}
	t.Path = at.NewPath
	switch {
	case at.NewLine != nil:
		t.Line, t.Side, t.Commit = *at.NewLine, forge.NewSide, at.HeadSHA
	case at.OldLine != nil:
		t.Path, t.Line, t.Side, t.Commit = at.OldPath, *at.OldLine, forge.OldSide, at.BaseSHA
	}
	t.Outdated = at.HeadSHA != "" && latest != "" && at.HeadSHA != latest
	return t, true, true
}

// changeRefusal says what an answer GitLab gave to a read of merge request
// number means: one it lacks, deleted after a read of the branch named it, or
// one it hides, else what readRefusal says.
func changeRefusal(a forge.API, b forge.Binding, number int, err error) error {
	var status *forge.StatusError
	if errors.As(err, &status) && status.Code == http.StatusNotFound {
		return fmt.Errorf("GitLab knows no merge request !%d in %s, or the token may not read it.", number, b.Address())
	}
	return readRefusal(a, b, err)
}
