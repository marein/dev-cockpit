package github

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/marein/dev-cockpit/internal/forge"
)

// ownPage is the most recently pushed repositories a search of the account's
// own reads, GitHub's largest page: GitHub's search cannot be asked for the
// repositories an account collaborates on, its list of them can, and the one
// page of it the door keeps by its ETag asks again for nothing while the
// list did not change.
const ownPage = 100

// accountName is the shape of a GitHub account's name, which a search qualifier
// takes.
var accountName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38})$`)

// listed is a repository as GitHub's lists and its search answer it, the
// fields a search reads. An internal repository of an enterprise is private
// too and says so in its visibility.
type listed struct {
	FullName   string `json:"full_name"`
	SSHURL     string `json:"ssh_url"`
	CloneURL   string `json:"clone_url"`
	Private    bool   `json:"private"`
	Visibility string `json:"visibility"`
	Fork       bool   `json:"fork"`
	Archived   bool   `json:"archived"`
}

func (r listed) found() forge.Repository {
	visibility := ""
	switch {
	case r.Visibility == forge.VisibilityInternal:
		visibility = forge.VisibilityInternal
	case r.Private:
		visibility = forge.VisibilityPrivate
	}
	return forge.Repository{Path: r.FullName, SSHURL: r.SSHURL, HTTPURL: r.CloneURL, Visibility: visibility, Fork: r.Fork, Archived: r.Archived}
}

// OwnRepositories reads the repositories the token's user owns, collaborates
// on and reaches through an organization, the most recently pushed first,
// and keeps those whose path carries every word of term.
func (Provider) OwnRepositories(ctx context.Context, c *forge.Client, h forge.Host, token, term string, limit int) ([]forge.Repository, error) {
	a := api(c, h, token)
	var list []listed
	if _, err := a.Get(ctx, "/user/repos?affiliation=owner,collaborator,organization_member&sort=pushed&per_page="+strconv.Itoa(ownPage), &list); err != nil {
		return nil, refusal(a, err)
	}
	words := forge.SearchWords(term)
	var out []forge.Repository
	for _, r := range list {
		if len(out) == limit {
			break
		}
		if forge.Carries(r.FullName, words) {
			out = append(out, r.found())
		}
	}
	return out, nil
}

// PublicRepositories asks GitHub's search for the public repositories whose
// name carries the name words of term, an owner named before a slash as the
// account they belong to, GitHub's best match first.
func (Provider) PublicRepositories(ctx context.Context, c *forge.Client, h forge.Host, token, term string, limit int) ([]forge.Repository, error) {
	names, owners := forge.NameWords(term)
	if len(names) == 0 {
		return nil, nil
	}
	query := strings.Join(names, " ") + " in:name is:public"
	for _, owner := range owners {
		if accountName.MatchString(owner) {
			query += " user:" + owner
		}
	}
	a := searchAPI(c, h, token)
	var answer struct {
		Items []listed `json:"items"`
	}
	if _, err := a.Get(ctx, "/repositories?"+url.Values{"q": {query}, "per_page": {strconv.Itoa(limit)}}.Encode(), &answer); err != nil {
		return nil, refusal(a, err)
	}
	out := make([]forge.Repository, 0, min(len(answer.Items), limit))
	for _, r := range answer.Items[:min(len(answer.Items), limit)] {
		out = append(out, r.found())
	}
	return out, nil
}

// searchAPI is GitHub's search as an API of its own. GitHub limits searches
// apart from every other request, and the door holds back what the server
// limits by the API's address: bound below its own, a search that used up
// its limit holds back the next searches alone, never the reads of a
// branch's status.
func searchAPI(c *forge.Client, h forge.Host, token string) forge.API {
	return c.API(apiBase(h)+"/search", h, token, headers())
}
