package gitlab

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/marein/dev-cockpit/internal/forge"
)

// draftMark is the mark a draft's title opens with: GitLab's API takes no
// other way to open a draft than the marks it reads off a title.
const draftMark = "Draft: "

// DefaultBranch reads the default branch of the project.
func (Provider) DefaultBranch(ctx context.Context, c *forge.Client, b forge.Binding, token string) (string, error) {
	a := api(c, b.Host, token)
	var project struct {
		DefaultBranch string `json:"default_branch"`
	}
	if _, err := a.Get(ctx, "/projects/"+url.PathEscape(b.Path), &project); err != nil {
		return "", readRefusal(a, b, err)
	}
	if project.DefaultBranch == "" {
		return "", fmt.Errorf("%s has no default branch on GitLab, its repository is empty.", b.Address())
	}
	return project.DefaultBranch, nil
}

// OpenChange opens a merge request of source into the project's own branch
// o.Target, a draft by the mark its title opens with.
func (Provider) OpenChange(ctx context.Context, c *forge.Client, b forge.Binding, token, source string, o forge.NewChange) (forge.Change, error) {
	a := api(c, b.Host, token)
	title := o.Title
	if o.Draft && !drafted(title) {
		title = draftMark + title
	}
	body := map[string]any{"source_branch": source, "target_branch": o.Target, "title": title, "description": asText(o.Body)}
	var mr mergeRequest
	if err := a.Post(ctx, "/projects/"+url.PathEscape(b.Path)+"/merge_requests", body, &mr); err != nil {
		return forge.Change{}, writeRefusal(a, b, err, "open a merge request", "")
	}
	return mr.neutral(), nil
}

// drafted reports whether a title opens with a mark GitLab reads a draft by.
func drafted(title string) bool {
	lower := strings.ToLower(title)
	for _, mark := range []string{"draft:", "[draft]", "(draft)"} {
		if strings.HasPrefix(lower, mark) {
			return true
		}
	}
	return false
}
