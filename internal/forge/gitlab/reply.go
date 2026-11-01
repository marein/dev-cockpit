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

// Reply adds a note to the merge request's discussion.
func (Provider) Reply(ctx context.Context, c *forge.Client, b forge.Binding, token string, ch forge.Change, t forge.Thread, body string) (forge.Note, error) {
	a := api(c, b.Host, token)
	ref := "merge request !" + strconv.Itoa(ch.Number)
	var n note
	if err := a.Post(ctx, discussionPath(b, ch, t)+"/notes", map[string]any{"body": asText(body)}, &n); err != nil {
		var status *forge.StatusError
		if errors.As(err, &status) && status.Code == http.StatusForbidden && !insufficientScope(status.Body) {
			return forge.Note{}, fmt.Errorf("The token's account may not comment on %s in %s.", ref, b.Address())
		}
		return forge.Note{}, writeRefusal(a, b, err, "answer the thread on "+ref, "thread of "+ref)
	}
	if n.ID == 0 {
		return forge.Note{}, fmt.Errorf("GitLab answered the reply on %s without its note.", ref)
	}
	id := strconv.FormatInt(n.ID, 10)
	return forge.Note{ID: id, Author: n.Author.Username, Body: n.Body, Created: n.CreatedAt, URL: ch.URL + "#note_" + id}, nil
}

// Resolve resolves the merge request's discussion, which GitLab allows the
// Developer role and the merge request's author.
func (Provider) Resolve(ctx context.Context, c *forge.Client, b forge.Binding, token string, ch forge.Change, t forge.Thread) error {
	a := api(c, b.Host, token)
	ref := "merge request !" + strconv.Itoa(ch.Number)
	if err := a.Put(ctx, discussionPath(b, ch, t)+"?resolved=true", nil, nil); err != nil {
		var status *forge.StatusError
		if errors.As(err, &status) && status.Code == http.StatusForbidden && !insufficientScope(status.Body) {
			return fmt.Errorf("The token's account may not resolve threads of %s in %s, it needs the Developer role there or to have opened it.", ref, b.Address())
		}
		return writeRefusal(a, b, err, "resolve the thread on "+ref, "thread of "+ref)
	}
	return nil
}

func discussionPath(b forge.Binding, ch forge.Change, t forge.Thread) string {
	return "/projects/" + url.PathEscape(b.Path) + "/merge_requests/" + strconv.Itoa(ch.Number) + "/discussions/" + url.PathEscape(t.ID)
}
