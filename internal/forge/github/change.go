package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/marein/dev-cockpit/internal/forge"
)

// repoInfo is what GitHub says of a repository itself. HasPullRequests is nil
// on a server too old to name the switch.
type repoInfo struct {
	DefaultBranch   string `json:"default_branch"`
	HasPullRequests *bool  `json:"has_pull_requests"`
}

// DefaultBranch reads the default branch of the repository. One whose pull
// requests are switched off has none to merge into.
func (Provider) DefaultBranch(ctx context.Context, c *forge.Client, b forge.Binding, token string) (string, error) {
	a := api(c, b.Host, token)
	var r repoInfo
	if _, err := a.Get(ctx, repository(b), &r); err != nil {
		return "", readRefusal(a, b, err)
	}
	if r.HasPullRequests != nil && !*r.HasPullRequests {
		return "", pullsOff(b)
	}
	if r.DefaultBranch == "" {
		return "", fmt.Errorf("%s has no default branch on GitHub, its repository is empty.", b.Address())
	}
	return r.DefaultBranch, nil
}

// pullsSwitchedOff says whether err, GitHub's answer to a request on the
// pull requests of b's repository, comes from pull requests switched off in
// the repository's settings: GitHub then answers every such request with
// 404, which alone reads like a repository it hides.
func pullsSwitchedOff(ctx context.Context, a forge.API, b forge.Binding, err error) bool {
	var status *forge.StatusError
	if !errors.As(err, &status) || status.Code != http.StatusNotFound {
		return false
	}
	var r repoInfo
	if _, err := a.Get(ctx, repository(b), &r); err != nil {
		return false
	}
	return r.HasPullRequests != nil && !*r.HasPullRequests
}

func pullsOff(b forge.Binding) error {
	return fmt.Errorf("Pull requests are switched off in the settings of %s on GitHub.", b.Address())
}

// OpenChange opens a pull request of the repository's own branch source,
// named with its owner like the read asks for it, into o.Target.
func (Provider) OpenChange(ctx context.Context, c *forge.Client, b forge.Binding, token, source string, o forge.NewChange) (forge.Change, error) {
	a := api(c, b.Host, token)
	owner, _, _ := strings.Cut(b.Path, "/")
	body := map[string]any{"title": o.Title, "head": owner + ":" + source, "base": o.Target, "body": o.Body, "draft": o.Draft}
	var p pull
	if err := a.Post(ctx, repository(b)+"/pulls", body, &p); err != nil {
		if pullsSwitchedOff(ctx, a, b, err) {
			return forge.Change{}, pullsOff(b)
		}
		return forge.Change{}, openRefusal(a, b, err)
	}
	return p.neutral(), nil
}

// openRefusal says what an answer GitHub gave to the open of a pull request
// means: a token that may not, which GitHub answers with 403, or with 404
// where it hides the repository from the token; a pull request GitHub
// refuses in words of its own, one of the branch already among them; else
// what readRefusal says.
func openRefusal(a forge.API, b forge.Binding, err error) error {
	var status *forge.StatusError
	if !errors.As(err, &status) {
		return readRefusal(a, b, err)
	}
	scope := tokenScope("Pull requests")
	message := status.Message()
	switch {
	case status.Code == http.StatusForbidden && (message == "" || strings.HasPrefix(message, "Resource not accessible")):
		return fmt.Errorf("The token may not open pull requests in %s: %s", b.Address(), scope)
	case status.Code == http.StatusNotFound:
		return fmt.Errorf("GitHub knows no repository %s, or the token may not open pull requests in it: %s", b.Address(), scope)
	case message != "" && (status.Code == http.StatusForbidden || status.Code == http.StatusUnprocessableEntity):
		return fmt.Errorf("GitHub refused to open the pull request: %s.", strings.TrimRight(message, "."))
	}
	return readRefusal(a, b, err)
}
